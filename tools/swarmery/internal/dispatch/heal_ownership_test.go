package dispatch

import "testing"

// HealStale's reach is bounded by DISPATCHER OWNERSHIP, not by source='queue'.
//
// internal/taskcap mints captured session cards with source='queue' too, so once
// a user ACCEPTS a suggestion (triage → in_progress) the old predicate matched a
// row with no run and no worktree behind it: HealStale dropped it to 'todo' with
// dispatch_error='daemon restart', and candidates() (source='queue' AND
// board_column='todo' AND paused=0 AND user_paused=0) then auto-dispatched it.
// Every daemon restart silently launched agent runs the user never asked for.
//
// worktree_path is the ownership marker: admit() writes it in the same UPDATE as
// board_column='in_progress', and liveWorktreeCount defines a live dispatcher
// slot as exactly `worktree_path IS NOT NULL AND board_column='in_progress'`.

// TestHealStale_LeavesAcceptedCapturedCard: the bug. A user-accepted captured
// card holds no worktree, so HealStale must not see it at all — neither the
// column nor dispatch_error may move.
func TestHealStale_LeavesAcceptedCapturedCard(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	id := insertTask(t, db, "T-accepted", taskOpts{
		column: "in_progress", origin: "session", // worktreePath deliberately unset ⇒ NULL
	})

	if err := s.HealStale(); err != nil {
		t.Fatal(err)
	}
	if got := column(t, db, id); got != "in_progress" {
		t.Errorf("accepted captured card column = %q, want in_progress "+
			"(healing it hands the card to candidates() and auto-dispatches it)", got)
	}
	if e := taskField(t, db, id, "dispatch_error"); e.Valid {
		t.Errorf("dispatch_error = %q, want NULL (no dispatcher run ever touched this card)", e.String)
	}
	if st := taskField(t, db, id, "status"); st.String != "queued" {
		t.Errorf("status = %q, want the untouched 'queued'", st.String)
	}
}

// TestHealStale_StillHealsCrashedDispatcherRun: the positive control. Without it
// the guard could disable healing outright and test 1 would still pass.
func TestHealStale_StillHealsCrashedDispatcherRun(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	id := insertTask(t, db, "T-crashed", taskOpts{
		column: "in_progress", worktreePath: "/wt/T-crashed",
	})

	if err := s.HealStale(); err != nil {
		t.Fatal(err)
	}
	if got := column(t, db, id); got != "todo" {
		t.Errorf("crashed dispatcher run column = %q, want todo", got)
	}
	if e := taskField(t, db, id, "dispatch_error"); e.String != "daemon restart" {
		t.Errorf("dispatch_error = %q, want 'daemon restart'", e.String)
	}
}

// TestHealStale_HealsReworkedCapturedCardHoldingWorktree: proves origin is NOT
// the discriminator. origin is immutable (api/tasks_board.go rejects patching
// origin/capture_key/origin_session_id), so a captured card the user reworked
// (in_review → todo) and the dispatcher re-admitted still reads origin='session'
// while being fully dispatcher-owned. An origin-based guard would strand this row
// in_progress forever after a crash — one bug traded for another.
func TestHealStale_HealsReworkedCapturedCardHoldingWorktree(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	id := insertTask(t, db, "T-reworked", taskOpts{
		column: "in_progress", origin: "session", worktreePath: "/wt/T-reworked",
	})

	if err := s.HealStale(); err != nil {
		t.Fatal(err)
	}
	if got := column(t, db, id); got != "todo" {
		t.Errorf("reworked-then-dispatched card column = %q, want todo "+
			"(origin='session' must not exempt a row the dispatcher owns)", got)
	}
	if e := taskField(t, db, id, "dispatch_error"); e.String != "daemon restart" {
		t.Errorf("dispatch_error = %q, want 'daemon restart'", e.String)
	}
}
