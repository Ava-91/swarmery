package advisor

import (
	"testing"
)

// The two numbers the NEXT retrospective needs. The window this plan came from
// could measure churn for exactly two tasks, so the point of these is that they
// are queryable at all — and that re-dispatch is computed from the SAME
// predicate r4Redispatch uses, not a second definition of the same word.
func TestClosureStats(t *testing.T) {
	db := testDB(t) // seeds project 1

	// Three tasks in-window: one fully reported, one with an empty report, one
	// with no phases at all.
	mustExec(t, db, `INSERT INTO tasks (id, project_id, title, prompt, status, created_at, started_at, source, external_id)
		VALUES (1,1,'reported','p','done','2026-08-12T00:00:00Z','2026-08-12T00:00:00Z','workspace','t-1'),
		       (2,1,'unreported','p','done','2026-08-12T00:00:00Z','2026-08-12T00:00:00Z','workspace','t-2'),
		       (3,1,'no phases','p','done','2026-08-12T00:00:00Z','2026-08-12T00:00:00Z','workspace','t-3')`)
	mustExec(t, db, `INSERT INTO epic_phases (workspace_task_id, seq, name, doc_path, depends_on, checkboxes_total, checkboxes_done, completion_report)
		VALUES (1,1,'p1','/d/1.md','[]',2,2,'Shipped it; see internal/x/y.go.'),
		       (1,2,'p2','/d/2.md','[]',2,2,'   '),
		       (2,1,'p1','/d/3.md','[]',2,2,NULL)`)
	// Delegations: two on the reported task (one a re-dispatch by isRedispatch's
	// own grammar — the SAME predicate r4Redispatch uses), one on the
	// unreported task — which must not count, since the rate is scoped to tasks
	// that carry a report.
	mustExec(t, db, `INSERT INTO task_delegations (task_id, seq, agent, verdict)
		VALUES (1,1,'implementation-agent','approved'),
		       (1,2,'quality-reviewer','rejected'),
		       (2,1,'implementation-agent','rejected')`)

	c, err := Closure(db, "2026-08-11T00:00:00Z", "2026-08-25T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if c.Tasks != 2 {
		t.Errorf("Tasks = %d, want 2 (only tasks with phases)", c.Tasks)
	}
	if c.TasksWithReport != 1 {
		t.Errorf("TasksWithReport = %d, want 1", c.TasksWithReport)
	}
	if c.Phases != 3 || c.PhasesWithReport != 1 {
		t.Errorf("phases = %d/%d, want 1/3 reported — a whitespace-only report is not a report",
			c.PhasesWithReport, c.Phases)
	}
	if c.Delegations != 2 {
		t.Errorf("Delegations = %d, want 2 (scoped to reported tasks)", c.Delegations)
	}
	if c.Redispatches != 1 {
		t.Errorf("Redispatches = %d, want 1", c.Redispatches)
	}
	if got := c.RedispatchRate(); got != 0.5 {
		t.Errorf("RedispatchRate = %v, want 0.5", got)
	}
	if got := c.ReportRate(); got != 0.5 {
		t.Errorf("ReportRate = %v, want 0.5", got)
	}
	if c.Summary() == "" {
		t.Error("Summary must render something a digest can print")
	}
}

// Empty windows must not divide by zero or read as 100%.
func TestClosureStatsEmptyWindow(t *testing.T) {
	db := testDB(t)
	c, err := Closure(db, "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if c.Tasks != 0 || c.RedispatchRate() != 0 || c.ReportRate() != 0 {
		t.Errorf("empty window = %+v, want zeros", c)
	}
}
