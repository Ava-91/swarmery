package procgroup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the budget runs out.
func waitFor(t *testing.T, budget time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// alive reports whether a single pid still exists.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// readPid reads a pid a test child wrote to file, once the file is non-empty.
func readPid(t *testing.T, file string) int {
	t.Helper()
	var raw []byte
	if !waitFor(t, 5*time.Second, func() bool {
		raw, _ = os.ReadFile(file)
		return len(strings.TrimSpace(string(raw))) > 0
	}) {
		t.Fatalf("child never wrote its pid to %s", file)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("pid file %q: %v", raw, err)
	}
	return pid
}

// TestIsolate_CancelKillsDescendants is the regression this package exists for:
// without a process group, cancellation SIGKILLs only the shell and the
// backgrounded grandchild survives (and holds the stderr pipe, blocking Wait).
func TestIsolate_CancelKillsDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & echo $! > "+pidFile+"; wait")
	// A bytes.Buffer makes exec create a pipe whose write end the grandchild
	// inherits — the exact shape that used to hang Wait past the deadline.
	cmd.Stderr = &bytes.Buffer{}
	Isolate(cmd, time.Second)

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() { done <- cmd.Wait() }()

	grandchild := readPid(t, pidFile)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after cancellation")
	}
	Drain(cmd.Process.Pid, 5*time.Second)

	if !waitFor(t, 2*time.Second, func() bool { return !alive(grandchild) }) {
		_ = syscall.Kill(grandchild, syscall.SIGKILL) // don't leak from a failing test
		t.Errorf("grandchild %d survived cancellation", grandchild)
	}
}

// TestIsolate_CleanExit pins that isolation does not disturb the ordinary path.
func TestIsolate_CleanExit(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exit 3")
	Isolate(cmd, 0)
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("Run err = %v, want *exec.ExitError", err)
	}
	if ee.ExitCode() != 3 {
		t.Errorf("ExitCode = %d, want 3", ee.ExitCode())
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid = false, want true")
	}
}

// TestDrain_EmptyGroup: a fully exited run leaks nothing, so Drain reports false
// and returns immediately rather than burning its grace window.
func TestDrain_EmptyGroup(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exit 0")
	Isolate(cmd, 0)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	start := time.Now()
	if Drain(cmd.Process.Pid, 5*time.Second) {
		t.Error("Drain = true, want false for an exited run")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Drain took %s on an empty group, want immediate", d)
	}
}

func TestDrain_NoProcess(t *testing.T) {
	if Drain(0, time.Second) {
		t.Error("Drain(0) = true, want false")
	}
}
