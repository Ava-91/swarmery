package advisor

import (
	"database/sql"
	"fmt"
)

// Closure metrics — what the NEXT retrospective reads to judge whether the
// closure gate worked.
//
// The retro this came from could measure churn for exactly TWO tasks in a
// fourteen-day window, which is why its churn section was one line long. Two
// numbers fix that, and both are exported so a query, the digest and any future
// page read the same values:
//
//   - how many tasks in a window carry a filled Completion Report, and
//   - the re-dispatch rate across all tasks that carry one.
//
// The second EXTENDS delegationShares/isRedispatch rather than re-deriving
// "was this re-dispatched?" — a second definition of re-dispatch would let the
// digest and this disagree about the same ledger, which is the class of drift
// the advisor's own comments keep warning about.

// ClosureStats is one window's closure measurables.
type ClosureStats struct {
	// Window boundaries, echoed so a stored result carries its own scope.
	From string
	To   string
	// Tasks is every non-archived task started in the window.
	Tasks int64
	// TasksWithReport is how many of them carry a non-empty Completion Report on
	// at least one phase. This is the count the retro's success metric names.
	TasksWithReport int64
	// PhasesWithReport / Phases are the same question one level down: the gate
	// acts per phase, so a plan can be half-reported and the task-level count
	// alone would hide that.
	Phases           int64
	PhasesWithReport int64
	// Delegations / Redispatches are the ledger rows those reported tasks
	// produced, and how many were re-dispatches by isRedispatch's definition.
	Delegations  int64
	Redispatches int64
}

// RedispatchRate is Redispatches/Delegations, or 0 when nothing was delegated.
// A rate rather than a count: session and task volume move between windows, and
// the retro's "255" already showed what happens when a raw count is compared
// against a differently-scoped one.
func (c ClosureStats) RedispatchRate() float64 {
	if c.Delegations == 0 {
		return 0
	}
	return float64(c.Redispatches) / float64(c.Delegations)
}

// ReportRate is TasksWithReport/Tasks, 0 when the window holds no tasks.
func (c ClosureStats) ReportRate() float64 {
	if c.Tasks == 0 {
		return 0
	}
	return float64(c.TasksWithReport) / float64(c.Tasks)
}

// Summary is the one-line rendering for a digest or a CLI.
func (c ClosureStats) Summary() string {
	return fmt.Sprintf(
		"%d/%d tasks carry a Completion Report (%.0f%%), %d/%d phases; re-dispatch %d/%d (%.0f%%) across reported tasks",
		c.TasksWithReport, c.Tasks, c.ReportRate()*100,
		c.PhasesWithReport, c.Phases,
		c.Redispatches, c.Delegations, c.RedispatchRate()*100)
}

// Closure computes ClosureStats over [from, to). Both bounds are RFC3339 UTC
// strings, the same form every other advisor window uses.
func Closure(db *sql.DB, from, to string) (ClosureStats, error) {
	c := ClosureStats{From: from, To: to}

	// Phase-level counts, and the task-level count derived from them: a task
	// "carries a report" when any of its phases does. TRIM guards the case the
	// parser already folds — a heading with nothing but whitespace under it.
	if err := db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN TRIM(COALESCE(e.completion_report,'')) <> '' THEN 1 ELSE 0 END), 0),
		       COUNT(DISTINCT e.workspace_task_id),
		       COUNT(DISTINCT CASE WHEN TRIM(COALESCE(e.completion_report,'')) <> ''
		                           THEN e.workspace_task_id END)
		  FROM epic_phases e
		  JOIN tasks t ON t.id = e.workspace_task_id
		  JOIN projects p ON p.id = t.project_id
		 WHERE t.started_at >= ? AND t.started_at < ? AND p.archived = 0`,
		from, to).Scan(&c.Phases, &c.PhasesWithReport, &c.Tasks, &c.TasksWithReport); err != nil {
		return c, err
	}

	// Re-dispatch across the tasks that DO carry a report — the retro's own
	// framing ("computed across more than two tasks"). The verdict test is
	// isRedispatch, the same predicate r4Redispatch uses.
	rows, err := db.Query(`
		SELECT COALESCE(td.verdict, '')
		  FROM task_delegations td
		  JOIN tasks t ON t.id = td.task_id
		  JOIN projects p ON p.id = t.project_id
		 WHERE t.started_at >= ? AND t.started_at < ? AND p.archived = 0
		   AND EXISTS (SELECT 1 FROM epic_phases e
		                WHERE e.workspace_task_id = t.id
		                  AND TRIM(COALESCE(e.completion_report,'')) <> '')`,
		from, to)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var verdict string
		if err := rows.Scan(&verdict); err != nil {
			return c, err
		}
		c.Delegations++
		if isRedispatch(verdict) {
			c.Redispatches++
		}
	}
	return c, rows.Err()
}
