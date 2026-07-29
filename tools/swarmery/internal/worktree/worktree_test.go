package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubGit is a scripted Git: it records every invocation and returns a canned
// (output, error) per matched arg-prefix. The most-recently-added matching
// script wins, so tests can register a default and override a specific verb.
type stubGit struct {
	calls   []string
	scripts []scriptEntry
}

type scriptEntry struct {
	match  func(args []string) bool
	output string
	err    error
	// fn, when set, is called (after recording) to mutate stub state or return
	// a dynamic result — used to make "list" reflect a prior "add".
	fn func(args []string) (string, error)
}

func (g *stubGit) Run(dir string, args ...string) (string, error) {
	g.calls = append(g.calls, strings.Join(args, " "))
	for i := len(g.scripts) - 1; i >= 0; i-- {
		s := g.scripts[i]
		if s.match(args) {
			if s.fn != nil {
				return s.fn(args)
			}
			return s.output, s.err
		}
	}
	return "", nil // unscripted verbs succeed with empty output
}

// on registers a script matching when args starts with the given verb tokens.
func (g *stubGit) on(verb string, output string, err error) *stubGit {
	toks := strings.Fields(verb)
	g.scripts = append(g.scripts, scriptEntry{
		match:  func(args []string) bool { return hasPrefix(args, toks) },
		output: output, err: err,
	})
	return g
}

func (g *stubGit) onFn(verb string, fn func(args []string) (string, error)) *stubGit {
	toks := strings.Fields(verb)
	g.scripts = append(g.scripts, scriptEntry{
		match: func(args []string) bool { return hasPrefix(args, toks) },
		fn:    fn,
	})
	return g
}

func hasPrefix(args, toks []string) bool {
	if len(args) < len(toks) {
		return false
	}
	for i, t := range toks {
		if args[i] != t {
			return false
		}
	}
	return true
}

func (g *stubGit) called(substr string) bool {
	for _, c := range g.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// baseStub is a Git that answers the resolveStartPoint handshake (default
// branch "main" @ SHA1) and an empty worktree list — the happy prelude every
// Acquire runs before its decision logic.
func baseStub() *stubGit {
	g := &stubGit{}
	g.on("symbolic-ref --short HEAD", "main\n", nil)
	g.on("rev-parse refs/heads/main", "aaaa1111\n", nil)
	g.on("rev-parse HEAD", "aaaa1111\n", nil)
	g.on("worktree list --porcelain", "", nil)
	g.on("worktree prune", "", nil)
	return g
}

// newMgr builds a Manager rooted at a temp dir with the given stub.
func newMgr(t *testing.T, g Git) *Manager {
	t.Helper()
	return &Manager{Git: g, Root: filepath.Join(t.TempDir(), "wts")}
}

// ---- Invariant 1: explicit startPoint ------------------------------------

func TestAcquirePinsExplicitStartPoint(t *testing.T) {
	g := baseStub()
	m := newMgr(t, g)
	a, err := m.Acquire("/tmp/repo", "proj", "T-abc123")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if a.StartPoint != "aaaa1111" {
		t.Errorf("StartPoint = %q, want the default-branch tip aaaa1111", a.StartPoint)
	}
	if a.Branch != "swarm/T-abc123" {
		t.Errorf("Branch = %q, want swarm/T-abc123", a.Branch)
	}
	// The add MUST include the -b branch AND the explicit start SHA, never a
	// bare `worktree add <path>` (Fusion FNXC:WorktreeIsolation).
	var addCall string
	for _, c := range g.calls {
		if strings.HasPrefix(c, "worktree add") {
			addCall = c
		}
	}
	if addCall == "" {
		t.Fatal("no `worktree add` call recorded")
	}
	if !strings.Contains(addCall, "-b swarm/T-abc123") || !strings.HasSuffix(addCall, "aaaa1111") {
		t.Errorf("add call = %q, want `-b swarm/T-abc123 <path> aaaa1111`", addCall)
	}
}

// resolveStartPoint pins to the DEFAULT branch tip even when the checkout sits
// on a sibling branch during recovery.
func TestAcquirePinsDefaultBranchNotAmbientHead(t *testing.T) {
	g := &stubGit{}
	g.on("symbolic-ref --short HEAD", "recovery-branch\n", nil) // ambient HEAD elsewhere
	g.on("rev-parse refs/heads/recovery-branch", "bbbb2222\n", nil)
	// symbolic-ref returns recovery-branch, so we pin to refs/heads/recovery-branch.
	// (The default-branch resolution follows symbolic-ref; the invariant is that
	// we resolve a NAMED ref explicitly, never the raw checkout HEAD blob.)
	g.on("worktree list --porcelain", "", nil)
	m := newMgr(t, g)
	a, err := m.Acquire("/tmp/repo", "proj", "T-x")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if a.StartPoint != "bbbb2222" {
		t.Errorf("StartPoint = %q, want bbbb2222 (resolved named ref)", a.StartPoint)
	}
	if g.called("rev-parse HEAD") {
		t.Error("Acquire used raw `rev-parse HEAD` when a symbolic ref was available")
	}
}

// ---- Invariant 2: repo-root guard ----------------------------------------

func TestAcquireRefusesPathInsideRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	// Root maliciously set inside the repo → computed path nests in repoRoot.
	m := &Manager{Git: baseStub(), Root: filepath.Join(repoRoot, "wts")}
	_, err := m.Acquire(repoRoot, "proj", "T-x")
	if !errors.Is(err, ErrRepoRootRefused) {
		t.Fatalf("err = %v, want ErrRepoRootRefused", err)
	}
}

func TestGuardRepoRootDirections(t *testing.T) {
	// equal, path-inside-repo, repo-inside-path all refuse.
	cases := [][2]string{
		{"/a/b", "/a/b"},   // equal
		{"/a/b", "/a/b/c"}, // path inside repo
		{"/a/b/c", "/a/b"}, // repo inside path
	}
	for _, c := range cases {
		if err := guardRepoRoot(c[0], c[1]); !errors.Is(err, ErrRepoRootRefused) {
			t.Errorf("guardRepoRoot(%q,%q) = %v, want ErrRepoRootRefused", c[0], c[1], err)
		}
	}
	// Disjoint siblings are fine.
	if err := guardRepoRoot("/a/repo", "/a/worktrees/T-x"); err != nil {
		t.Errorf("guardRepoRoot(disjoint) = %v, want nil", err)
	}
}

// TestEvalOrCleanKeepsDeepestComponent guards against a regression where
// evalOrClean dropped the basename of the deepest non-existent directory when
// its parent resolved directly — collapsing e.g. "<real>/repo" to "<real>" and
// making the repo-root guard over-match. Reproduces the Linux-CI condition
// where repoRoot and the worktree root share a symlinked real parent.
func TestEvalOrCleanKeepsDeepestComponent(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks(real): %v", err)
	}

	// Direct child of an existing (symlinked) dir must be preserved.
	if got, want := evalOrClean(filepath.Join(link, "repo")), filepath.Join(resolvedReal, "repo"); got != want {
		t.Errorf("evalOrClean(link/repo) = %q, want %q", got, want)
	}
	// Multi-level non-existent tail under a symlinked existing dir.
	if got, want := evalOrClean(filepath.Join(link, "wts", "proj", "T-x")), filepath.Join(resolvedReal, "wts", "proj", "T-x"); got != want {
		t.Errorf("evalOrClean(link/wts/proj/T-x) = %q, want %q", got, want)
	}
	// A sibling repoRoot under the same symlinked parent must NOT be seen as
	// containing the worktree path (the exact false-positive fixed here).
	repoRoot := filepath.Join(link, "repo")
	wtPath := filepath.Join(link, "wts", "proj", "T-x")
	if err := guardRepoRoot(repoRoot, wtPath); err != nil {
		t.Errorf("guardRepoRoot(sibling under symlink) = %v, want nil", err)
	}
}

// ---- Invariant 3: branch-exists conflict fails loudly --------------------

func TestAcquireBranchBusyElsewhere(t *testing.T) {
	g := baseStub()
	// The branch is checked out at a DIFFERENT path than ours.
	g.on("worktree list --porcelain",
		"worktree /some/other/place\nbranch refs/heads/swarm/T-busy\n\n", nil)
	m := newMgr(t, g)
	_, err := m.Acquire("/tmp/repo", "proj", "T-busy")
	if !errors.Is(err, ErrBranchBusy) {
		t.Fatalf("err = %v, want ErrBranchBusy", err)
	}
	// No rename attempted, no destructive add.
	if g.called("worktree add") {
		t.Error("Acquire attempted an add despite a busy branch")
	}
}

func TestAcquireBranchExistsButNotCheckedOut(t *testing.T) {
	g := baseStub()
	// list is empty (branch not checked out), but `add` reports the branch exists.
	g.on("worktree add", "fatal: a branch named 'swarm/T-x' already exists", errors.New("exit 128"))
	m := newMgr(t, g)
	_, err := m.Acquire("/tmp/repo", "proj", "T-x")
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("err = %v, want ErrBranchExists (branch exists on add)", err)
	}
	// The message must not send the reader looking for a worktree: the list probe
	// just proved none holds this branch.
	if strings.Contains(err.Error(), "another worktree") {
		t.Errorf("err = %q, must not claim another worktree holds the branch", err)
	}
}

// The two conflicts have different remedies — release a checkout vs merge-or-delete a
// leftover — so they must never satisfy each other's errors.Is. Collapsing them is the
// bug this pair of sentinels exists to prevent.
func TestBranchConflictSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrBranchExists, ErrBranchBusy) {
		t.Error("ErrBranchExists matches ErrBranchBusy — a mere name collision would read as a live conflict")
	}
	if errors.Is(ErrBranchBusy, ErrBranchExists) {
		t.Error("ErrBranchBusy matches ErrBranchExists — a live checkout would read as a deletable leftover")
	}
}

// The busy path must stay busy: a branch checked out elsewhere is not a leftover, and
// telling the operator to delete it would destroy a running task's checkout.
func TestAcquireBusyIsNotReportedAsExists(t *testing.T) {
	g := baseStub()
	g.on("worktree list --porcelain",
		"worktree /some/other/place\nbranch refs/heads/swarm/T-busy\n\n", nil)
	m := newMgr(t, g)
	_, err := m.Acquire("/tmp/repo", "proj", "T-busy")
	if errors.Is(err, ErrBranchExists) {
		t.Fatalf("err = %v, want ErrBranchBusy only", err)
	}
	if !strings.Contains(err.Error(), "/some/other/place") {
		t.Errorf("err = %q, want it to name the holding worktree", err)
	}
}

// ---- Invariant 4: reuse-or-reset -----------------------------------------

func TestAcquireWarmReuseBranchMatched(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wts")
	path := filepath.Join(root, "proj", "T-reuse")
	g := baseStub()
	// Our exact path is registered on our exact branch → reuse as-is.
	g.on("worktree list --porcelain",
		fmt.Sprintf("worktree %s\nbranch refs/heads/swarm/T-reuse\n\n", path), nil)
	m := &Manager{Git: g, Root: root}
	a, err := m.Acquire("/tmp/repo", "proj", "T-reuse")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if a.Path != path {
		t.Errorf("Path = %q, want %q", a.Path, path)
	}
	// Warm reuse: no remove, no add.
	if g.called("worktree remove") || g.called("worktree add") {
		t.Errorf("warm reuse must not remove/add; calls=%v", g.calls)
	}
}

func TestAcquireReclaimsForeignBranchAtPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wts")
	path := filepath.Join(root, "proj", "T-foreign")
	g := baseStub()
	// Our path is registered but on a FOREIGN branch → remove + recreate.
	g.on("worktree list --porcelain",
		fmt.Sprintf("worktree %s\nbranch refs/heads/some-other-branch\n\n", path), nil)
	m := &Manager{Git: g, Root: root}
	if _, err := m.Acquire("/tmp/repo", "proj", "T-foreign"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !g.called("worktree remove --force " + path) {
		t.Errorf("expected reclaim remove of %s; calls=%v", path, g.calls)
	}
	if !g.called("worktree add -b swarm/T-foreign") {
		t.Error("expected recreate add after reclaim")
	}
}

func TestAcquireTransientListFailureRetriesNeverDestroys(t *testing.T) {
	g := baseStub()
	// First list call errors, second succeeds (empty). Must retry, not destroy.
	var listCalls int
	g.onFn("worktree list --porcelain", func(args []string) (string, error) {
		listCalls++
		if listCalls == 1 {
			return "", errors.New("transient: could not lock")
		}
		return "", nil
	})
	m := newMgr(t, g)
	if _, err := m.Acquire("/tmp/repo", "proj", "T-x"); err != nil {
		t.Fatalf("Acquire after retry: %v", err)
	}
	if listCalls < 2 {
		t.Errorf("list retried %d times, want >= 2", listCalls)
	}
}

func TestAcquireTransientListFailsBothTimes(t *testing.T) {
	g := baseStub()
	g.on("worktree list --porcelain", "", errors.New("still locked"))
	m := newMgr(t, g)
	_, err := m.Acquire("/tmp/repo", "proj", "T-x")
	if err == nil || errors.Is(err, ErrBranchBusy) {
		t.Fatalf("err = %v, want a non-busy list error after two failures", err)
	}
	// Crucially, no destructive remove happened on a flaky probe.
	if g.called("worktree remove") {
		t.Error("Acquire removed a worktree on a transient list failure")
	}
}

// ---- Invariant 5: stale-lock sweep ---------------------------------------

func TestAcquireSweepsStaleLocks(t *testing.T) {
	repoRoot := t.TempDir()
	// Build a fake registered worktree dir with an OLD index.lock and a fresh one.
	wtDir := filepath.Join(repoRoot, ".git", "worktrees", "old")
	mustMkdir(t, wtDir)
	oldLock := filepath.Join(wtDir, "index.lock")
	mustWrite(t, oldLock, "lock")
	freshDir := filepath.Join(repoRoot, ".git", "worktrees", "fresh")
	mustMkdir(t, freshDir)
	freshLock := filepath.Join(freshDir, "index.lock")
	mustWrite(t, freshLock, "lock")

	g := baseStub()
	m := &Manager{
		Git:  g,
		Root: filepath.Join(t.TempDir(), "wts"),
		now:  func() time.Time { return time.Now() },
	}
	// Age the old lock past the threshold.
	ageFile(t, oldLock, time.Now().Add(-staleLockAge-time.Minute))

	if _, err := m.Acquire(repoRoot, "proj", "T-x"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if fileExists(oldLock) {
		t.Error("stale index.lock was not swept")
	}
	if !fileExists(freshLock) {
		t.Error("fresh index.lock must be preserved")
	}
}

// ---- Invariant 6: trailer format -----------------------------------------

func TestTrailerFormat(t *testing.T) {
	if got := Trailer("T-abc123"); got != "Swarm-Task-Id: T-abc123" {
		t.Errorf("Trailer = %q, want Swarm-Task-Id: T-abc123", got)
	}
}

func TestRegexpEscapeMetachars(t *testing.T) {
	// A hypothetical id with ERE metacharacters must be escaped so the grep
	// matches it literally (defensive — real ids are T-<base36>).
	got := regexpEscape("a.b+c*")
	if got != `a\.b\+c\*` {
		t.Errorf("regexpEscape = %q, want a\\.b\\+c\\*", got)
	}
	// Hyphen stays literal.
	if regexpEscape("T-x") != "T-x" {
		t.Errorf("regexpEscape(T-x) escaped a hyphen")
	}
}

func TestCommitsForTaskGrepsExactTrailer(t *testing.T) {
	g := &stubGit{}
	g.on("log", "deadbeef\ncafef00d\n", nil)
	m := &Manager{Git: g}
	shas, err := m.CommitsForTask("/tmp/repo", "T-abc123")
	if err != nil {
		t.Fatalf("CommitsForTask: %v", err)
	}
	if len(shas) != 2 || shas[0] != "deadbeef" || shas[1] != "cafef00d" {
		t.Fatalf("shas = %v", shas)
	}
	// The grep must reference the exact trailer line, anchored (^…$). Hyphens
	// are not ERE metacharacters, so they are not escaped.
	last := g.calls[len(g.calls)-1]
	if !strings.Contains(last, "--grep") || !strings.Contains(last, "^Swarm-Task-Id: T-abc123$") {
		t.Errorf("log call = %q, want anchored exact-trailer grep", last)
	}
}

// ---- Remove / Prune ------------------------------------------------------

func TestRemoveKeepBranch(t *testing.T) {
	g := &stubGit{}
	m := &Manager{Git: g}
	a := Acquired{Path: "/wt/T-x", Branch: "swarm/T-x"}
	if err := m.Remove("/tmp/repo", a, true); err != nil {
		t.Fatalf("Remove(keep): %v", err)
	}
	if !g.called("worktree remove --force /wt/T-x") {
		t.Error("Remove did not force-remove the worktree")
	}
	if g.called("branch -D") {
		t.Error("keepBranch=true must NOT delete the branch")
	}
}

func TestRemoveDeleteBranch(t *testing.T) {
	g := &stubGit{}
	m := &Manager{Git: g}
	a := Acquired{Path: "/wt/T-x", Branch: "swarm/T-x"}
	if err := m.Remove("/tmp/repo", a, false); err != nil {
		t.Fatalf("Remove(delete): %v", err)
	}
	if !g.called("branch -D swarm/T-x") {
		t.Error("keepBranch=false must delete the branch")
	}
}

func TestPruneSweepsAndPrunes(t *testing.T) {
	repoRoot := t.TempDir()
	wtDir := filepath.Join(repoRoot, ".git", "worktrees", "gone")
	mustMkdir(t, wtDir)
	lock := filepath.Join(wtDir, "index.lock")
	mustWrite(t, lock, "x")
	ageFile(t, lock, time.Now().Add(-staleLockAge-time.Minute))

	g := &stubGit{}
	m := &Manager{Git: g, Root: filepath.Join(t.TempDir(), "wts")}
	if err := m.Prune(repoRoot); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if fileExists(lock) {
		t.Error("Prune did not sweep the stale lock")
	}
	if !g.called("worktree prune") {
		t.Error("Prune did not call `git worktree prune`")
	}
}

func TestPrunePropagatesGitError(t *testing.T) {
	g := &stubGit{}
	g.on("worktree prune", "boom", errors.New("exit 1"))
	m := &Manager{Git: g, Root: t.TempDir()}
	if err := m.Prune(t.TempDir()); err == nil {
		t.Error("Prune should propagate a git prune failure")
	}
}

func TestRemovePropagatesErrors(t *testing.T) {
	// worktree remove fails → error surfaced, branch delete not attempted.
	g := &stubGit{}
	g.on("worktree remove", "cannot remove", errors.New("exit 1"))
	m := &Manager{Git: g}
	if err := m.Remove("/tmp/repo", Acquired{Path: "/wt/T-x", Branch: "swarm/T-x"}, false); err == nil {
		t.Error("Remove should propagate a worktree-remove failure")
	}
	if g.called("branch -D") {
		t.Error("branch delete must not run after a failed remove")
	}
	// remove ok but branch -D fails → error surfaced.
	g2 := &stubGit{}
	g2.on("branch -D", "no such branch", errors.New("exit 1"))
	m2 := &Manager{Git: g2}
	if err := m2.Remove("/tmp/repo", Acquired{Path: "/wt/T-x", Branch: "swarm/T-x"}, false); err == nil {
		t.Error("Remove should propagate a branch-delete failure")
	}
}

// ---- ReclaimEmptyBranch / DeleteBranch -----------------------------------

// wantCalls asserts the EXACT recorded git command sequence. Reclaim's safety
// depends on the ORDER of its probes (existence → HEAD → prune → list → count →
// delete), so asserting only the return value would let a reordering that skips
// a guard pass.
func wantCalls(t *testing.T, g *stubGit, want ...string) {
	t.Helper()
	if len(g.calls) != len(want) {
		t.Fatalf("git calls =\n  %v\nwant\n  %v", g.calls, want)
	}
	for i := range want {
		if g.calls[i] != want[i] {
			t.Fatalf("git call[%d] = %q, want %q (full: %v)", i, g.calls[i], want[i], g.calls)
		}
	}
}

func TestReclaimEmptyBranchMissingBranchIsNoop(t *testing.T) {
	g := baseStub()
	// `rev-parse --verify --quiet` on an absent ref: non-zero exit, NO output.
	g.on("rev-parse --verify --quiet", "", errors.New("exit 1"))
	m := newMgr(t, g)
	ahead, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-714")
	if err != nil {
		t.Fatalf("ReclaimEmptyBranch: %v", err)
	}
	if ahead != 0 {
		t.Errorf("ahead = %d, want 0 for a missing branch", ahead)
	}
	// Nothing beyond the existence probe: no prune, no list, no delete.
	wantCalls(t, g, "rev-parse --verify --quiet refs/heads/swarm/phase-714")
}

func TestReclaimEmptyBranchDeletesEmptyLeftover(t *testing.T) {
	g := baseStub()
	g.on("rev-list --count", "0\n", nil) // zero commits ahead of the base
	m := newMgr(t, g)
	ahead, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-714")
	if err != nil {
		t.Fatalf("ReclaimEmptyBranch: %v", err)
	}
	if ahead != 0 {
		t.Errorf("ahead = %d, want 0", ahead)
	}
	wantCalls(t, g,
		"rev-parse --verify --quiet refs/heads/swarm/phase-714",
		"symbolic-ref --short HEAD",
		"worktree prune",
		"worktree list --porcelain",
		// reclaimBase — symbolic-ref only, no detached-HEAD fallback.
		"symbolic-ref --short HEAD",
		"rev-parse refs/heads/main",
		"rev-list --count aaaa1111..refs/heads/swarm/phase-714",
		"branch -D swarm/phase-714",
	)
}

func TestReclaimEmptyBranchKeepsBranchWithCommits(t *testing.T) {
	g := baseStub()
	g.on("rev-list --count", "3\n", nil)
	m := newMgr(t, g)
	ahead, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-715")
	if err != nil {
		t.Fatalf("ReclaimEmptyBranch: %v", err)
	}
	if ahead != 3 {
		t.Errorf("ahead = %d, want 3", ahead)
	}
	// Identical prelude, but the sequence STOPS at the count — a branch holding
	// work is never destroyed implicitly.
	wantCalls(t, g,
		"rev-parse --verify --quiet refs/heads/swarm/phase-715",
		"symbolic-ref --short HEAD",
		"worktree prune",
		"worktree list --porcelain",
		"symbolic-ref --short HEAD",
		"rev-parse refs/heads/main",
		"rev-list --count aaaa1111..refs/heads/swarm/phase-715",
	)
}

func TestReclaimEmptyBranchRefusesCheckedOutBranch(t *testing.T) {
	g := baseStub()
	g.on("worktree list --porcelain",
		"worktree /wt/proj/phase-716\nbranch refs/heads/swarm/phase-716\n\n", nil)
	m := newMgr(t, g)
	_, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-716")
	if !errors.Is(err, ErrBranchCheckedOut) {
		t.Fatalf("err = %v, want ErrBranchCheckedOut", err)
	}
	if !strings.Contains(err.Error(), "/wt/proj/phase-716") {
		t.Errorf("err = %v, want it to name the holding worktree path", err)
	}
	if g.called("branch -D") {
		t.Error("a checked-out branch must never be deleted")
	}
	if g.called("rev-list") {
		t.Error("the guard must short-circuit before counting commits")
	}
}

// The HEAD guard is reachable only for a swarm/ branch now that the namespace
// guard runs first, so the repo is scripted as sitting ON the run branch — the
// state a user lands in by checking a run branch out to inspect it.
func TestReclaimEmptyBranchRefusesHeadBranch(t *testing.T) {
	g := baseStub()
	g.on("symbolic-ref --short HEAD", "swarm/phase-716\n", nil)
	m := newMgr(t, g)
	_, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-716")
	if !errors.Is(err, ErrBranchIsHead) {
		t.Fatalf("err = %v, want ErrBranchIsHead", err)
	}
	if g.called("branch -D") {
		t.Error("the repo's HEAD branch must never be deleted")
	}
	wantCalls(t, g,
		"rev-parse --verify --quiet refs/heads/swarm/phase-716",
		"symbolic-ref --short HEAD",
	)
}

// ---- I2: the swarm/ namespace guard --------------------------------------

// A branch outside the swarm/ namespace is refused by BOTH reclaim paths before
// any git command runs. Without it, ReclaimEmptyBranch("dev") on a repo whose
// HEAD is a feature branch that already contains dev computes ahead == 0 and
// deletes dev — the guard closes the class at the boundary rather than relying on
// every caller to build the name correctly.
func TestReclaimEmptyBranchRefusesForeignNamespace(t *testing.T) {
	for _, branch := range []string{"dev", "main", "feature/x", "swarm/", "swarmish/x"} {
		g := baseStub()
		m := newMgr(t, g)
		_, err := m.ReclaimEmptyBranch("/tmp/repo", branch)
		if !errors.Is(err, ErrRefusedBranch) {
			t.Errorf("ReclaimEmptyBranch(%q) err = %v, want ErrRefusedBranch", branch, err)
		}
		if !strings.Contains(err.Error(), branch) {
			t.Errorf("err = %v, want it to name the branch %q", err, branch)
		}
		if len(g.calls) != 0 {
			t.Errorf("ReclaimEmptyBranch(%q) issued git calls %v, want none", branch, g.calls)
		}
	}
}

func TestDeleteBranchRefusesForeignNamespace(t *testing.T) {
	for _, branch := range []string{"dev", "main", "feature/x", "swarm/"} {
		g := baseStub()
		m := newMgr(t, g)
		_, err := m.DeleteBranch("/tmp/repo", branch)
		if !errors.Is(err, ErrRefusedBranch) {
			t.Errorf("DeleteBranch(%q) err = %v, want ErrRefusedBranch", branch, err)
		}
		if len(g.calls) != 0 {
			t.Errorf("DeleteBranch(%q) issued git calls %v, want none", branch, g.calls)
		}
	}
}

func TestReclaimEmptyBranchNonNumericCount(t *testing.T) {
	g := baseStub()
	g.on("rev-list --count", "not-a-number\n", nil)
	m := newMgr(t, g)
	_, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-717")
	if err == nil {
		t.Fatal("err = nil, want a parse error")
	}
	if !strings.Contains(err.Error(), "swarm/phase-717") {
		t.Errorf("err = %v, want it to name the branch", err)
	}
	if g.called("branch -D") {
		t.Error("an unparseable count must not authorize a delete")
	}
}

func TestReclaimEmptyBranchPropagatesListFailure(t *testing.T) {
	g := baseStub()
	g.on("worktree list --porcelain", "", errors.New("could not lock"))
	m := newMgr(t, g)
	if _, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-1"); err == nil {
		t.Fatal("err = nil, want the worktree-list failure surfaced")
	}
	if g.called("branch -D") {
		t.Error("a failed list probe must not authorize a delete")
	}
}

func TestDeleteBranchDeletesBranchWithCommits(t *testing.T) {
	g := baseStub()
	g.on("rev-list --count", "3\n", nil) // has work — DeleteBranch discards it anyway
	m := newMgr(t, g)
	existed, err := m.DeleteBranch("/tmp/repo", "swarm/phase-715")
	if err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if !existed {
		t.Error("existed = false, want true — the branch was there and was deleted")
	}
	wantCalls(t, g,
		"rev-parse --verify --quiet refs/heads/swarm/phase-715",
		"symbolic-ref --short HEAD",
		"worktree prune",
		"worktree list --porcelain",
		// No rev-list: the explicit decision has already been made.
		"branch -D swarm/phase-715",
	)
}

func TestDeleteBranchMissingIsIdempotent(t *testing.T) {
	g := baseStub()
	g.on("rev-parse --verify --quiet", "", errors.New("exit 1"))
	m := newMgr(t, g)
	existed, err := m.DeleteBranch("/tmp/repo", "swarm/phase-999")
	if err != nil {
		t.Fatalf("DeleteBranch on a missing branch = %v, want nil", err)
	}
	// The whole point of the second return value: idempotent is not the same as
	// "deleted something", and a caller that cannot tell them apart reports a no-op
	// as a deletion.
	if existed {
		t.Error("existed = true on a missing branch, want false")
	}
	if g.called("branch -D") {
		t.Error("nothing to delete, yet a delete was issued")
	}
}

func TestDeleteBranchRefusesCheckedOut(t *testing.T) {
	g := baseStub()
	g.on("worktree list --porcelain",
		"worktree /wt/proj/phase-716\nbranch refs/heads/swarm/phase-716\n\n", nil)
	m := newMgr(t, g)
	_, err := m.DeleteBranch("/tmp/repo", "swarm/phase-716")
	if !errors.Is(err, ErrBranchCheckedOut) {
		t.Fatalf("err = %v, want ErrBranchCheckedOut", err)
	}
	if g.called("branch -D") {
		t.Error("DeleteBranch deleted a branch that is checked out")
	}
}

func TestDeleteBranchRefusesHead(t *testing.T) {
	g := baseStub()
	g.on("symbolic-ref --short HEAD", "swarm/phase-716\n", nil)
	m := newMgr(t, g)
	if _, err := m.DeleteBranch("/tmp/repo", "swarm/phase-716"); !errors.Is(err, ErrBranchIsHead) {
		t.Fatalf("err = %v, want ErrBranchIsHead", err)
	}
	if g.called("branch -D") {
		t.Error("DeleteBranch deleted the repo's HEAD branch")
	}
}

func TestDeleteBranchPropagatesGitError(t *testing.T) {
	g := baseStub()
	g.on("branch -D", "error: cannot delete", errors.New("exit 1"))
	m := newMgr(t, g)
	if _, err := m.DeleteBranch("/tmp/repo", "swarm/phase-1"); err == nil {
		t.Error("DeleteBranch should propagate a git branch -D failure")
	}
}

// ---- C1: a crash-leftover worktree at the run's OWN path -------------------

// ownLeftoverStub scripts the exact state a daemon crash leaves behind: the run's
// worktree is still REGISTERED at the deterministic path Acquire computes
// (<Root>/<slug>/<taskID>), still checked out on its own branch. `git worktree
// prune` cannot clear it because the directory still exists.
func ownLeftoverStub(t *testing.T, slug, taskID string) (*stubGit, *Manager, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "wts")
	own := filepath.Join(root, slug, taskID)
	g := baseStub()
	g.on("worktree list --porcelain",
		fmt.Sprintf("worktree %s\nbranch refs/heads/swarm/%s\n\n", own, taskID), nil)
	return g, &Manager{Git: g, Root: root}, own
}

// The regression this fixes: checkBranchReclaimable saw the branch checked out
// ANYWHERE — including at our own path — and returned ErrBranchCheckedOut, so
// phaserun.Start bailed before reaching the Acquire that recovers this exact state
// by warm reuse (invariant 4). Retrying a phase after a daemon crash was
// impossible.
func TestReclaimEmptyBranchToleratesOwnLeftoverWorktree(t *testing.T) {
	g, m, own := ownLeftoverStub(t, "proj", "phase-714")

	ahead, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-714")
	if err != nil {
		t.Fatalf("ReclaimEmptyBranch on our own leftover worktree = %v, want nil", err)
	}
	if ahead != 0 {
		t.Errorf("ahead = %d, want 0 — the checkout is ours, there is nothing to reclaim", ahead)
	}
	if g.called("branch -D") {
		t.Fatalf("a branch checked out in a live worktree must never be deleted (calls: %v)", g.calls)
	}
	// Reclaim stops at the list — counting commits would be pointless work, and
	// a non-zero count must NOT surface as BranchDirty: warm reuse continues the
	// work rather than destroying it.
	if g.called("rev-list") {
		t.Errorf("reclaim counted commits for a branch it will not touch (calls: %v)", g.calls)
	}

	// …and Start's next step, Acquire, recovers the run by warm-reusing that very
	// worktree.
	a, err := m.Acquire("/tmp/repo", "proj", "phase-714")
	if err != nil {
		t.Fatalf("Acquire after reclaim = %v, want warm reuse", err)
	}
	if !samePath(a.Path, own) || a.Branch != "swarm/phase-714" {
		t.Errorf("Acquire = {%s,%s}, want warm reuse of {%s,swarm/phase-714}", a.Path, a.Branch, own)
	}
	if g.called("worktree add") {
		t.Error("warm reuse must not re-add the worktree")
	}
}

// The branch checked out at a FOREIGN path is still a hard conflict — Acquire
// would reject it as ErrBranchBusy, and `git branch -D` refuses it anyway.
func TestReclaimEmptyBranchStillRefusesForeignWorktree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wts")
	g := baseStub()
	g.on("worktree list --porcelain",
		"worktree /elsewhere/phase-714\nbranch refs/heads/swarm/phase-714\n\n", nil)
	m := &Manager{Git: g, Root: root}
	_, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-714")
	if !errors.Is(err, ErrBranchCheckedOut) {
		t.Fatalf("err = %v, want ErrBranchCheckedOut for a foreign checkout", err)
	}
}

// A worktree under our Root but for a DIFFERENT task id is not ours either.
func TestReclaimEmptyBranchForeignTaskUnderRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wts")
	g := baseStub()
	g.on("worktree list --porcelain",
		"worktree "+filepath.Join(root, "proj", "phase-999")+"\nbranch refs/heads/swarm/phase-714\n\n", nil)
	m := &Manager{Git: g, Root: root}
	if _, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-714"); !errors.Is(err, ErrBranchCheckedOut) {
		t.Fatalf("err = %v, want ErrBranchCheckedOut", err)
	}
}

// DeleteBranch deliberately does NOT get the own-path tolerance: it ends in a
// `git branch -D`, which git itself refuses while the branch is checked out. A
// sentinel naming the holding worktree is a better answer than a raw git failure
// — and far better than the silent success I6 is about.
func TestDeleteBranchRefusesOwnLeftoverWorktree(t *testing.T) {
	g, m, own := ownLeftoverStub(t, "proj", "phase-714")
	_, err := m.DeleteBranch("/tmp/repo", "swarm/phase-714")
	if !errors.Is(err, ErrBranchCheckedOut) {
		t.Fatalf("err = %v, want ErrBranchCheckedOut", err)
	}
	if !strings.Contains(err.Error(), own) {
		t.Errorf("err = %v, want it to name the holding worktree %s", err, own)
	}
	if g.called("branch -D") {
		t.Error("DeleteBranch issued a delete git would refuse")
	}
}

// ---- I6: "missing" vs "git is unhappy" ------------------------------------

// A probe that FAILS WITH OUTPUT (unreadable repo, locked ref, bad permissions)
// is not proof the branch is gone. Reporting it as missing made DeleteBranch
// return nil, DeleteRunBranch log "deleted run branch", and the UI clear a
// dirty-branch banner on a no-op.
func TestDeleteBranchSurfacesProbeFailure(t *testing.T) {
	g := baseStub()
	g.on("rev-parse --verify --quiet",
		"fatal: not a git repository (or any of the parent directories): .git",
		errors.New("exit 128"))
	m := newMgr(t, g)
	_, err := m.DeleteBranch("/tmp/repo", "swarm/phase-714")
	if err == nil {
		t.Fatal("DeleteBranch = nil on a broken probe, want an error — the branch is still there")
	}
	if !strings.Contains(err.Error(), "swarm/phase-714") {
		t.Errorf("err = %v, want it to name the branch", err)
	}
	if g.called("branch -D") {
		t.Error("a failed probe must not authorize a delete")
	}
}

func TestReclaimEmptyBranchSurfacesProbeFailure(t *testing.T) {
	g := baseStub()
	g.on("rev-parse --verify --quiet", "fatal: not a git repository", errors.New("exit 128"))
	m := newMgr(t, g)
	if _, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-714"); err == nil {
		t.Fatal("ReclaimEmptyBranch = nil on a broken probe, want an error")
	}
}

// ---- M7: a negative commit count ------------------------------------------

// strconv.Atoi("-1") succeeds and `ahead > 0` is false, so a negative count used
// to authorize `branch -D` — the one outcome a nonsensical count must never buy.
func TestReclaimEmptyBranchNegativeCount(t *testing.T) {
	g := baseStub()
	g.on("rev-list --count", "-1\n", nil)
	m := newMgr(t, g)
	_, err := m.ReclaimEmptyBranch("/tmp/repo", "swarm/phase-717")
	if err == nil {
		t.Fatal("err = nil for a negative commit count, want an error")
	}
	if !strings.Contains(err.Error(), "swarm/phase-717") {
		t.Errorf("err = %v, want it to name the branch", err)
	}
	if g.called("branch -D") {
		t.Error("a negative count must not authorize a delete")
	}
}

func TestResolveRootDefaultsToHome(t *testing.T) {
	// Empty Root resolves under $HOME/.swarmery/worktrees.
	m := &Manager{Git: baseStub()}
	root, err := m.resolveRoot()
	if err != nil {
		t.Fatalf("resolveRoot: %v", err)
	}
	if !strings.HasSuffix(root, filepath.FromSlash(DefaultRoot)) {
		t.Errorf("default root = %q, want it to end with %q", root, DefaultRoot)
	}
	if !filepath.IsAbs(root) {
		t.Errorf("default root = %q, want absolute", root)
	}
}

// ---- porcelain parser ----------------------------------------------------

func TestParseWorktreeList(t *testing.T) {
	out := "worktree /a\nHEAD 1111\nbranch refs/heads/main\n\n" +
		"worktree /b\nHEAD 2222\ndetached\n\n" +
		"worktree /c\nHEAD 3333\nbranch refs/heads/swarm/T-9\n"
	e := parseWorktreeList(out)
	if len(e) != 3 {
		t.Fatalf("entries = %d, want 3", len(e))
	}
	if p, ok := e.pathForBranch("swarm/T-9"); !ok || p != "/c" {
		t.Errorf("pathForBranch(swarm/T-9) = %q,%v", p, ok)
	}
	if _, ok := e.pathForBranch("nope"); ok {
		t.Error("pathForBranch(nope) should be false")
	}
	if w, ok := e.byPath("/b"); !ok || w.branch != "" {
		t.Errorf("byPath(/b) detached entry = %+v,%v", w, ok)
	}
}

// ---- non-repo -------------------------------------------------------------

func TestAcquireNonRepo(t *testing.T) {
	g := &stubGit{}
	// symbolic-ref fails AND rev-parse HEAD fails → ErrNotARepo.
	g.on("symbolic-ref --short HEAD", "fatal: not a git repository", errors.New("exit 128"))
	g.on("rev-parse HEAD", "fatal: not a git repository", errors.New("exit 128"))
	m := newMgr(t, g)
	_, err := m.Acquire("/not/a/repo", "proj", "T-x")
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("err = %v, want ErrNotARepo", err)
	}
}
