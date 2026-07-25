// Package trajeval scores real agent runs post-hoc from ingested events
// (Verification Contour v2, Pipeline A). Deterministic, best-effort: a
// malformed or partial event stream skips a detector rather than panicking,
// mirroring internal/evals tolerance-by-contract.
package trajeval

import (
	"database/sql"
	"encoding/json"
	"time"
)

const searchLoopThreshold = 4

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

// detectSearchLoop returns a finding when >= searchLoopThreshold consecutive
// tool_call events share one tool name with no intervening progress event.
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
		case e.typ == "tool_call" && e.tool == runTool:
			run = append(run, e.turnID)
		case e.typ == "tool_call":
			if f := flush(); f != nil {
				return f
			}
			runTool, run = e.tool, []int64{e.turnID}
		case isProgress(e.typ):
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
func Compute(db *sql.DB, now time.Time) error {
	rows, err := db.Query(`SELECT DISTINCT session_id FROM events ORDER BY session_id`)
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

func persist(db *sql.DB, sessionID int64, agent string, fp bool, fs []*finding, now time.Time) error {
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
	for _, f := range fs {
		ids, _ := json.Marshal(f.evidenceIDs)
		if _, err := tx.Exec(
			`INSERT INTO trajectory_findings(score_id, kind, severity, evidence_turn_ids) VALUES (?,?,?,?)`,
			scoreID, f.kind, f.severity, string(ids)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
