package planrun

import (
	"errors"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/runcore"
)

// A plan run is the most expensive thing the daemon starts — an orchestrator that
// spawns its own sub-agents for hours — and it used to draw against no budget at
// all. A full pool refuses with ErrNoSlot (retriable, naming its holders) and
// stamps nothing: no plan_runs row is written, so a busy machine can never be read
// later as a failed plan.
func TestStart_RefusedWhenTheRunBudgetIsFull(t *testing.T) {
	db, taskID, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	s.Slots = runcore.NewSlots(1)
	if _, err := s.Slots.TryAcquire(runcore.SlotKey("phaserun", 3), "u-phase", nil); err != nil {
		t.Fatal(err)
	}

	_, err := s.Start(taskID, "", "")
	if !errors.Is(err, runcore.ErrNoSlot) {
		t.Fatalf("err = %v, want runcore.ErrNoSlot", err)
	}
	var noSlot *runcore.NoSlotError
	if !errors.As(err, &noSlot) || len(noSlot.Holders) != 1 || noSlot.Holders[0].Key != "phaserun:3" {
		t.Errorf("refusal does not name the phase run holding the pool: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_runs WHERE workspace_task_id=?`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("plan_runs rows = %d, want 0 — a full pool must not stamp a run", n)
	}

	// Free the slot and the very same Start succeeds.
	s.Slots.Release(runcore.SlotKey("phaserun", 3))
	if _, err := s.Start(taskID, "", ""); err != nil {
		t.Fatalf("Start after the slot freed: %v", err)
	}
}

// The duplicate refusal keeps its own sentinel, which the API renders as
// "already-running" — a different answer from a full pool.
func TestStart_DuplicateStillReportsErrRunning(t *testing.T) {
	db, taskID, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Slots.TryAcquire(s.slotKey(taskID), "u-live", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(taskID, "", ""); !errors.Is(err, ErrRunning) {
		t.Fatalf("err = %v, want ErrRunning", err)
	}
}

// The plan↔phase exclusion is now enforced from BOTH sides. This is the side that
// always existed; phaserun's mirror is in internal/phaserun/slots_test.go, and the
// pair is what makes the guarantee independent of which button the operator
// presses first.
func TestStart_StillRefusesDuringALivePhaseRun(t *testing.T) {
	db, taskID, _ := fixture(t)
	mustExec(t, db, `UPDATE epic_phases SET run_state='running' WHERE workspace_task_id=? AND seq=1`, taskID)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(taskID, "", ""); !errors.Is(err, ErrPhaseRunning) {
		t.Fatalf("err = %v, want ErrPhaseRunning", err)
	}
}
