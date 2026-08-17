package dispatch

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/runcore"
)

// The board draws from the DAEMON-WIDE pool, not from a private one. Proven with
// a slot held under another engine's key: nothing in dispatch's own state says a
// run is in flight, yet admission must wait — which is the whole point of one
// budget, and what dispatch's MaxWorktrees cap could not see before (it counted
// `tasks` rows only, so a plan run holding the machine was invisible to it).
func TestSchedule_ForeignEngineSlotBlocksAdmission(t *testing.T) {
	db := testDB(t)
	r := &stubRunner{}
	s := newTestService(t, db, r, &stubWt{})
	s.Slots = runcore.NewSlots(1)
	id := insertTask(t, db, "T-blocked", taskOpts{})

	// A plan run is holding the only slot.
	if _, err := s.Slots.TryAcquire(runcore.SlotKey("planrun", 99), "u-plan", nil); err != nil {
		t.Fatal(err)
	}

	s.Schedule()

	if r.count() != 0 {
		t.Errorf("runner spawned %d times, want 0 — the pool was full", r.count())
	}
	if got := column(t, db, id); got != "todo" {
		t.Errorf("column = %q, want todo — a full pool defers, it does not fail the row", got)
	}
	if e := taskField(t, db, id, "dispatch_error"); e.Valid && e.String != "" {
		t.Errorf("dispatch_error = %q, want empty — a busy pool is retriable, never an error on the card", e.String)
	}

	// Free the slot: the same candidate is admitted on the next pass, untouched.
	s.Slots.Release(runcore.SlotKey("planrun", 99))
	s.Schedule()
	waitFor(t, func() bool { return column(t, db, id) != "todo" })
	if r.count() != 1 {
		t.Errorf("runner spawned %d times after the slot freed, want 1", r.count())
	}
}

// MaxConcurrent keeps meaning "BOARD runs", so a foreign slot must not consume
// the board's own cap — only the shared budget.
func TestActiveCount_CountsOnlyBoardRuns(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	s.Slots = runcore.NewSlots(8)
	if _, err := s.Slots.TryAcquire(runcore.SlotKey("phaserun", 1), "u", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Slots.TryAcquire(runcore.SlotKey("planrun", 2), "u", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.activeCount(); got != 0 {
		t.Errorf("activeCount = %d, want 0 — MaxConcurrent bounds board runs only", got)
	}
	if err := s.markActive(7); err != nil {
		t.Fatal(err)
	}
	if got := s.activeCount(); got != 1 {
		t.Errorf("activeCount = %d, want 1", got)
	}
	if got := s.Slots.Count(); got != 3 {
		t.Errorf("Slots.Count = %d, want 3 — the budget counts every engine", got)
	}
}

// markActive is now a claim that can fail, and the two refusals must stay
// distinguishable: a duplicate is "already running", a full pool is "try later".
func TestMarkActive_RefusalsAreDistinguishable(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	s.Slots = runcore.NewSlots(1)

	if err := s.markActive(1); err != nil {
		t.Fatal(err)
	}
	if err := s.markActive(1); err == nil || !isBusy(err) {
		t.Errorf("re-claiming the same task gave %v, want ErrBusy", err)
	}
	if err := s.markActive(2); err == nil || isBusy(err) {
		t.Errorf("claiming past the budget gave %v, want ErrNoSlot", err)
	}
}

func isBusy(err error) bool { return errors.Is(err, runcore.ErrBusy) }

// Adoption must survive a full pool: the orphan is already running, so refusing
// to track it would free the slot and let the scheduler dispatch a rival into the
// same worktree.
func TestAdopt_HoldsSlotEvenWhenPoolIsFull(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	s.Slots = runcore.NewSlots(1)
	s.adoptPoll = time.Millisecond
	var alive atomic.Bool // the watcher polls from its own goroutine
	alive.Store(true)
	s.ProcAlive = func(int) bool { return alive.Load() }
	s.Go = func(fn func()) { go fn() } // the watcher must not block the test

	if _, err := s.Slots.TryAcquire(runcore.SlotKey("planrun", 5), "u-plan", nil); err != nil {
		t.Fatal(err)
	}
	s.adopt(42, 4242, "u-adopted")

	if !s.isActive(42) {
		t.Fatal("adopted run is not holding a slot — a rival could now be dispatched into its worktree")
	}
	if got := s.Slots.Count(); got != 2 {
		t.Errorf("Slots.Count = %d, want 2 — adoption over-subscribes deliberately", got)
	}
	alive.Store(false)
	waitFor(t, func() bool { return !s.isActive(42) })
}
