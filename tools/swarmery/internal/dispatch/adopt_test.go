package dispatch

import (
	"database/sql"
	"testing"
	"time"
)

// TestHealStale_AdoptsSurvivingRun: an executor in its own process group outlives
// a daemon restart. Requeuing its task would put a SECOND executor into the
// worktree the first is still writing in, so the task is left in_progress and its
// concurrency slot held until the process is observed to exit.
func TestHealStale_AdoptsSurvivingRun(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	live := insertTask(t, db, "T-live", taskOpts{column: "in_progress"})
	dead := insertTask(t, db, "T-dead", taskOpts{column: "in_progress"})
	mustSetUUID(t, db, live, "live-uuid")
	mustSetUUID(t, db, dead, "dead-uuid")

	var watcher func()
	s.Go = func(fn func()) { watcher = fn } // hold the watcher: the run stays in flight
	s.FindRun = func(uuid string) (int, bool) { return 4242, uuid == "live-uuid" }
	alive := true
	s.ProcAlive = func(pid int) bool { return alive && pid == 4242 }

	if err := s.HealStale(); err != nil {
		t.Fatal(err)
	}
	if got := column(t, db, live); got != "in_progress" {
		t.Errorf("adopted task column = %q, want in_progress", got)
	}
	if got := column(t, db, dead); got != "todo" {
		t.Errorf("task with no live process = %q, want todo", got)
	}
	if !s.isActive(live) {
		t.Error("adopted task does not hold its concurrency slot")
	}

	// The orphan exits: the slot is released so the scheduler can use it again,
	// and the task is left for the evidence-based HealDeadProcess to reclaim.
	alive = false
	if watcher == nil {
		t.Fatal("adoption spawned no watcher")
	}
	watcher()
	if s.isActive(live) {
		t.Error("slot still held after the adopted run ended")
	}
	if got := column(t, db, live); got != "in_progress" {
		t.Errorf("column after the adopted run ended = %q, want in_progress (HealDeadProcess owns the reclaim)", got)
	}
}

// TestHealStale_NoUUIDIsHealed: without a session uuid there is nothing to match a
// process against, so the task must fall through to the requeue.
func TestHealStale_NoUUIDIsHealed(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})
	id := insertTask(t, db, "T-nouuid", taskOpts{column: "in_progress"})
	s.FindRun = func(string) (int, bool) { t.Error("must not probe without a uuid"); return 0, false }
	s.adoptPoll = time.Millisecond

	if err := s.HealStale(); err != nil {
		t.Fatal(err)
	}
	if got := column(t, db, id); got != "todo" {
		t.Errorf("column = %q, want todo", got)
	}
}

func mustSetUUID(t *testing.T, db *sql.DB, id int64, uuid string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE tasks SET dispatch_session_uuid=? WHERE id=?`, uuid, id); err != nil {
		t.Fatal(err)
	}
}
