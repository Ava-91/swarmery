package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// The dry-run CLI must decide out loud and change nothing. This runs it against
// a REAL repository with a REAL extra worktree — the shape the janitor exists
// for — and asserts `git worktree list` is byte-identical before and after.
func TestCmdWorktrees_DryRunChangesNothing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	git(repo, "init", "-q", "-b", "main")
	git(repo, "config", "user.email", "t@example.com")
	git(repo, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(repo, "a.md"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "a.md")
	git(repo, "commit", "-qm", "init")
	wt := filepath.Join(t.TempDir(), "agent-cli")
	git(repo, "worktree", "add", "-q", "-b", "worktree-agent-cli", wt)
	// Untracked content that exists nowhere in git: a real run would salvage
	// then remove this. The dry-run must do neither.
	if err := os.WriteFile(filepath.Join(wt, "unique.md"), []byte("only here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "cli.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, name, first_seen) VALUES (1, ?, 'repo', 'Repo', '2026-08-01T00:00:00Z')`,
		repo); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	db.Close()

	before := git(repo, "worktree", "list")

	// MinIdle is 30m by default and the fixture is seconds old, so the sweep
	// would skip on the idle veto alone — set it to 0 so the CLI reaches a real
	// verdict and this test actually exercises the dry-run guard.
	t.Setenv("SWARMERY_WTJANITOR_MIN_IDLE_MIN", "0")

	if err := cmdWorktrees([]string{"clean", "--db", dbPath, "--dry-run", "--repo", repo}); err != nil {
		t.Fatalf("cmdWorktrees: %v", err)
	}

	if after := git(repo, "worktree", "list"); after != before {
		t.Errorf("dry-run changed the worktree list:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(wt, "unique.md")); err != nil {
		t.Errorf("dry-run removed the worktree's content: %v", err)
	}
	if out := git(repo, "branch", "--list", "salvage/*"); strings.TrimSpace(out) != "" {
		t.Errorf("dry-run created a salvage branch: %q", out)
	}
}

func TestCmdWorktrees_RejectsUnknownSubcommand(t *testing.T) {
	if err := cmdWorktrees(nil); err == nil {
		t.Error("cmdWorktrees(nil) = nil, want a usage error")
	}
	if err := cmdWorktrees([]string{"nuke"}); err == nil {
		t.Error(`cmdWorktrees("nuke") = nil, want a usage error`)
	}
}
