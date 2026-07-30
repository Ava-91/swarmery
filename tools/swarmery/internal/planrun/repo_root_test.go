package planrun

import (
	"database/sql"
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

// multiRepoFixture rebuilds the fixture's project as an UMBRELLA directory: no
// .git of its own, one checkout inside it — the exact shape (project Skygor) that
// made every plan run fail admission on "fatal: not a git repository".
func multiRepoFixture(t *testing.T, db *sql.DB, taskID int64, phaseRepoCell string) (projectRoot, repo string) {
	t.Helper()
	projectRoot = filepath.Join(t.TempDir(), "Umbrella")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repo = mkRepo(t, filepath.Join(projectRoot, "app"))
	mustExec(t, db, `UPDATE projects SET path=? WHERE id=1`, projectRoot)
	mustExec(t, db, `UPDATE epic_phases SET repo=? WHERE workspace_task_id=?`, phaseRepoCell, taskID)
	return projectRoot, repo
}

// The whole point of the change: the worktree is cut from the declared checkout,
// not from the umbrella the project path names.
func TestStart_MultiRepoProject_AcquiresInDeclaredRepo(t *testing.T) {
	db, taskID, _ := fixture(t)
	_, repo := multiRepoFixture(t, db, taskID, "`app`")

	r := &stubRunner{}
	wt := &stubWt{}
	s := newTestService(db, r, wt)
	s.RepoRoot = nil // exercise the REAL resolver — that is what is under test here

	if _, err := s.Start(taskID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := wt.lastAcquireRoot(); !sameDir(t, got, repo) {
		t.Fatalf("Acquire repoRoot = %q, want the declared checkout %q", got, repo)
	}
	if got := wt.reclaimedList(); len(got) == 0 {
		t.Fatal("the branch was never reclaimed — admission did not reach the worktree")
	}
}

// The regression that guards every other project in the registry: when the
// project path IS a checkout, resolution must not move the run anywhere, even
// though the phases declare a repo that does not exist on disk.
func TestStart_SingleRepoProject_UsesProjectPath(t *testing.T) {
	db, taskID, _ := fixture(t)
	projectRoot := mkRepo(t, filepath.Join(t.TempDir(), "solo"))
	mustExec(t, db, `UPDATE projects SET path=? WHERE id=1`, projectRoot)
	mustExec(t, db, `UPDATE epic_phases SET repo='`+"`ghost-repo`"+`' WHERE workspace_task_id=?`, taskID)

	r := &stubRunner{}
	wt := &stubWt{}
	s := newTestService(db, r, wt)
	s.RepoRoot = nil

	if _, err := s.Start(taskID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := wt.lastAcquireRoot(); !sameDir(t, got, projectRoot) {
		t.Fatalf("Acquire repoRoot = %q, want the project path %q", got, projectRoot)
	}
}

// A project path that is not a checkout and declares nothing usable is an
// admission verdict: no worktree, no plan_runs row, and the single-flight slot
// free for the retry that follows the fix.
func TestStart_NoRepoRoot_RefusesAndLeavesNoState(t *testing.T) {
	db, taskID, _ := fixture(t)
	projectRoot := filepath.Join(t.TempDir(), "Umbrella")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE projects SET path=? WHERE id=1`, projectRoot)

	r := &stubRunner{}
	wt := &stubWt{}
	s := newTestService(db, r, wt)
	s.RepoRoot = nil

	_, err := s.Start(taskID, "", "")
	if !errors.Is(err, ErrNoRepoRoot) {
		t.Fatalf("Start err = %v, want ErrNoRepoRoot", err)
	}
	// The message replaces git's "fatal: not a git repository" — it must name what
	// was checked, or the user is no better off than before.
	if !strings.Contains(err.Error(), projectRoot) {
		t.Errorf("error %q does not name the path it checked", err)
	}
	if wt.acquiredCount() != 0 {
		t.Error("a worktree was acquired despite the admission failure")
	}
	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_runs WHERE workspace_task_id=?`, taskID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Errorf("plan_runs rows = %d, want 0", runs)
	}
	// Slot released: a second attempt must fail the SAME way, not with ErrRunning.
	if _, err := s.Start(taskID, "", ""); !errors.Is(err, ErrNoRepoRoot) {
		t.Errorf("second Start = %v, want ErrNoRepoRoot (the slot leaked)", err)
	}
}

// One worktree cannot hold two repos. The refusal names them, because "run the
// phases individually" is only actionable if the user can see the split.
func TestStart_PlanSpansRepos_Refuses(t *testing.T) {
	db, taskID, _ := fixture(t)
	projectRoot, _ := multiRepoFixture(t, db, taskID, "`app`")
	mkRepo(t, filepath.Join(projectRoot, "infra"))
	mustExec(t, db, `UPDATE epic_phases SET repo='`+"`infra`"+`' WHERE workspace_task_id=? AND seq=2`, taskID)

	s := newTestService(db, &stubRunner{}, &stubWt{})
	s.RepoRoot = nil

	_, err := s.Start(taskID, "", "")
	var spans *PlanSpansReposError
	if !errors.As(err, &spans) {
		t.Fatalf("Start err = %v, want *PlanSpansReposError", err)
	}
	if len(spans.Repos) != 2 || spans.Repos[0] != "app" || spans.Repos[1] != "infra" {
		t.Errorf("Repos = %v, want [app infra] sorted", spans.Repos)
	}
	if !errors.Is(err, ErrPlanSpansRepos) {
		t.Error("the error does not match its sentinel")
	}
}

// A finished phase in another repo is history, not a conflict. Counting it would
// strand the remaining work behind a split that no longer exists.
func TestStart_CompletedPhaseInAnotherRepoDoesNotSplitThePlan(t *testing.T) {
	db, taskID, _ := fixture(t)
	projectRoot, repo := multiRepoFixture(t, db, taskID, "`app`")
	mkRepo(t, filepath.Join(projectRoot, "infra"))
	// Phase 1 is done and lived in `infra`; phase 2 is the remaining work in `app`.
	mustExec(t, db, `UPDATE epic_phases SET repo='`+"`infra`"+`', checkboxes_done=checkboxes_total
		WHERE workspace_task_id=? AND seq=1`, taskID)

	wt := &stubWt{}
	s := newTestService(db, &stubRunner{}, wt)
	s.RepoRoot = nil

	if _, err := s.Start(taskID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := wt.lastAcquireRoot(); !sameDir(t, got, repo) {
		t.Fatalf("Acquire repoRoot = %q, want the unfinished phase's repo %q", got, repo)
	}
}

// The branch lives in the repo the run resolved to; deleting it at the project
// root would find nothing and report the no-op as a deletion.
func TestDeleteRunBranch_UsesResolvedRepo(t *testing.T) {
	db, taskID, _ := fixture(t)
	_, repo := multiRepoFixture(t, db, taskID, "`app`")

	wt := &stubWt{}
	s := newTestService(db, &stubRunner{}, wt)
	s.RepoRoot = nil

	branch, existed, err := s.DeleteRunBranch(taskID)
	if err != nil {
		t.Fatalf("DeleteRunBranch: %v", err)
	}
	if !existed || branch == "" {
		t.Fatalf("DeleteRunBranch = (%q, %v)", branch, existed)
	}
	if got := wt.lastDeleteRoot(); !sameDir(t, got, repo) {
		t.Fatalf("DeleteBranch repoRoot = %q, want %q", got, repo)
	}
}

// The prompt orients the agent only when the worktree is NOT the project root —
// otherwise the note is noise in every single-repo run.
func TestBuildPromptIn_RepoNote(t *testing.T) {
	phases := []Phase{{Seq: 1, Name: "P1", DocPath: "/plan/phase-1.md", Total: 1}}

	multi := BuildPromptIn("/plan", "readme", phases, ModeAuto, "/proj/app", "/proj")
	if !strings.Contains(multi, "REPOSITORY:") || !strings.Contains(multi, "`app/src/...`") {
		t.Errorf("multi-repo prompt is missing the orientation block:\n%s", multi)
	}
	solo := BuildPromptIn("/plan", "readme", phases, ModeAuto, "/proj", "/proj")
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
