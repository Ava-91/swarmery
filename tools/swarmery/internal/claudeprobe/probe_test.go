package claudeprobe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeClaude writes a shell script named `claude` into a temp dir and points
// SWARMERY_CLAUDE_BIN at it — the claudebin override, so resolution cannot
// wander off to a real install in /opt/homebrew/bin. Same pattern as
// internal/verify's fakeClaudeRunner. No test in this file may invoke the
// real CLI.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-claude shim is POSIX-only")
	}
	script := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("SWARMERY_CLAUDE_BIN", script)
	return script
}

// TestProbeClassification is the classifier table: exit status first, wording
// second, and an unrecognised failure NEVER classifies as ready.
func TestProbeClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		want   Result
	}{
		{"zero exit is ready", `printf '{"loggedIn": true}\n'; exit 0`,
			Result{Status: StatusReady}},
		{"auth-status no-login shape", `printf '{"loggedIn": false, "authMethod": "none"}\n'; exit 1`,
			Result{Status: StatusNoLogin, Reason: ReasonNoLogin}},
		{"plain-run no-login line", `printf 'Not logged in · Please run /login\n'; exit 1`,
			Result{Status: StatusNoLogin, Reason: ReasonNoLogin}},
		{"unrecognised non-zero is unknown", `printf 'segmentation fault\n'; exit 3`,
			Result{Status: StatusUnknown, Reason: ReasonUnrecognised}},
		// A liar that prints the ready-looking JSON but fails must still be
		// classified by its exit status.
		{"non-zero with happy output is not ready", `printf '{"loggedIn": true}\n'; exit 1`,
			Result{Status: StatusUnknown, Reason: ReasonUnrecognised}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeClaude(t, tc.script)
			got := Probe(context.Background(), "")
			if got != tc.want {
				t.Errorf("Probe = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestProbeReasonsAreFixedPhrases: whatever the CLI prints, Reason is drawn
// from the package constants — output is never interpolated into it.
func TestProbeReasonsAreFixedPhrases(t *testing.T) {
	const leak = "sk-ant-oat01-FAKE-LEAKED-TOKEN"
	fakeClaude(t, `printf '%s\n' '`+leak+`'; exit 7`)
	got := Probe(context.Background(), "")
	fixed := map[string]bool{
		"": true, ReasonNoLogin: true, ReasonNoBinary: true,
		ReasonTimeout: true, ReasonUnrecognised: true, ReasonStartFailed: true,
	}
	if !fixed[got.Reason] {
		t.Errorf("Reason = %q, not one of the fixed phrases", got.Reason)
	}
	if strings.Contains(got.Reason, leak) {
		t.Errorf("Reason carries CLI output: %q", got.Reason)
	}
}

// TestProbeMissingBinary: no CLI on the machine is unknown, not a crash.
// resolveBin is swapped rather than emptying PATH because claudebin probes
// fixed system dirs a test cannot control.
func TestProbeMissingBinary(t *testing.T) {
	prev := resolveBin
	resolveBin = func() (string, error) { return "", errors.New("claude not found") }
	t.Cleanup(func() { resolveBin = prev })

	got := Probe(context.Background(), "")
	if want := (Result{Status: StatusUnknown, Reason: ReasonNoBinary}); got != want {
		t.Errorf("Probe = %+v, want %+v", got, want)
	}
}

// TestProbeDefaultAccountEnvHasNoConfigDir: probing the default account means
// the variable is ABSENT from the child env — not empty — even when the probing
// process itself inherited one.
func TestProbeDefaultAccountEnvHasNoConfigDir(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "seen")
	fakeClaude(t, `printf '%s' "${CLAUDE_CONFIG_DIR-__UNSET__}" > `+marker+`; exit 0`)
	// The hostile precondition: the daemon itself runs under some account.
	t.Setenv("CLAUDE_CONFIG_DIR", "/somewhere/leaked")

	if got := Probe(context.Background(), ""); got.Status != StatusReady {
		t.Fatalf("Probe = %+v, want ready", got)
	}
	seen, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(seen) != "__UNSET__" {
		t.Errorf("child saw CLAUDE_CONFIG_DIR=%q, want it absent", seen)
	}
}

// TestProbeNamedAccountEnvHasExactlyItsDir: a named account's probe carries
// exactly that dir, overriding anything inherited.
func TestProbeNamedAccountEnvHasExactlyItsDir(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "seen")
	fakeClaude(t, `printf '%s' "${CLAUDE_CONFIG_DIR-__UNSET__}" > `+marker+`; exit 0`)
	t.Setenv("CLAUDE_CONFIG_DIR", "/somewhere/leaked")

	dir := filepath.Join(t.TempDir(), ".claude-nabu-org")
	if got := Probe(context.Background(), dir); got.Status != StatusReady {
		t.Fatalf("Probe = %+v, want ready", got)
	}
	seen, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(seen) != dir {
		t.Errorf("child saw CLAUDE_CONFIG_DIR=%q, want %q", seen, dir)
	}
}

// TestProbeTimeoutKillsProcessGroup: a hung CLI is classified unknown/timeout,
// and the whole process group — including a background descendant — is gone by
// the time Probe returns, not just the leader.
func TestProbeTimeoutKillsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	// The leader records its own pid (== the group id under Setpgid), spawns a
	// descendant, and hangs well past the deadline.
	fakeClaude(t, `echo $$ > `+pidFile+`
sleep 60 &
sleep 60`)

	// A hard 10s deadline backstops the test; the pid-file poll below cancels
	// far earlier, as soon as the fake CLI is provably running. (Script
	// startup latency on macOS makes a fixed short deadline racy.)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- Probe(ctx, "") }()

	var raw []byte
	deadline := time.Now().Add(8 * time.Second)
	for {
		var err error
		if raw, err = os.ReadFile(pidFile); err == nil && len(raw) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake claude never wrote its pid: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel() // the CLI is mid-hang — cut it off

	got := <-done
	if want := (Result{Status: StatusUnknown, Reason: ReasonTimeout}); got != want {
		t.Fatalf("Probe = %+v, want %+v", got, want)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", raw, err)
	}
	// Signal 0 to the GROUP: ESRCH means every member (leader and the
	// backgrounded sleep) is dead. Probe drains before returning, so no wait
	// loop is needed here.
	if err := syscall.Kill(-pid, 0); err != syscall.ESRCH {
		t.Errorf("process group %d still alive after timeout (kill err = %v)", pid, err)
	}
}
