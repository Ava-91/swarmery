package api

import (
	"database/sql"
	"strconv"
)

// ---------------------------------------------------------------------------
// Which plan run does a session belong to?
//
// A headless plan run stamps a deterministic branch on every session it spawns:
// planrun/service.go builds "plan-<workspaceTaskId>", phaserun/service.go builds
// "phase-<epicPhaseId>", and worktree.branchName prefixes both with "swarm/".
// That branch is already in sessions.git_branch, so it IS the grouping key — no
// new column on sessions and no new ingest path. sessions.parent_uuid is not
// used: it has no observed JSONL source and is NULL in practice.
//
// Subagent sessions are the exception. They are ingested from the run
// worktree's own transcript tree and frequently carry no git_branch of their
// own, so for those the run marker is read back out of the worktree path in
// sessions.cwd (…/worktrees/<projectSlug>/phase-1280[/…]).
//
// Everything below builds SQLite EXPRESSIONS rather than doing the work in Go:
// listSessions is on the hot path, so the group resolves inside the one list
// query via LEFT JOINs on an INTEGER PRIMARY KEY — never a per-row follow-up.
// ---------------------------------------------------------------------------

// sessionPlanDTO names the plan run that spawned a session. Null on an ordinary
// interactive session. Role is "controller" for the plan run itself
// (swarm/plan-<taskId>) and "phase" for one phase of it (swarm/phase-<phaseId>);
// only the latter carries the Phase* fields.
type sessionPlanDTO struct {
	TaskID    int64   `json:"taskId"` // workspace task holding the plan
	Title     string  `json:"title"`  // plan title, for the group header
	Role      string  `json:"role"`   // controller | phase
	PhaseID   *int64  `json:"phaseId"`
	PhaseSeq  *int    `json:"phaseSeq"`
	PhaseName *string `json:"phaseName"`
}

// sqlSegmentAfter builds a SQLite expression for the path segment that follows
// the first occurrence of marker in path: over "/w/-repo/phase-1280/tools",
// sqlSegmentAfter("s.cwd", "/phase-") yields "1280". NULL when marker is absent.
//
// Appending '/' to the path guarantees a terminator, so the segment is the text
// up to the next slash whether or not cwd descends any deeper than the worktree
// root itself.
func sqlSegmentAfter(path, marker string) string {
	lit := "'" + marker + "'"
	at := "instr(" + path + ", " + lit + ")"
	start := at + " + " + strconv.Itoa(len(marker))
	return "CASE WHEN " + at + " > 0 THEN substr(" + path + ", " + start +
		", instr(substr(" + path + " || '/', " + start + "), '/') - 1) END"
}

// sqlDigitsOrNull wraps a SQLite text expression so it evaluates to the integer
// it spells, or NULL when it spells anything else. This is the guard that makes
// a hand-made branch like "swarm/plan-abc" resolve to no group instead of
// erroring: GLOB against a non-digit is false, so the CASE falls through.
// An empty segment casts to 0, which matches no row — also no group.
func sqlDigitsOrNull(expr string) string {
	return "CASE WHEN (" + expr + ") NOT GLOB '*[^0-9]*' THEN CAST((" + expr + ") AS INTEGER) END"
}

// runIDExpr builds the expression for the numeric run id of kind ("plan" or
// "phase") owning a session row.
//
// The stamped branch wins whenever the session has one of ours; the cwd
// fallback applies only to sessions with no swarm run branch at all (subagents,
// whose transcripts live under the run's worktree). Keeping that precedence
// explicit is what makes a malformed "swarm/plan-abc" yield NULL rather than
// quietly regrouping by path.
func runIDExpr(kind string) string {
	prefix := "swarm/" + kind + "-"
	fromBranch := sqlDigitsOrNull("substr(s.git_branch, " + strconv.Itoa(len(prefix)+1) + ")")
	fromCWD := sqlDigitsOrNull(sqlSegmentAfter("s.cwd", "/"+kind+"-"))
	return "CASE" +
		" WHEN s.git_branch LIKE '" + prefix + "%' THEN " + fromBranch +
		" WHEN s.git_branch IS NULL" +
		" OR (s.git_branch NOT LIKE 'swarm/plan-%' AND s.git_branch NOT LIKE 'swarm/phase-%')" +
		" THEN " + fromCWD +
		" END"
}

// sessionPlanGroupCols are the projection columns sessionSelect adds for the
// group descriptor; their order matches scanSession's tail.
const sessionPlanGroupCols = `,
	       plan_task.id, plan_task.title,
	       run_phase.id, run_phase.seq, run_phase.name, phase_task.id, phase_task.title`

// sessionPlanGroupJoins resolves the owning plan run for every row of
// sessionSelect. Both joins land on an INTEGER PRIMARY KEY, so each row costs a
// rowid lookup — epic_phases is never scanned.
//
// The phase join keys off epic_phases.run_branch (migration 0043, indexed by 0044) —
// the branch STAMPED at spawn — and only falls back to the row id when that finds
// nothing. epic_phases identity is doc_path, so a plan re-index replaces the row and
// mints a new id; matching on 'swarm/phase-' || id therefore stops resolving the
// moment a phase doc is renamed, which is exactly when a session is most worth
// finding. Observed on this machine: the sessions for swarm/phase-1279/1280 resolved
// to nothing, because ids 1279/1280 no longer existed.
//
// The fallback is still needed and is not dead code: rows that ran before 0043 and
// were missed by its backfill have a NULL run_branch, and subagent sessions carry no
// branch at all — only a cwd, from which runIDExpr can recover an id but never a
// branch string.
//
// MIN(id) makes the subquery deterministic if two rows ever carry one branch. The
// carry-over in wsingest.applyEpics drains the source row before the prune, so that
// should not happen; picking arbitrarily on a broken invariant would be worse.
var sessionPlanGroupJoins = `
	LEFT JOIN tasks plan_task ON plan_task.id = (` + runIDExpr("plan") + `)
	LEFT JOIN epic_phases run_phase ON run_phase.id = COALESCE(
		(SELECT MIN(ep.id) FROM epic_phases ep
		  WHERE s.git_branch LIKE 'swarm/phase-%' AND ep.run_branch = s.git_branch),
		(` + runIDExpr("phase") + `))
	LEFT JOIN tasks phase_task ON phase_task.id = run_phase.workspace_task_id`

// sessionPlanGroupScan holds the sessionPlanGroupCols tail of a session row, so
// the scan order and the projection order are declared in one file.
type sessionPlanGroupScan struct {
	planTaskID     sql.NullInt64
	planTitle      sql.NullString
	phaseID        sql.NullInt64
	phaseSeq       sql.NullInt64
	phaseName      sql.NullString
	phaseTaskID    sql.NullInt64
	phaseTaskTitle sql.NullString
}

// dest returns the scan targets in sessionPlanGroupCols order.
func (g *sessionPlanGroupScan) dest() []any {
	return []any{
		&g.planTaskID, &g.planTitle,
		&g.phaseID, &g.phaseSeq, &g.phaseName, &g.phaseTaskID, &g.phaseTaskTitle,
	}
}

// dto folds the tail into the DTO, or nil when the session belongs to no plan
// run. The phase form wins: a session can only satisfy one of the two branch
// shapes, but resolving phases first keeps the precedence explicit. A phase
// whose workspace task no longer exists yields no group — the group header is
// the plan, and without it there is nothing to head.
func (g *sessionPlanGroupScan) dto() *sessionPlanDTO {
	if g.phaseID.Valid && g.phaseTaskID.Valid {
		out := &sessionPlanDTO{
			TaskID: g.phaseTaskID.Int64,
			Title:  g.phaseTaskTitle.String,
			Role:   "phase",
		}
		id := g.phaseID.Int64
		out.PhaseID = &id
		if g.phaseSeq.Valid {
			seq := int(g.phaseSeq.Int64)
			out.PhaseSeq = &seq
		}
		if g.phaseName.Valid {
			name := g.phaseName.String
			out.PhaseName = &name
		}
		return out
	}
	if g.planTaskID.Valid {
		return &sessionPlanDTO{
			TaskID: g.planTaskID.Int64,
			Title:  g.planTitle.String,
			Role:   "controller",
		}
	}
	return nil
}
