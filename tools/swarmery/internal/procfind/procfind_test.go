package procfind

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestBySessionUUID_FindsAndMisses drives the real ps scan against a real
// process. The victim is named `claude` in argv (the scan requires it) and
// carries a uuid, exactly like a headless run.
func TestBySessionUUID_FindsAndMisses(t *testing.T) {
	const uuid = "3f7c1c1e-procfind-test-0001"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The scan matches on argv text, so trailing words are enough to imitate a run.
	// `; true` defeats sh's exec optimisation — without it sh replaces itself with
	// `sleep` and the argv under test disappears.
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30; true", "claude", "--session-id", uuid)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start victim: %v", err)
	}
	go func() { _ = cmd.Wait() }()

	var pid int
	var ok bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pid, ok = BySessionUUID(uuid); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok {
		t.Fatal("BySessionUUID did not find a live process carrying the uuid")
	}
	if pid != cmd.Process.Pid {
		t.Errorf("pid = %d, want the victim %d", pid, cmd.Process.Pid)
	}

	cancel()
	// Once it is gone the same query must report absence, not a stale hit.
	found := true
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, found = BySessionUUID(uuid); !found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if found {
		t.Error("BySessionUUID still reports the killed process as live")
	}
}

func TestBySessionUUID_EmptyUUID(t *testing.T) {
	if _, ok := BySessionUUID("   "); ok {
		t.Error("an empty uuid must never match a process")
	}
}
