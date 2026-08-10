package api

// Tests for the board review diff endpoint (tasks_diff.go).
//
// These run against REAL temp git repos rather than a scripted runner: the
// endpoint's whole job is to translate git's output, so a fake that returns what
// this package expects would only assert that the parsers agree with themselves.
// The one thing that IS faked lives in tasks_review_test.go (push/gh), because
// those talk to a network the test must not.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// gitFixture runs a git command in dir, failing the test on a non-zero exit.
// The environment is pinned so the developer's own gitconfig (signing keys, a
// different default branch, hooks) cannot decide whether the suite passes.
func gitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=swarmery-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=swarmery-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// reviewRepo builds a throwaway repo shaped like a dispatched card's: a base
// commit on main, then a run branch carrying two commits across two files. It
// finishes on main so the run branch is NOT checked out — the shape the daemon
// actually sees, since the worktree that produced the branch is reclaimed while
// the branch survives.
func reviewRepo(t *testing.T, branch string) (repo, base string) {
	t.Helper()
	repo = t.TempDir()
	gitFixture(t, repo, "init", "-q", "-b", "main")
	writeRepoFile(t, repo, "README.md", "base\n")
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-qm", "base commit")
	base = strings.TrimSpace(gitFixture(t, repo, "rev-parse", "HEAD"))

	gitFixture(t, repo, "checkout", "-q", "-b", branch)
	writeRepoFile(t, repo, "alpha.txt", "one\ntwo\n")
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-qm", "add alpha")
	writeRepoFile(t, repo, "alpha.txt", "one\ntwo\nthree\n")
	writeRepoFile(t, repo, "beta.txt", "b1\n")
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-qm", "extend alpha, add beta")
	gitFixture(t, repo, "checkout", "-q", "main")
	return repo, base
}

// reviewCard is the dispatcher-owned state a seeded board row carries.
type reviewCard struct {
	Branch      string
	StartPoint  string
	Worktree    string
	BoardColumn string
	Status      string
	Prompt      string
	Verdict     string
	Detail      string
}

// reviewServer wires an httptest server whose project 1 points at repo, with the
// dispatcher DETACHED (restored on cleanup) so nothing in these tests can spawn.
// Package vars leak between tests in this package; saving and restoring is what
// keeps that from being an ordering dependency.
func reviewServer(t *testing.T, repo string) (*httptest.Server, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "review_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(
		`INSERT INTO projects(id, path, slug, first_seen) VALUES(1,?,'p','2026-01-01T00:00:00Z')`,
		repo); err != nil {
		t.Fatal(err)
	}
	prev := dispatchSvc
	dispatchSvc = nil
	t.Cleanup(func() { dispatchSvc = prev })

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db
}

// seedReviewCard inserts a board card in the state a dispatched task reaches.
func seedReviewCard(t *testing.T, db *sql.DB, extID string, c reviewCard) int64 {
	t.Helper()
	col := c.BoardColumn
	if col == "" {
		col = "in_review"
	}
	status := c.Status
	if status == "" {
		status = "needs_review"
	}
	prompt := c.Prompt
	if prompt == "" {
		prompt = "do the thing"
	}
	res, err := db.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, branch, start_point,
		                   worktree_path, verify_verdict, verify_detail,
		                   file_scope, labels, dependencies, origin)
		VALUES (1, 'review me', ?, 5, ?, '2026-01-01T00:00:00.000Z',
		        'queue', ?, ?, ?, ?, ?, ?, ?, '[]', '[]', '[]', 'manual')`,
		prompt, status, extID, col,
		nullableStr(c.Branch), nullableStr(c.StartPoint), nullableStr(c.Worktree),
		nullableStr(c.Verdict), nullableStr(c.Detail))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// getDiff issues the diff request and returns the response plus its decoded body.
func getDiff(t *testing.T, srvURL string, id int64) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/api/board/tasks/%d/diff", srvURL, id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode diff body: %v", err)
	}
	return resp, body
}

func TestTaskDiffHappyPath(t *testing.T) {
	const branch = "swarm/T-abc123"
	repo, base := reviewRepo(t, branch)
	srv, db := reviewServer(t, repo)
	id := seedReviewCard(t, db, "T-abc123", reviewCard{Branch: branch, StartPoint: base})

	resp, _ := getDiff(t, srv.URL, id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// Decode into the DTO for the typed assertions.
	resp2, err := http.Get(fmt.Sprintf("%s/api/board/tasks/%d/diff", srv.URL, id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var d taskDiffDTO
	if err := json.NewDecoder(resp2.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}

	if d.Base != base || d.Branch != branch {
		t.Errorf("base/branch = %q/%q, want %q/%q", d.Base, d.Branch, base, branch)
	}
	if len(d.Commits) != 2 {
		t.Fatalf("commits = %d (%+v), want 2", len(d.Commits), d.Commits)
	}
	// git log lists newest first.
	if d.Commits[0].Subject != "extend alpha, add beta" || d.Commits[1].Subject != "add alpha" {
		t.Errorf("commit subjects = %q, %q", d.Commits[0].Subject, d.Commits[1].Subject)
	}
	if len(d.Commits[0].SHA) != 40 {
		t.Errorf("sha = %q, want a full 40-char hash", d.Commits[0].SHA)
	}
	if len(d.Files) != 2 {
		t.Fatalf("files = %+v, want 2", d.Files)
	}
	byPath := map[string]reviewFileDTO{}
	for _, f := range d.Files {
		byPath[f.Path] = f
	}
	if got := byPath["alpha.txt"]; got.Additions != 3 || got.Deletions != 0 {
		t.Errorf("alpha.txt = +%d/-%d, want +3/-0", got.Additions, got.Deletions)
	}
	if got := byPath["beta.txt"]; got.Additions != 1 {
		t.Errorf("beta.txt = +%d, want +1", got.Additions)
	}
	if !strings.Contains(d.Patch, "diff --git") || !strings.Contains(d.Patch, "beta.txt") {
		t.Errorf("patch missing expected content:\n%s", d.Patch)
	}
	// README is untouched by the branch — a `...` diff must not drag it in.
	if strings.Contains(d.Patch, "README.md") {
		t.Errorf("patch contains an unchanged file:\n%s", d.Patch)
	}
	if d.PatchTruncated {
		t.Error("patchTruncated = true for a tiny diff")
	}
}

func TestTaskDiffNoStartPointIs409(t *testing.T) {
	const branch = "swarm/T-nobase"
	repo, _ := reviewRepo(t, branch)
	srv, db := reviewServer(t, repo)
	// A legacy card: dispatched (it has a branch) before 0051 pinned the base.
	id := seedReviewCard(t, db, "T-nobase", reviewCard{Branch: branch})

	resp, body := getDiff(t, srv.URL, id)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if body["code"] != codeNoStartPoint {
		t.Errorf("code = %v, want %q", body["code"], codeNoStartPoint)
	}
}

func TestTaskDiffNoBranchIs409(t *testing.T) {
	repo, base := reviewRepo(t, "swarm/T-unused")
	srv, db := reviewServer(t, repo)
	// Never dispatched: no branch at all. Distinct code from the legacy card
	// above — "dispatch it" vs "re-run it so a base gets pinned".
	id := seedReviewCard(t, db, "T-nobranch", reviewCard{StartPoint: base, BoardColumn: "triage"})

	resp, body := getDiff(t, srv.URL, id)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if body["code"] != codeNoRunBranch {
		t.Errorf("code = %v, want %q", body["code"], codeNoRunBranch)
	}
}

func TestTaskDiffDeletedBranchIs404(t *testing.T) {
	const branch = "swarm/T-gone77"
	repo, base := reviewRepo(t, branch)
	srv, db := reviewServer(t, repo)
	id := seedReviewCard(t, db, "T-gone77", reviewCard{Branch: branch, StartPoint: base})

	// Someone deleted the branch outside the daemon.
	gitFixture(t, repo, "branch", "-D", branch)

	resp, body := getDiff(t, srv.URL, id)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, branch) {
		t.Errorf("error %q does not name the missing branch", msg)
	}
	// The body must carry git's own words, not just ours — that is what tells the
	// user the ref is gone rather than the endpoint being broken.
	if !strings.Contains(msg, "unknown revision") && !strings.Contains(msg, "fatal:") {
		t.Errorf("error %q carries no git stderr", msg)
	}
}

func TestTaskDiffUnreachableBaseIs409(t *testing.T) {
	const branch = "swarm/T-orphan"
	repo, _ := reviewRepo(t, branch)
	srv, db := reviewServer(t, repo)
	// A start point this repo has never heard of (a force-push / gc away).
	id := seedReviewCard(t, db, "T-orphan", reviewCard{
		Branch: branch, StartPoint: "0000000000000000000000000000000000000042",
	})

	resp, body := getDiff(t, srv.URL, id)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if body["code"] != codeBaseUnreachable {
		t.Errorf("code = %v, want %q", body["code"], codeBaseUnreachable)
	}
}

func TestTaskDiffUnknownTaskIs404(t *testing.T) {
	repo, _ := reviewRepo(t, "swarm/T-x")
	srv, _ := reviewServer(t, repo)
	resp, _ := getDiff(t, srv.URL, 9999)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestTaskDiffTruncatesAt200KB(t *testing.T) {
	const branch = "swarm/T-bigdif"
	repo, base := reviewRepo(t, branch)
	// Add a commit whose diff comfortably exceeds the cap.
	gitFixture(t, repo, "checkout", "-q", branch)
	var big strings.Builder
	for i := 0; big.Len() < reviewMaxPatchBytes+50_000; i++ {
		fmt.Fprintf(&big, "line %06d: the quick brown fox jumps over the lazy dog\n", i)
	}
	writeRepoFile(t, repo, "huge.txt", big.String())
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-qm", "add huge file")
	gitFixture(t, repo, "checkout", "-q", "main")

	srv, db := reviewServer(t, repo)
	id := seedReviewCard(t, db, "T-bigdif", reviewCard{Branch: branch, StartPoint: base})

	resp, err := http.Get(fmt.Sprintf("%s/api/board/tasks/%d/diff", srv.URL, id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var d taskDiffDTO
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if !d.PatchTruncated {
		t.Fatalf("patchTruncated = false for a %d-byte patch", len(d.Patch))
	}
	if len(d.Patch) > reviewMaxPatchBytes {
		t.Errorf("patch = %d bytes, want <= %d", len(d.Patch), reviewMaxPatchBytes)
	}
	if !strings.HasSuffix(d.Patch, "\n") {
		t.Error("truncated patch does not end on a line boundary")
	}
	// The file table is computed from numstat, not from the patch, so truncation
	// must not cost the reviewer the summary of what changed.
	if len(d.Files) != 3 {
		t.Errorf("files = %+v, want 3 even with a truncated patch", d.Files)
	}
}

func TestParseReviewCommits(t *testing.T) {
	in := "abc\x00subject one\ndef\x00subject: with | odd \t chars\n\n"
	got := parseReviewCommits(in)
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}
	if got[1].SHA != "def" || got[1].Subject != "subject: with | odd \t chars" {
		t.Errorf("second commit = %+v", got[1])
	}
	if c := parseReviewCommits(""); c == nil || len(c) != 0 {
		t.Errorf("empty log = %v, want an empty (non-nil) slice", c)
	}
}

func TestParseNumstat(t *testing.T) {
	got := parseNumstat("3\t1\tsrc/a.go\n-\t-\tassets/logo.png\ngarbage\n")
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 entries", got)
	}
	if got[0] != (reviewFileDTO{Path: "src/a.go", Additions: 3, Deletions: 1}) {
		t.Errorf("text file = %+v", got[0])
	}
	// A binary file has no line counts; the path is still what the table needs.
	if got[1] != (reviewFileDTO{Path: "assets/logo.png"}) {
		t.Errorf("binary file = %+v", got[1])
	}
	if f := parseNumstat(""); f == nil || len(f) != 0 {
		t.Errorf("empty numstat = %v, want an empty (non-nil) slice", f)
	}
}

func TestTruncatePatch(t *testing.T) {
	if s, cut := truncatePatch("short", 100); s != "short" || cut {
		t.Errorf("under cap = %q/%v", s, cut)
	}
	s, cut := truncatePatch("aaaa\nbbbb\ncccc\n", 12)
	if !cut {
		t.Fatal("over cap did not report truncation")
	}
	if s != "aaaa\nbbbb\n" {
		t.Errorf("truncated = %q, want a whole-line prefix", s)
	}
	// No newline inside the cap: fall back to a hard cut rather than returning "".
	s, cut = truncatePatch(strings.Repeat("x", 50), 10)
	if !cut || len(s) != 10 {
		t.Errorf("newline-free truncation = %q/%v", s, cut)
	}
}
