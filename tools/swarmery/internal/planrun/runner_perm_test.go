package planrun

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeflags"
)

// fakeClaudeArgs points the runner at a script that dumps its argv (one per
// line) into argFile and exits 0.
func fakeClaudeArgs(t *testing.T, argFile string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fakeclaude.sh")
	body := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argFile + "; done\nexit " + strconv.Itoa(0) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWARMERY_CLAUDE_BIN", script)
}

// A plan run orchestrates real work — it must be able to write, run and commit.
// Without --permission-mode every such call is auto-denied in a headless run and
// the process still exits 0 (internal/claudeflags).
func TestClaudeRunner_Start_PassesPermissionMode(t *testing.T) {
	t.Setenv(claudeflags.ModeEnv, "")
	t.Setenv(permEnv, "")

	argFile := filepath.Join(t.TempDir(), "args")
	fakeClaudeArgs(t, argFile)
	r := ClaudeRunner{Timeout: 30 * time.Second}
	if _, err := r.Start(context.Background(), RunSpec{Prompt: "p", SessionUUID: "u-pm1", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	raw, _ := os.ReadFile(argFile)
	if got := strings.TrimSpace(string(raw)); !strings.Contains(got, "--permission-mode\n"+claudeflags.DefaultMode) {
		t.Errorf("args missing --permission-mode %s:\n%s", claudeflags.DefaultMode, got)
	}
}

func TestClaudeRunner_Start_PermissionModeOffOmitsFlag(t *testing.T) {
	t.Setenv(claudeflags.ModeEnv, "")
	t.Setenv(permEnv, "off")

	argFile := filepath.Join(t.TempDir(), "args")
	fakeClaudeArgs(t, argFile)
	r := ClaudeRunner{Timeout: 30 * time.Second}
	if _, err := r.Start(context.Background(), RunSpec{Prompt: "p", SessionUUID: "u-pm2", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	raw, _ := os.ReadFile(argFile)
	if got := strings.TrimSpace(string(raw)); strings.Contains(got, "--permission-mode") {
		t.Errorf("args carry --permission-mode although %s=off:\n%s", permEnv, got)
	}
}
