package api

import (
	"testing"
)

// The closure measurables have to be reachable from outside Go, or "queryable
// for a window" is only true for someone writing a test. They ride on the churn
// endpoint because that is the table the retro's churn section is built from —
// the one that could speak about exactly two tasks.
func TestRetroTasks_CarriesClosureStats(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`UPDATE epic_phases SET completion_report =
		'Shipped it: internal/api/epics.go and internal/phasegate/phasegate.go; make test green.'
		WHERE seq = 1 AND workspace_task_id = ?`, taskID)
	mustExec(`INSERT INTO task_delegations (task_id, seq, agent, verdict)
		VALUES (?, 1, 'implementation-agent', 'approved'),
		       (?, 2, 'quality-reviewer', 'rejected')`, taskID, taskID)

	var out retroTasksDTO
	getJSON(t, srv.URL+"/api/retro/tasks?from=2026-07-01&to=2026-12-31", &out)

	if out.Closure.TasksWithReport != 1 {
		t.Errorf("tasksWithReport = %d, want 1", out.Closure.TasksWithReport)
	}
	if out.Closure.PhasesWithReport != 1 || out.Closure.Phases != 2 {
		t.Errorf("phases = %d/%d, want 1/2", out.Closure.PhasesWithReport, out.Closure.Phases)
	}
	if out.Closure.Delegations != 2 || out.Closure.Redispatches != 1 {
		t.Errorf("delegations = %d, redispatches = %d, want 2 and 1",
			out.Closure.Delegations, out.Closure.Redispatches)
	}
	if out.Closure.RedispatchRate != 0.5 {
		t.Errorf("redispatchRate = %v, want 0.5", out.Closure.RedispatchRate)
	}
	if out.Closure.Summary == "" {
		t.Error("summary must render")
	}
}
