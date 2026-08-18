package verify

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
)

// TestHealStale_KillsSurvivingVerifier: process-group isolation lets a verifier
// outlive the daemon, but its verdict lives in a stdout pipe that died with the
// parent. A survivor is therefore killed rather than adopted — otherwise it burns
// tokens and holds a worktree to produce an answer nobody can read, while the
// task is already stamped inconclusive.
func TestHealStale_KillsSurvivingVerifier(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "sleep 30")
	procgroup.Isolate(cmd, 0)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start victim: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap so the liveness check sees no zombie
	t.Cleanup(func() { _ = procgroup.Kill(pid) })

	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, stubTrees{hash: "h"})
	id := insertTask(t, db, taskOpts{})
	if _, err := db.Exec(`INSERT INTO verification_runs(target_key, task_id, status, started_at, verify_session_uuid)
		VALUES('task:'||?, ?, 'running', ?, 'live-uuid')`, id, id, s.ts()); err != nil {
		t.Fatal(err)
	}
	var probed string
	s.FindRun = func(uuid string) (int, bool) { probed = uuid; return pid, true }

	if err := s.HealStale(); err != nil {
		t.Fatal(err)
	}
	if probed != "live-uuid" {
		t.Errorf("probed uuid = %q, want the run's live-uuid", probed)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // gone, as required
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("orphaned verifier %d survived HealStale", pid)
}

// TestHealStale_NoSurvivorIsSilent: the normal case — the verifier really did die
// with the daemon — must not try to signal anything.
func TestHealStale_NoSurvivorIsSilent(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, stubTrees{hash: "h"})
	id := insertTask(t, db, taskOpts{})
	if _, err := db.Exec(`INSERT INTO verification_runs(target_key, task_id, status, started_at, verify_session_uuid)
		VALUES('task:'||?, ?, 'running', ?, 'dead-uuid')`, id, id, s.ts()); err != nil {
		t.Fatal(err)
	}
	s.FindRun = func(string) (int, bool) { return 0, false }

	if err := s.HealStale(); err != nil {
		t.Fatal(err)
	}
	if got := verdictOf(t, db, id); got != "inconclusive" {
		t.Fatalf("healed task verdict = %q, want inconclusive", got)
	}
}
