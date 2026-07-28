package phasediag

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// Blocker kinds, in the order Diagnose emits them (most actionable first).
const (
	KindDepIncomplete     = "dep-incomplete"
	KindDepUnmerged       = "dep-unmerged"
	KindBranchBlocksRetry = "branch-blocks-retry"
	KindBranchDirty       = "branch-dirty"
	KindNoCriteria        = "no-criteria"
)

// Blocker is one reason the phase did not progress, or one thing standing between
// the user and a successful retry. Summary is rendered verbatim by the UI.
type Blocker struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// AgentMessage is the executor's own last word, for the cases the daemon cannot
// prove. Text is the newest assistant turn of the run's session, truncated on a
// rune boundary.
type AgentMessage struct {
	SessionUUID string `json:"sessionUuid"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

// Diagnosis is the full answer for one phase.
type Diagnosis struct {
	PhaseID       int64  `json:"phaseId"`
	Seq           int    `json:"seq"`
	Name          string `json:"name"`
	RunOutcome    string `json:"runOutcome"`
	CriteriaTotal int    `json:"criteriaTotal"`
	// CriteriaBefore is nil when run_checkboxes_before is NULL — the run's baseline
	// was never measured. A plain int would serialise that as 0 and let the UI render
	// a "0 → N" delta nobody measured, which is the exact claim the NULL policy in
	// OutcomeFromRow exists to refuse.
	CriteriaBefore *int          `json:"criteriaBefore"`
	CriteriaAfter  int           `json:"criteriaAfter"`
	RunStartedAt   *string       `json:"runStartedAt"`
	RunEndedAt     *string       `json:"runEndedAt"`
	RunError       *string       `json:"runError"`
	Blockers       []Blocker     `json:"blockers"`
	AgentMessage   *AgentMessage `json:"agentMessage"`
}

// ErrPhaseNotFound: no epic_phases row for the id (mapped to 404 by the api layer).
var ErrPhaseNotFound = errors.New("phasediag: phase not found")

// maxAgentText caps the executor excerpt shown in the modal.
const maxAgentText = 1200

// maxSubjects caps the commit subjects listed in a branch-dirty detail — enough
// to recognise the work, not a full log dump.
const maxSubjects = 20

// phaseRow is one epic_phases row joined to its project, as both the subject of a
// diagnosis and (partially) its dependencies.
type phaseRow struct {
	ID       int64
	Seq      int
	Name     string
	DocPath  string
	Total    int
	Done     int
	Complete bool // ticked criteria are the ONLY completion proof (see phaserun.depSatisfied)
}

// branchName is the deterministic run branch for a phase id — the same name
// phaserun hands worktree.Acquire ("phase-<id>" ⇒ swarm/phase-<id>).
func branchName(phaseID int64) string {
	return "swarm/phase-" + strconv.FormatInt(phaseID, 10)
}

// Diagnose builds the diagnosis for one phase. git may be nil, and the project may
// have no filesystem path — branch-derived blockers are then omitted rather than
// guessed; criteria and dependency blockers still render.
func Diagnose(db *sql.DB, git worktree.Git, phaseID int64) (Diagnosis, error) {
	var (
		d          Diagnosis
		taskID     int64
		depsJSON   string
		runState   string
		uuid       sql.NullString
		startedAt  sql.NullString
		endedAt    sql.NullString
		runErr     sql.NullString
		before     sql.NullInt64
		after      sql.NullInt64
		docPath    string
		projPath   sql.NullString
		total, don int
	)
	err := db.QueryRow(`
		SELECT e.workspace_task_id, e.seq, e.name, e.doc_path, e.depends_on,
		       e.checkboxes_total, e.checkboxes_done, e.run_state, e.run_session_uuid,
		       e.run_started_at, e.run_ended_at, e.run_error, e.run_checkboxes_before,
		       e.run_checkboxes_after, p.path
		  FROM epic_phases e
		  JOIN tasks t ON t.id = e.workspace_task_id
		  JOIN projects p ON p.id = t.project_id
		 WHERE e.id = ?`, phaseID).Scan(
		&taskID, &d.Seq, &d.Name, &docPath, &depsJSON,
		&total, &don, &runState, &uuid,
		&startedAt, &endedAt, &runErr, &before, &after,
		&projPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Diagnosis{}, ErrPhaseNotFound
	}
	if err != nil {
		return Diagnosis{}, err
	}

	d.PhaseID = phaseID
	d.CriteriaTotal = total
	// The run's right edge is the count stamped at exit (0042) when it exists;
	// otherwise the LIVE count, which may have moved since — the wsingest rescan
	// and TickPhaseChecklist both write checkboxes_done.
	d.CriteriaAfter = don
	if after.Valid {
		d.CriteriaAfter = int(after.Int64)
	}
	// A NULL baseline is reported as such (nil), not as a fabricated 0 — see the
	// field comment. The outcome derivation applies the same NULL policy via
	// OutcomeFromRow, the one code path the api layer's phase DTO also uses.
	if before.Valid {
		b := int(before.Int64)
		d.CriteriaBefore = &b
	}
	d.RunStartedAt = nullStr(startedAt)
	d.RunEndedAt = nullStr(endedAt)
	d.RunError = nullStr(runErr)
	d.RunOutcome = OutcomeFromRow(runState, total, don, before, after)
	d.Blockers = []Blocker{} // always a JSON array, never null

	// The base branch is the one signal every branch-derived blocker is relative
	// to. No repo path, no git seam, or a detached HEAD ⇒ we cannot name a base,
	// so those blockers are skipped entirely rather than guessed against a
	// hard-coded "main".
	repoRoot := projPath.String
	base := ""
	if repoRoot != "" && git != nil {
		base = baseBranch(git, repoRoot)
	}
	branchesKnown := base != ""

	deps, err := loadDeps(db, taskID, depsJSON)
	if err != nil {
		return Diagnosis{}, err
	}

	// 1. dep-incomplete — a dependency cannot prove it is done. A dependency with
	// NO criteria is equally unprovable (same rule as phaserun.depSatisfied), but
	// "is only 0/0 complete" implies a count that came up short; the truth is that
	// nothing was ever countable. Same Kind — the UI renders kinds, and a sixth one
	// would widen a contract other phases consume — different sentence.
	for _, dep := range deps {
		if dep.Complete {
			continue
		}
		summary := fmt.Sprintf("Phase %d is only %d/%d complete", dep.Seq, dep.Done, dep.Total)
		if dep.Total == 0 {
			summary = fmt.Sprintf(
				"Phase %d has no acceptance-criteria checkboxes, so its completion cannot be proven", dep.Seq)
		}
		d.Blockers = append(d.Blockers, Blocker{
			Kind:    KindDepIncomplete,
			Summary: summary,
			Detail:  fmt.Sprintf("phase %d — %s", dep.Seq, filepath.Base(dep.DocPath)),
		})
	}

	// 2. dep-unmerged — the dependency IS ticked, but its code never reached base.
	// This is the incident's real cause: the executor read a green dependency and
	// found none of its code in the tree it was given.
	if branchesKnown {
		for _, dep := range deps {
			if !dep.Complete {
				continue
			}
			branch := branchName(dep.ID)
			exists, ahead, _ := branchAhead(git, repoRoot, base, branch)
			if exists && ahead > 0 {
				d.Blockers = append(d.Blockers, Blocker{
					Kind: KindDepUnmerged,
					Summary: fmt.Sprintf("Phase %d is ticked, but its code is on %s — %s not merged into %s",
						dep.Seq, branch, plural(ahead, "commit"), base),
					Detail: fmt.Sprintf("%s → %s ahead, %s", branch, plural(ahead, "commit"), againstBase(base)),
				})
			}
		}
	}

	// 3/4. The phase's OWN leftover branch — mutually exclusive states: an empty
	// one is noise the next retry cleans up, a non-empty one holds work a retry
	// would collide with.
	if branchesKnown {
		branch := branchName(phaseID)
		exists, ahead, subjects := branchAhead(git, repoRoot, base, branch)
		switch {
		case exists && ahead == 0:
			d.Blockers = append(d.Blockers, Blocker{
				Kind:    KindBranchBlocksRetry,
				Summary: fmt.Sprintf("Leftover branch %s will be cleaned up automatically on retry", branch),
				Detail:  fmt.Sprintf("%s → 0 commits ahead, %s", branch, againstBase(base)),
			})
		case exists:
			d.Blockers = append(d.Blockers, Blocker{
				Kind: KindBranchDirty,
				Summary: fmt.Sprintf("Branch %s holds %s — merge or delete it before retrying",
					branch, plural(ahead, "commit")),
				// The base note goes LAST: the subjects are the answer to "what is on
				// this branch", newest first, and the qualifier belongs after them.
				Detail: strings.Join(append(subjects, againstBase(base)), "\n"),
			})
		}
	}

	// 5. no-criteria — nothing to measure, so no run of this phase can ever be
	// proven to have achieved anything.
	if total == 0 {
		d.Blockers = append(d.Blockers, Blocker{
			Kind:    KindNoCriteria,
			Summary: "This phase doc has no acceptance-criteria checkboxes, so progress cannot be measured",
			Detail:  docPath,
		})
	}

	d.AgentMessage = agentMessage(db, uuid.String)
	return d, nil
}

// loadDeps resolves depends_on (JSON seq numbers) to sibling phase rows, ascending
// by seq. Garbage JSON ⇒ no dependencies (the epics.go decodeIntList posture), and
// a seq with no sibling row is skipped: unknowable, not a blocker.
func loadDeps(db *sql.DB, taskID int64, depsJSON string) ([]phaseRow, error) {
	var seqs []int
	if err := json.Unmarshal([]byte(depsJSON), &seqs); err != nil {
		return nil, nil
	}
	sort.Ints(seqs)

	out := make([]phaseRow, 0, len(seqs))
	for _, seq := range seqs {
		var r phaseRow
		err := db.QueryRow(`
			SELECT id, seq, name, doc_path, checkboxes_total, checkboxes_done
			  FROM epic_phases
			 WHERE workspace_task_id = ? AND seq = ?
			 ORDER BY id LIMIT 1`, taskID, seq).Scan(
			&r.ID, &r.Seq, &r.Name, &r.DocPath, &r.Total, &r.Done)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Completion is proven by ticked criteria only — never by run_state. A
		// zero-criteria dependency can never prove it, so it counts as incomplete.
		r.Complete = r.Total > 0 && r.Done >= r.Total
		out = append(out, r)
	}
	return out, nil
}

// baseBranch returns the repo's current branch name — the same signal
// worktree.Manager.resolveStartPoint pins acquisitions to, so a diagnosis can
// never disagree with an acquisition about what "base" means. Detached HEAD or a
// git failure returns "", and every branch-derived blocker is then skipped.
func baseBranch(git worktree.Git, repoRoot string) string {
	out, err := git.Run(repoRoot, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// againstBase spells out what a commit count was compared with. baseBranch is
// deliberately the repo's CURRENT checkout (so a diagnosis can never disagree with
// worktree.Acquire about "base"), which means a user sitting on a feature branch
// sees a dependency merged into dev but not into that branch as dep-unmerged, and
// branch-blocks-retry / branch-dirty flip under the same skew. The base choice
// stays; every blocker derived from it says which branch it measured against, so
// base skew is recognisable instead of looking like a real problem.
func againstBase(base string) string {
	return "measured against the currently checked-out branch " + base
}

// branchAhead reports whether refs/heads/<branch> exists and how many commits it
// holds beyond base, plus their subjects (newest first, capped at maxSubjects) for
// the branch-dirty detail.
//
// ANY git error degrades to exists=false: a diagnosis explaining why a run failed
// must never itself fail because git was unhappy.
func branchAhead(git worktree.Git, repoRoot, base, branch string) (exists bool, ahead int, subjects []string) {
	ref := "refs/heads/" + branch
	if _, err := git.Run(repoRoot, "show-ref", "--verify", "--quiet", ref); err != nil {
		return false, 0, nil
	}
	out, err := git.Run(repoRoot, "rev-list", "--count", base+".."+ref)
	if err != nil {
		return false, 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n < 0 {
		return false, 0, nil
	}
	if n == 0 {
		return true, 0, nil
	}
	log, err := git.Run(repoRoot, "log", "--format=%s",
		"--max-count="+strconv.Itoa(maxSubjects), base+".."+ref)
	if err != nil {
		return false, 0, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			subjects = append(subjects, s)
		}
	}
	return true, n, subjects
}

// agentMessage returns the executor's newest assistant prose for the run's
// session, nil when there is no uuid or nothing ingested yet. turns.text holds
// text blocks only — ingest drops thinking blocks (migration 0005) — so this is
// the executor's own explanation, never extended thinking.
func agentMessage(db *sql.DB, uuid string) *AgentMessage {
	if uuid == "" {
		return nil
	}
	var text sql.NullString
	err := db.QueryRow(`
		SELECT tr.text
		  FROM turns tr JOIN sessions se ON se.id = tr.session_id
		 WHERE se.session_uuid=? AND tr.role='assistant' AND tr.text IS NOT NULL
		 ORDER BY tr.seq DESC LIMIT 1`, uuid).Scan(&text)
	if err != nil || !text.Valid {
		return nil
	}
	trimmed := strings.TrimSpace(text.String)
	if trimmed == "" {
		return nil
	}
	msg := &AgentMessage{SessionUUID: uuid, Text: trimmed}
	// Cut on a RUNE boundary: a byte-wise slice would split a multi-byte rune and
	// render as a replacement char in the modal.
	if r := []rune(trimmed); len(r) > maxAgentText {
		msg.Text = string(r[:maxAgentText])
		msg.Truncated = true
	}
	return msg
}

// plural renders "1 commit" / "N commits".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// nullStr converts a NULL-able column to an omitted (nil) JSON value.
func nullStr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}
