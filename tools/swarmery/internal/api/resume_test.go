package api_test

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeClaude installs a stub `claude` that records the CLAUDE_CONFIG_DIR it was
// spawned with, so a test can assert the resume's account env WITHOUT a real CLI.
func fakeClaude(t *testing.T) (dumpPath string) {
	t.Helper()
	dir := t.TempDir()
	dumpPath = filepath.Join(dir, "config-dir")
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s' \"$CLAUDE_CONFIG_DIR\" > " + dumpPath + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWARMERY_CLAUDE_BIN", bin)
	return dumpPath
}

// awaitDump waits for the stub to land its file and returns the contents.
func awaitDump(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stub claude never ran (no %s)", path)
	return ""
}

// insertResumableSession adds a session row the composer will accept: existing
// cwd, no live process, and the given account key.
func insertResumableSession(t *testing.T, db *sql.DB, uuid, account string) {
	t.Helper()
	cwd := t.TempDir()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, project_id, session_uuid, cwd, status, started_at, account)
		 VALUES (1, 1, ?, ?, 'idle', ?, ?)`,
		uuid, cwd, time.Now().UTC().Format(time.RFC3339), account); err != nil {
		t.Fatal(err)
	}
}

// THE regression: a session written under a non-default Claude account must be
// resumed under that same account's config dir. When the env delta is dropped,
// `claude -r` reads the DEFAULT config dir, finds no transcript, and exits with
// "No conversation found with session ID" — which, for the planning wizard, made
// every answer roll back to the same question forever.
func TestResumeRunsUnderTheSessionsAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "") // the test process may run under an account itself
	dump := fakeClaude(t)

	h := openMessageTestDB(t)
	insertResumableSession(t, h.DB, "uuid-acct", "nanitor")

	if w := postMessage(t, h, "1", `{"text":"hello"}`); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", w.Code, w.Body.String())
	}

	want := filepath.Join(home, ".claude-nanitor")
	if got := awaitDump(t, dump); got != want {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got, want)
	}
}

// The default account stays an EMPTY env delta — binding a project to `default`
// must not start pinning CLAUDE_CONFIG_DIR (claudeacct's package invariant).
func TestResumeUnderDefaultAccountPinsNoConfigDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "") // the test process may run under an account itself
	dump := fakeClaude(t)

	h := openMessageTestDB(t)
	insertResumableSession(t, h.DB, "uuid-default", "")

	if w := postMessage(t, h, "1", `{"text":"hello"}`); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", w.Code, w.Body.String())
	}

	if got := awaitDump(t, dump); got != "" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want empty", got)
	}
}
