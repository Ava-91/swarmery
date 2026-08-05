package wtjanitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// RealRemover must delete BOTH the checkout and its branch for the harness
// namespace. Regression: routing the branch through worktree.Manager tripped
// its ErrRefusedBranch guard (that package owns swarm/ only), which removed the
// directory, failed on the branch, and reported the whole thing as a failure —
// leaving branches behind and a journal that said nothing happened.
func TestRealRemover_RemovesHarnessWorktreeAndItsBranch(t *testing.T) {
	repo, run := testRepo(t)
	wtPath := filepath.Join(t.TempDir(), "agent-real")
	run("worktree", "add", "-q", "-b", "worktree-agent-real", wtPath)

	r := NewRealRemover(&worktree.Manager{Git: worktree.ExecGit{}})
	if err := r.RemoveWorktree(repo, wtPath, "worktree-agent-real", true); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree directory still present: %v", err)
	}
	if out := run("branch", "--list", "worktree-agent-real"); strings.TrimSpace(out) != "" {
		t.Errorf("branch still present: %q", out)
	}
}

// The same must hold for the dispatcher's own namespace, which DOES go through
// Manager's guard when it deletes.
func TestRealRemover_RemovesSwarmWorktreeAndItsBranch(t *testing.T) {
	repo, run := testRepo(t)
	wtPath := filepath.Join(t.TempDir(), "swarm-task")
	run("worktree", "add", "-q", "-b", "swarm/T-1", wtPath)

	r := NewRealRemover(&worktree.Manager{Git: worktree.ExecGit{}})
	if err := r.RemoveWorktree(repo, wtPath, "swarm/T-1", true); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if out := run("branch", "--list", "swarm/T-1"); strings.TrimSpace(out) != "" {
		t.Errorf("branch still present: %q", out)
	}
}

// deleteBranch=false keeps the branch — the path a caller takes when the branch
// still carries something.
func TestRealRemover_KeepsBranchWhenAsked(t *testing.T) {
	repo, run := testRepo(t)
	wtPath := filepath.Join(t.TempDir(), "agent-keep")
	run("worktree", "add", "-q", "-b", "worktree-agent-keep", wtPath)

	r := NewRealRemover(&worktree.Manager{Git: worktree.ExecGit{}})
	if err := r.RemoveWorktree(repo, wtPath, "worktree-agent-keep", false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if out := run("branch", "--list", "worktree-agent-keep"); strings.TrimSpace(out) == "" {
		t.Error("branch was deleted despite deleteBranch=false")
	}
}

// The namespace guard is what stops "zero commits of its own" from being enough
// to delete something the janitor never created.
func TestRealRemover_RefusesForeignBranches(t *testing.T) {
	repo, _ := testRepo(t)
	r := NewRealRemover(&worktree.Manager{Git: worktree.ExecGit{}})
	for _, b := range []string{"main", "dev", "feat/whatever", "salvage/agent-x-20260805"} {
		if err := r.DeleteBranch(repo, b); err == nil {
			t.Errorf("DeleteBranch(%q) = nil; the janitor must refuse branches outside its namespace", b)
		}
	}
}

func TestOwnsBranch(t *testing.T) {
	owned := []string{"swarm/plan-1", "worktree-agent-abc123"}
	foreign := []string{"main", "dev", "salvage/agent-x-20260805", "feat/x", "swarmish/y"}
	for _, b := range owned {
		if !ownsBranch(b) {
			t.Errorf("ownsBranch(%q) = false, want true", b)
		}
	}
	for _, b := range foreign {
		if ownsBranch(b) {
			t.Errorf("ownsBranch(%q) = true, want false", b)
		}
	}
}
