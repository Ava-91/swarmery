// Package trajeval scores real agent runs post-hoc from ingested events
// (Verification Contour v2, Pipeline A). Deterministic, best-effort: a
// malformed or partial event stream skips a detector rather than panicking,
// mirroring internal/evals tolerance-by-contract.
package trajeval

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
)

// normAgent folds "core:tech-lead" and "tech-lead" to the same lowercase key.
// Twin of internal/advisor normAgent — keep in lockstep.
func normAgent(t string) string {
	if i := strings.LastIndexByte(t, ':'); i >= 0 {
		t = t[i+1:]
	}
	return strings.ToLower(t)
}

const searchLoopThreshold = 4

// searchTools are the tools a SEARCH loop can actually be made of: repeated
// LOOKING that never turns into doing.
//
// The detector used to accept any tool name, and on the live corpus that made
// the metric mean something other than its name. Of 557 recorded search-loop
// findings, 83% were runs of Bash and 3% were runs of Edit — a run of Edit
// calls is the agent producing work, and a run of Bash calls is how anything
// gets built or tested here. Worse, the Bash command-shape guard shipped in
// this same cycle explicitly asks agents to issue ONE operation per call,
// which raises consecutive Bash calls by design: left alone, the deterministic
// gate would have inflated the very number the next retro reads as "the
// searching got worse".
//
// So the run is restricted to the tools whose repetition without progress is
// genuinely a pathology. A tool outside this set ends the current run and does
// not begin one.
var searchTools = map[string]bool{
	"Grep":         true,
	"Glob":         true,
	"Read":         true,
	"NotebookRead": true,
	"WebSearch":    true,
	"WebFetch":     true,
}

// progressTools are tool calls that produce work, so they break a run even
// when the ingest pipeline emitted no file_change event for them — which is
// why 15 findings in the live corpus were runs of Edit in the first place.
var progressTools = map[string]bool{
	"Edit":         true,
	"MultiEdit":    true,
	"Write":        true,
	"NotebookEdit": true,
}

// event is one row of the events table, reduced to the fields detectors need.
type event struct {
	turnID int64
	typ    string
	tool   string
}

// finding is one detected anti-pattern, pre-persistence.
type finding struct {
	kind        string
	severity    string
	evidenceIDs []int64
}

// isProgress reports whether an event breaks a search-loop run.
func isProgress(typ string) bool {
	return typ == "file_change" || typ == "commit" || typ == "test_run"
}

// isSearchCall reports whether an event extends a search run.
func isSearchCall(e event) bool {
	return e.typ == "tool_call" && searchTools[e.tool]
}

// breaksRun reports whether an event ends the current run: a progress event,
// or a tool call that produces work. Everything the agent does that is not
// looking counts as having stopped looking.
func breaksRun(e event) bool {
	return isProgress(e.typ) || (e.typ == "tool_call" && progressTools[e.tool])
}

// detectSearchLoop returns the FIRST detected loop in the stream: a finding
// when >= searchLoopThreshold consecutive calls to the SAME search tool
// (searchTools) occur with no intervening progress — no file_change, commit or
// test_run event, and no call to a tool that produces work.
//
// Any other event ends the current run. A tool call outside searchTools ends it
// without starting a new one: four builds in a row are not a search loop, and
// counting them as one is what made this metric unreadable.
func detectSearchLoop(evs []event) *finding {
	var runTool string
	var run []int64
	flush := func() *finding {
		if len(run) >= searchLoopThreshold {
			return &finding{kind: "search-loop", severity: "warn", evidenceIDs: append([]int64(nil), run...)}
		}
		return nil
	}
	for _, e := range evs {
		switch {
		case isSearchCall(e) && e.tool == runTool:
			run = append(run, e.turnID)
		case isSearchCall(e):
			if f := flush(); f != nil {
				return f
			}
			runTool, run = e.tool, []int64{e.turnID}
		case breaksRun(e), e.typ == "tool_call":
			// Progress, work, or any non-search tool call: the agent stopped
			// looking, so whatever run was open ends here.
			if f := flush(); f != nil {
				return f
			}
			runTool, run = "", nil
		}
	}
	return flush()
}

// detectVerifySkip returns a finding when the session edited files but ran no
// test. Best-effort: a stream with no file_change is not a skip.
func detectVerifySkip(evs []event) *finding {
	var edited bool
	var editIDs []int64
	for _, e := range evs {
		switch e.typ {
		case "file_change":
			edited = true
			editIDs = append(editIDs, e.turnID)
		case "test_run":
			return nil
		}
	}
	if !edited {
		return nil
	}
	return &finding{kind: "verify-skip", severity: "warn", evidenceIDs: editIDs}
}

// firstPass is an MVP proxy: a run is first-pass when it produced no error
// events. Re-dispatch (advisor R4) integration is a documented follow-up.
func firstPass(evs []event) bool {
	for _, e := range evs {
		if e.typ == "error" {
			return false
		}
	}
	return true
}

// Compute scores every session that has events, persisting one row per
// (session, agent_name) idempotently. Best-effort per session: a failure on
// one session is skipped, never aborts the batch.
//
// System-project sessions (cwd = ingest.SystemDir(), legacy cwd "/") are
// daemon-spawned headless runs — trajjudge's own judge sessions among them.
// Scoring them would make every judge run a fresh judge candidate, so the
// pool never drains and each daemon restart burns a full batch. They are
// never scored, and score rows accumulated before this guard are pruned
// (findings cascade).
func Compute(db *sql.DB, now time.Time) error {
	sysDir := ingest.SystemDir()
	if sysDir == "" {
		// Home unresolvable: an empty string would match NULL-cwd sessions
		// via COALESCE below; use a path no cwd can equal instead.
		sysDir = "\x00unresolvable"
	}
	if _, err := db.Exec(`
		DELETE FROM trajectory_scores WHERE session_id IN
		  (SELECT id FROM sessions WHERE cwd IN (?, '/'))`, sysDir); err != nil {
		return err
	}
	rows, err := db.Query(`
		SELECT DISTINCT e.session_id
		FROM events e
		JOIN sessions s ON s.id = e.session_id
		WHERE COALESCE(s.cwd, '') NOT IN (?, '/')
		ORDER BY e.session_id`, sysDir)
	if err != nil {
		return err
	}
	defer rows.Close()
	var sessionIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		sessionIDs = append(sessionIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, sid := range sessionIDs {
		byAgent, err := loadEventsByAgent(db, sid)
		if err != nil {
			continue // best-effort skip
		}
		for agent, evs := range byAgent {
			if len(evs) == 0 {
				continue
			}
			var found []*finding
			if f := detectSearchLoop(evs); f != nil {
				found = append(found, f)
			}
			if f := detectVerifySkip(evs); f != nil {
				found = append(found, f)
			}
			agent = normAgent(agent)
			if err := persist(db, sid, agent, firstPass(evs), found, now); err != nil {
				continue
			}
		}
	}
	return nil
}

// loadEventsByAgent returns a session's events grouped by the agent that
// produced them (turns.agent_name, NULL => "main"). Events with no turn (NULL
// turn_id) fold to "main".
func loadEventsByAgent(db *sql.DB, sessionID int64) (map[string][]event, error) {
	rows, err := db.Query(`
		SELECT COALESCE(t.agent_name, 'main'), COALESCE(e.turn_id, 0), e.type, COALESCE(e.tool_name, '')
		FROM events e
		LEFT JOIN turns t ON t.id = e.turn_id
		WHERE e.session_id = ?
		ORDER BY e.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]event{}
	for rows.Next() {
		var agent string
		var e event
		if err := rows.Scan(&agent, &e.turnID, &e.typ, &e.tool); err != nil {
			return nil, err
		}
		out[agent] = append(out[agent], e)
	}
	return out, rows.Err()
}

// persist writes or replaces the trajectory score and its findings for one
// (session, agent) pair within a single transaction.
func persist(db *sql.DB, sessionID int64, agent string, fp bool, findings []*finding, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Delete-then-insert for idempotent recompute (findings cascade).
	if _, err := tx.Exec(`DELETE FROM trajectory_scores WHERE session_id=? AND agent=?`, sessionID, agent); err != nil {
		return err
	}
	fpInt := 0
	if fp {
		fpInt = 1
	}
	res, err := tx.Exec(
		`INSERT INTO trajectory_scores(session_id, agent, first_pass, computed_at) VALUES (?,?,?,?)`,
		sessionID, agent, fpInt, now.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	scoreID, _ := res.LastInsertId()
	for _, f := range findings {
		ids, _ := json.Marshal(f.evidenceIDs)
		if _, err := tx.Exec(
			`INSERT INTO trajectory_findings(score_id, kind, severity, evidence_turn_ids) VALUES (?,?,?,?)`,
			scoreID, f.kind, f.severity, string(ids)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
