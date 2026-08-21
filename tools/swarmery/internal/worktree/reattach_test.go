package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReattachPathIntegration walks the exact operator story against a real git
// repo: a run acquires a worktree, commits there, ends — the janitor removes the
// directory and keeps the branch — and somebody then wants back into that run.
// The re-attached checkout must carry the run's own commit, not a fresh base.
func TestReattachPathIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test needs a real git binary; skipped in -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo, m, run := reattachFixture(t)
	acq, err := m.Acquire(repo, "proj", "plan-430")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// The run does its work and commits it on its own branch.
	mustWrite(t, filepath.Join(acq.Path, "feature.txt"), "the run's work\n")
	if out, gitErr := m.Git.Run(acq.Path, "add", "feature.txt"); gitErr != nil {
		t.Fatalf("add: %v\n%s", gitErr, out)
	}
	if out, gitErr := m.Git.Run(acq.Path, "commit", "-q", "-m", "run work"); gitErr != nil {
		t.Fatalf("commit: %v\n%s", gitErr, out)
	}
	want, err := m.Git.Run(repo, "rev-parse", acq.Branch)
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	// The run ends: directory gone, branch kept — the state a stopped session is
	// found in, and the state that used to make it unresumable.
	if err := m.Remove(repo, acq, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if dirExists(acq.Path) {
		t.Fatalf("worktree %s still on disk after Remove", acq.Path)
	}

	back, err := m.ReattachPath(repo, acq.Path)
	if err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	if back.Path != acq.Path || back.Branch != acq.Branch {
		t.Fatalf("re-attached %+v, want path %s on %s", back, acq.Path, acq.Branch)
	}
	if got := strings.TrimSpace(back.StartPoint); got != strings.TrimSpace(want) {
		t.Errorf("StartPoint = %s, want the branch tip %s", got, strings.TrimSpace(want))
	}
	// The point of the whole exercise: the run's work is there to continue.
	if body, readErr := os.ReadFile(filepath.Join(back.Path, "feature.txt")); readErr != nil {
		t.Errorf("the run's file is missing from the re-attached worktree: %v", readErr)
	} else if strings.TrimSpace(string(body)) != "the run's work" {
		t.Errorf("feature.txt = %q, want the run's own content", body)
	}

	// Idempotent: a caller may re-attach before every resume without checking.
	if _, err := m.ReattachPath(repo, acq.Path); err != nil {
		t.Errorf("second re-attach should be a no-op success, got %v", err)
	}
	if out := run("worktree", "list", "--porcelain"); strings.Count(out, acq.Path) == 0 {
		t.Errorf("worktree not registered after re-attach:\n%s", out)
	}
}

// A branch that is gone is the one case that is NOT recoverable — the work no
// longer exists anywhere, and checking out something else in its place would
// hand the caller a worktree that lies about what it holds.
func TestReattachPathBranchGone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test needs a real git binary; skipped in -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo, m, _ := reattachFixture(t)
	root, err := m.resolveRoot()
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ReattachPath(repo, filepath.Join(root, "proj", "plan-999"))
	if !errors.Is(err, ErrBranchGone) {
		t.Fatalf("err = %v, want ErrBranchGone", err)
	}
}

// A path outside <Root>/<slug>/<taskID> is refused before any git runs: this
// function checks out a branch at a caller-supplied path, so the namespace guard
// is what keeps it from ever aiming somewhere this manager does not own.
func TestReattachPathRefusesForeignPath(t *testing.T) {
	repo, m, _ := reattachFixture(t)
	for _, p := range []string{
		filepath.Join(t.TempDir(), "elsewhere", "plan-430"),
		filepath.Join(t.TempDir(), "plan-430"),
	} {
		if _, err := m.ReattachPath(repo, p); !errors.Is(err, ErrPathOccupied) {
			t.Errorf("ReattachPath(%s) err = %v, want ErrPathOccupied", p, err)
		}
	}
}

// reattachFixture is a one-commit repo plus a Manager rooted outside it.
func reattachFixture(t *testing.T) (repo string, m *Manager, run func(args ...string) string) {
	t.Helper()
	repo = t.TempDir()
	git := ExecGit{}
	run = func(args ...string) string {
		t.Helper()
		out, err := git.Run(repo, args...)
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	mustWrite(t, filepath.Join(repo, "README.md"), "hello\n")
	run("add", "README.md")
	run("commit", "-q", "-m", "init")
	return repo, &Manager{Git: git, Root: filepath.Join(t.TempDir(), "wts")}, run
}
