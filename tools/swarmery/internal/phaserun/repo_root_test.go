package phaserun

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkRepo marks dir as a git checkout for repopath.Resolve (which stats .git and
// never shells out to git).
func mkRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The change under test: the worktree is cut from the repository the PHASE DOC
// declares, not from the umbrella directory projects.path names. Before this,
// admission died probing the run branch with "fatal: not a git repository".
func TestStart_MultiRepoProject_AcquiresInDeclaredRepo(t *testing.T) {
	db, _, p1, _ := fixture(t)
	projectRoot := filepath.Join(t.TempDir(), "Umbrella")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := mkRepo(t, filepath.Join(projectRoot, "app"))
	mustExec(t, db, `UPDATE projects SET path=? WHERE id=1`, projectRoot)
	mustExec(t, db, "UPDATE epic_phases SET repo='`app`' WHERE id=?", p1)

	wt := &stubWt{}
	s := newTestService(db, &stubRunner{}, wt)
	s.RepoRoot = nil // exercise the REAL resolver — that is what is under test

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := wt.lastAcquireRoot(); !sameDir(t, got, repo) {
		t.Fatalf("Acquire repoRoot = %q, want the declared checkout %q", got, repo)
	}
}

// The regression guarding every existing project: a project path that IS a
// checkout stays the run root, even when the doc declares a repo that is not on
// disk.
func TestStart_SingleRepoProject_UsesProjectPath(t *testing.T) {
	db, _, p1, _ := fixture(t)
	projectRoot := mkRepo(t, filepath.Join(t.TempDir(), "solo"))
	mustExec(t, db, `UPDATE projects SET path=? WHERE id=1`, projectRoot)
	mustExec(t, db, "UPDATE epic_phases SET repo='`ghost-repo`' WHERE id=?", p1)

	wt := &stubWt{}
	s := newTestService(db, &stubRunner{}, wt)
	s.RepoRoot = nil

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := wt.lastAcquireRoot(); !sameDir(t, got, projectRoot) {
		t.Fatalf("Acquire repoRoot = %q, want the project path %q", got, projectRoot)
	}
}

// Nothing resolves ⇒ an admission verdict: no worktree, the phase row untouched,
// the single-flight slot free for the retry that follows the fix.
func TestStart_NoRepoRoot_RefusesAndLeavesNoState(t *testing.T) {
	db, _, p1, _ := fixture(t)
	projectRoot := filepath.Join(t.TempDir(), "Umbrella")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE projects SET path=? WHERE id=1`, projectRoot)

	wt := &stubWt{}
	s := newTestService(db, &stubRunner{}, wt)
	s.RepoRoot = nil

	err := errors.New("")
	if _, err = s.Start(p1); !errors.Is(err, ErrNoRepoRoot) {
		t.Fatalf("Start err = %v, want ErrNoRepoRoot", err)
	}
	// The message replaces git's "fatal: not a git repository" — it has to name
	// what was checked, or the user is no better off than before.
	if !strings.Contains(err.Error(), projectRoot) {
		t.Errorf("error %q does not name the path it checked", err)
	}
	if len(wt.acquireRoots) != 0 {
		t.Error("a worktree was acquired despite the admission failure")
	}
	var state string
	if err := db.QueryRow(`SELECT run_state FROM epic_phases WHERE id=?`, p1).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "idle" {
		t.Errorf("run_state = %q, want it untouched at idle", state)
	}
	if _, err := s.Start(p1); !errors.Is(err, ErrNoRepoRoot) {
		t.Errorf("second Start = %v, want ErrNoRepoRoot (the slot leaked)", err)
	}
}

// The prompt orients the agent only when the worktree is NOT the project root.
func TestBuildPromptIn_RepoNote(t *testing.T) {
	multi := BuildPromptIn("/plan/phase-1.md", "phase-1.md", "body", "/proj/app", "/proj")
	if !strings.Contains(multi, "REPOSITORY:") || !strings.Contains(multi, "`app/src/...`") {
		t.Errorf("multi-repo prompt is missing the orientation block:\n%s", multi)
	}
	solo := BuildPromptIn("/plan/phase-1.md", "phase-1.md", "body", "/proj", "/proj")
	if strings.Contains(solo, "REPOSITORY:") {
		t.Error("single-repo prompt should not carry the orientation block")
	}
}

// sameDir compares paths after symlink resolution — on macOS /var → /private/var
// makes a raw string compare flaky in exactly these assertions.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}
