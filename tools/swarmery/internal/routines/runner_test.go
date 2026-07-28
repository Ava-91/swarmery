package routines

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeClaudeRunner writes a shell script named `claude` into a temp dir and
// prepends it to PATH so ClaudeRunner.Run spawns IT instead of the real binary.
// Mirrors the dispatch/verify arg-assertion shims.
func fakeClaudeRunner(t *testing.T, body string) {
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

// TestClaudeRunnerArgs asserts the built claude arg list carries the headless
// slimming flag (--setting-sources project,local) alongside --output-format text
// and the model override. The prompt travels on stdin, not in argv.
func TestClaudeRunnerArgs(t *testing.T) {
	// Echo the args into the run cwd so we can assert on them. Exit 0.
	fakeClaudeRunner(t, `echo "$@" > "$PWD/args.txt"; exit 0`)
	cwd := t.TempDir()
	_, err := ClaudeRunner{}.Run(context.Background(), cwd, "the prompt", "sonnet")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(cwd, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(out)
	for _, want := range []string{"-p", "--output-format", "text", "--model", "sonnet", "--setting-sources", "project,local"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
}
