package phaserun

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/runcore"
)

// TestStart_RefusesDuringALivePlanRun is the mirror of planrun's
// "a phase run is in flight" case. Before this gate existed the exclusion only
// closed in one direction: planrun asked whether a phase was running, phaserun
// never asked whether the PLAN was — so starting a phase during a live plan run
// put two orchestrators on the same docs in two worktrees, and whichever finished
// second overwrote the other's edits.
func TestStart_RefusesDuringALivePlanRun(t *testing.T) {
	db, taskID, p1, _ := fixture(t)
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state) VALUES (?, 'running')`, taskID)

	s := newTestService(db, &stubRunner{}, &stubWt{})
	_, err := s.Start(p1)
	if !errors.Is(err, ErrPlanRunning) {
		t.Fatalf("err = %v, want ErrPlanRunning", err)
	}
	// A refusal leaves NO state: not the row, not a slot.
	if got := phaseRunState(t, db, p1); got != "idle" {
		t.Errorf("run_state = %q, want the untouched 'idle'", got)
	}
	if s.Slots.IsActive(s.slotKey(p1)) {
		t.Error("a refused Start left a slot held")
	}
}

// A plan run that has ENDED must not block anything — the gate reads the live
// state, not the history.
func TestStart_AllowedAfterThePlanRunEnded(t *testing.T) {
	db, taskID, p1, _ := fixture(t)
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state) VALUES (?, 'done')`, taskID)

	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// A plan run of a DIFFERENT plan is none of this phase's business.
func TestStart_UnaffectedByAnotherPlansRun(t *testing.T) {
	db, _, p1, _ := fixture(t)
	// A second workspace plan, running. plan_runs.workspace_task_id is a real FK,
	// so the other plan has to exist as a row rather than as a made-up id.
	res, err := db.Exec(`INSERT INTO tasks (project_id, title, prompt, status, created_at,
		source, external_id) VALUES (1,'Other Epic','goal','running','2026-07-27T00:00:00Z',
		'workspace','2026-07-27-other-epic')`)
	if err != nil {
		t.Fatal(err)
	}
	otherTask, _ := res.LastInsertId()
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state) VALUES (?, 'running')`, otherTask)

	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// The bound this engine never had. A phase run used to be limited by nothing at
// all — the pool is now shared with the board and with plan runs, and a full pool
// refuses with ErrNoSlot (retriable, naming its holders), never by stamping the
// phase failed.
func TestStart_RefusedWhenTheRunBudgetIsFull(t *testing.T) {
	db, _, p1, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	s.Slots = runcore.NewSlots(1)
	if _, err := s.Slots.TryAcquire(runcore.SlotKey("dispatch", 5), "u-board", nil); err != nil {
		t.Fatal(err)
	}

	_, err := s.Start(p1)
	if !errors.Is(err, runcore.ErrNoSlot) {
		t.Fatalf("err = %v, want runcore.ErrNoSlot", err)
	}
	var noSlot *runcore.NoSlotError
	if !errors.As(err, &noSlot) || len(noSlot.Holders) != 1 || noSlot.Holders[0].Key != "dispatch:5" {
		t.Errorf("refusal does not name the board run holding the pool: %v", err)
	}
	// Nothing was stamped: a busy pool is not a failed run.
	if got := phaseRunState(t, db, p1); got != "idle" {
		t.Errorf("run_state = %q, want the untouched 'idle' — a full pool must not fail the phase", got)
	}

	// Free the slot and the very same Start succeeds.
	s.Slots.Release(runcore.SlotKey("dispatch", 5))
	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start after the slot freed: %v", err)
	}
}

// The duplicate refusal keeps its own sentinel, which the API renders as
// "already-running" — a different answer from a full pool, and the reason
// Slots keeps ErrBusy and ErrNoSlot apart.
func TestStart_DuplicateStillReportsErrRunning(t *testing.T) {
	db, _, p1, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Slots.TryAcquire(s.slotKey(p1), "u-live", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(p1); !errors.Is(err, ErrRunning) {
		t.Fatalf("err = %v, want ErrRunning", err)
	}
}

// phaseRunState reads a phase's run_state ("" when the column is NULL — never
// run). Local to this file: the sibling suites read the column inline.
func phaseRunState(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var state sql.NullString
	if err := db.QueryRow(`SELECT run_state FROM epic_phases WHERE id=?`, id).Scan(&state); err != nil {
		t.Fatalf("read run_state of %d: %v", id, err)
	}
	return state.String
}
