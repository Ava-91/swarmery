package planrun

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
)

// TestHealStale_AdoptsSurvivingRun: a plan run in its own process group outlives
// a daemon restart, so the restarted daemon must recognise it instead of
// condemning a live orchestrator to 'failed / daemon restart'.
func TestHealStale_AdoptsSurvivingRun(t *testing.T) {
	db, taskID, _ := fixture(t)
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state, run_session_uuid)
		VALUES (?, 'running', 'live-uuid')`, taskID)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	var watcher func()
	s.Go = func(fn func()) { watcher = fn } // hold the watcher: the run stays in flight
	s.FindRun = func(uuid string) (int, bool) { return 4242, uuid == "live-uuid" }
	alive := true
	s.ProcAlive = func(pid int) bool { return alive && pid == 4242 }

	if err := s.HealStale(); err != nil {
		t.Fatalf("HealStale: %v", err)
	}
	if state, _, _, _, _ := planRow(t, db, taskID); state != "running" {
		t.Errorf("adopted plan run state = %q, want running", state)
	}
	// Slot held: a Retry must not put a second orchestrator in the same worktree.
	if _, err := s.Start(taskID, "", ""); !errors.Is(err, ErrRunning) {
		t.Errorf("Start on an adopted plan = %v, want ErrRunning", err)
	}

	alive = false
	if watcher == nil {
		t.Fatal("adoption spawned no watcher")
	}
	watcher()
	state, _, _, _, runErr := planRow(t, db, taskID)
	if state != "done" {
		t.Errorf("adopted run after exit: state = %q, want done", state)
	}
	if !strings.Contains(runErr.String, "exit status unknown") {
		t.Errorf("run_error = %q, want the unknown-exit note", runErr.String)
	}
}

// TestHealStale_NoUUIDIsHealed: without a session uuid there is nothing to match
// a process against, so the row must fall through to the fail sweep.
func TestHealStale_NoUUIDIsHealed(t *testing.T) {
	db, taskID, _ := fixture(t)
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state, run_session_uuid)
		VALUES (?, 'running', NULL)`, taskID)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	s.FindRun = func(string) (int, bool) { t.Error("must not probe without a uuid"); return 0, false }

	if err := s.HealStale(); err != nil {
		t.Fatalf("HealStale: %v", err)
	}
	if state, _, _, _, runErr := planRow(t, db, taskID); state != "failed" || runErr.String != "daemon restart" {
		t.Errorf("state=%q run_error=%q, want failed/daemon restart", state, runErr.String)
	}
}

// TestAdopt_CancelKillsTheOrphan: Stop on an adopted run has no child to cancel,
// so it must reach the orphan through its process group. Driven with a real
// process — signalling an invented pid is the bug this guards against.
func TestAdopt_CancelKillsTheOrphan(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "sleep 30")
	procgroup.Isolate(cmd, 0)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start victim: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap, so the liveness probe sees no zombie
	t.Cleanup(func() { _ = procgroup.Kill(pid) })

	db, taskID, _ := fixture(t)
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state, run_session_uuid)
		VALUES (?, 'running', 'live-uuid')`, taskID)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	s.Go = func(fn func()) { go fn() }
	s.adoptPoll = 5 * time.Millisecond
	s.FindRun = func(string) (int, bool) { return pid, true }

	if err := s.HealStale(); err != nil {
		t.Fatalf("HealStale: %v", err)
	}
	if !s.Cancel(taskID) {
		t.Fatal("Cancel on an adopted run = false, want true")
	}
	waitFor(t, func() bool {
		state, _, _, _, runErr := planRow(t, db, taskID)
		return state == "failed" && runErr.String == "cancelled"
	})
}
