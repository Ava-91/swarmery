package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeClaude writes a shell script named `claude` into a temp dir and prepends
// it to PATH so ClaudeRunner.Start spawns IT instead of the real binary. The
// script body decides the behavior (exit code / sleep). This exercises the real
// process-spawn + exit-routing branches without invoking a real claude session.
func fakeClaude(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-claude PATH shim is POSIX-only")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestClaudeRunnerExitZero(t *testing.T) {
	fakeClaude(t, `exit 0`)
	run, err := ClaudeRunner{}.Start(context.Background(),
		RunSpec{Prompt: "p", SessionUUID: "u1", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Start err: %v", err)
	}
	if run.ExitCode != 0 || run.TimedOut {
		t.Errorf("clean exit: code=%d timedOut=%v", run.ExitCode, run.TimedOut)
	}
	if run.SessionUUID != "u1" {
		t.Errorf("uuid not echoed: %q", run.SessionUUID)
	}
}

func TestClaudeRunnerNonzeroExit(t *testing.T) {
	fakeClaude(t, `echo "explosion" 1>&2; exit 3`)
	run, err := ClaudeRunner{}.Start(context.Background(),
		RunSpec{Prompt: "p", SessionUUID: "u2", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("nonzero exit should be an outcome, not a Start error: %v", err)
	}
	if run.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", run.ExitCode)
	}
	if run.Stderr == "" {
		t.Error("stderr tail should be captured")
	}
}

func TestClaudeRunnerModelFlag(t *testing.T) {
	// Echo the args so we can assert --model is passed through. Exit 0.
	fakeClaude(t, `echo "$@" > "$PWD/args.txt"; exit 0`)
	cwd := t.TempDir()
	_, err := ClaudeRunner{}.Start(context.Background(),
		RunSpec{Prompt: "hello", SessionUUID: "u3", Cwd: cwd, Model: "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(cwd, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(out)
	for _, want := range []string{"-p", "hello", "--session-id", "u3", "--model", "sonnet", "--setting-sources", "project,local"} {
		if !contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
}

// spawnArgs runs ClaudeRunner against a fake claude that echoes its argv into a
// file, and returns that argv line. Shared by the agent-prefix tests.
func spawnArgs(t *testing.T, spec RunSpec) string {
	t.Helper()
	fakeClaude(t, `echo "$@" > "$PWD/args.txt"; exit 0`)
	cwd := t.TempDir()
	spec.Cwd = cwd
	if _, err := (ClaudeRunner{}).Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(cwd, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	return string(out)
}

// A task carrying an agent must reach `claude -p` as "@<agent>: <prompt>" —
// ONCE. The count assertion is the real contract: the prefix has exactly one
// application site (agentPrompt), so a service that also prefixed would show up
// here as two.
func TestClaudeRunnerAgentPrefixesPromptOnce(t *testing.T) {
	got := spawnArgs(t, RunSpec{Prompt: "hello", SessionUUID: "u6", Agent: "tech-lead"})
	if !contains(got, "@tech-lead: hello") {
		t.Errorf("args %q missing the agent-prefixed prompt", got)
	}
	if n := strings.Count(got, "@tech-lead: "); n != 1 {
		t.Errorf("prefix applied %d times, want exactly 1 (args %q)", n, got)
	}
	// Model/session flags are untouched by the prefix.
	for _, want := range []string{"--session-id", "u6", "--setting-sources", "project,local"} {
		if !contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
}

// Regression: a task with no agent dispatches byte-identically to pre-feature
// behavior — the prompt is passed through with no mention of any kind.
func TestClaudeRunnerNoAgentLeavesPromptUnchanged(t *testing.T) {
	got := spawnArgs(t, RunSpec{Prompt: "hello", SessionUUID: "u7"})
	if !contains(got, "-p hello ") {
		t.Errorf("args %q should carry the bare prompt", got)
	}
	if contains(got, "@") {
		t.Errorf("args %q must contain no agent mention when Agent is unset", got)
	}
}

// agentPrompt is the single prefix site; pin its whole closed set of behaviors
// here so the argv tests above only have to prove the wiring.
func TestAgentPromptSingleSite(t *testing.T) {
	for _, tc := range []struct{ name, agent, prompt, want string }{
		{"unset", "", "do the thing", "do the thing"},
		{"whitespace only is no agent", "   ", "do the thing", "do the thing"},
		{"set", "tech-lead", "do the thing", "@tech-lead: do the thing"},
		{"trimmed", "  tech-lead  ", "do the thing", "@tech-lead: do the thing"},
		{"prompt already mentioning someone is not re-owned",
			"tech-lead", "ask @qa about it", "@tech-lead: ask @qa about it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentPrompt(RunSpec{Agent: tc.agent, Prompt: tc.prompt}); got != tc.want {
				t.Errorf("agentPrompt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaudeRunnerTimeout(t *testing.T) {
	fakeClaude(t, `sleep 5`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	run, err := ClaudeRunner{}.Start(ctx,
		RunSpec{Prompt: "p", SessionUUID: "u4", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("timeout should be an outcome, not a Start error: %v", err)
	}
	if !run.TimedOut {
		t.Errorf("expected TimedOut, got code=%d", run.ExitCode)
	}
}

func TestClaudeRunnerStartError(t *testing.T) {
	// Point PATH at an empty dir so `claude` cannot be resolved → Start error.
	t.Setenv("PATH", t.TempDir())
	run, err := ClaudeRunner{}.Start(context.Background(),
		RunSpec{Prompt: "p", SessionUUID: "u5", Cwd: t.TempDir()})
	if err == nil {
		t.Fatal("expected a Start error when claude is absent from PATH")
	}
	if run.ExitCode != -1 {
		t.Errorf("start-failure exit code = %d, want -1", run.ExitCode)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
