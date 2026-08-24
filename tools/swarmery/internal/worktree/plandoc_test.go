package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLendPlanDocPutsItInsideTheWorktree(t *testing.T) {
	wt := t.TempDir()
	doc := filepath.Join(t.TempDir(), "phase-1-thing.md")
	writeFile(t, doc, "# Phase 1\n\n- [ ] a criterion\n")

	rel, err := LendPlanDoc(wt, doc)
	if err != nil {
		t.Fatalf("lend: %v", err)
	}
	if rel != filepath.Join(PlanDocDir, "phase-1-thing.md") {
		t.Errorf("rel = %q, want it under %s", rel, PlanDocDir)
	}
	if filepath.IsAbs(rel) {
		t.Error("the lent path must be RELATIVE — an absolute path is what the sandbox refuses")
	}
	if got := readFile(t, filepath.Join(wt, rel)); !strings.Contains(got, "a criterion") {
		t.Errorf("the lent copy does not carry the document: %q", got)
	}
}

func TestLendPlanDocWithNothingToLend(t *testing.T) {
	wt := t.TempDir()
	if rel, err := LendPlanDoc(wt, ""); rel != "" || err != nil {
		t.Errorf("no doc = (%q, %v), want (\"\", nil) — a card without a plan is normal", rel, err)
	}
	if rel, err := LendPlanDoc("", "/x/doc.md"); rel != "" || err != nil {
		t.Errorf("no worktree = (%q, %v), want (\"\", nil)", rel, err)
	}
	if _, err := LendPlanDoc(wt, filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Error("a missing source document should report an error, not lend silently")
	}
}

// The whole reason lending is safe: an edit made in the worktree reaches the
// workspace document the dashboard reads.
func TestReturnPlanDocCarriesTheReportBack(t *testing.T) {
	wt := t.TempDir()
	doc := filepath.Join(t.TempDir(), "phase-1-thing.md")
	writeFile(t, doc, "# Phase 1\n\n- [ ] a criterion\n\n## Completion Report\n")

	rel, err := LendPlanDoc(wt, doc)
	if err != nil {
		t.Fatal(err)
	}
	// The agent ticks a box and writes its report into the LENT copy.
	edited := "# Phase 1\n\n- [x] a criterion\n\n## Completion Report\n\nShipped the thing.\n"
	writeFile(t, filepath.Join(wt, rel), edited)

	wrote, err := ReturnPlanDoc(wt, rel, doc)
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if !wrote {
		t.Fatal("an edited document was not returned")
	}
	got := readFile(t, doc)
	if !strings.Contains(got, "Shipped the thing.") {
		t.Errorf("the Completion Report did not reach the workspace doc:\n%s", got)
	}
	if !strings.Contains(got, "- [x] a criterion") {
		t.Errorf("the ticked checkbox did not reach the workspace doc:\n%s", got)
	}
}

// Returning an untouched file would rewrite the workspace doc's mtime on every
// run and make "was this phase touched?" unanswerable from the filesystem.
func TestReturnPlanDocLeavesAnUntouchedDocAlone(t *testing.T) {
	wt := t.TempDir()
	doc := filepath.Join(t.TempDir(), "phase-1-thing.md")
	writeFile(t, doc, "# Phase 1\n")
	rel, err := LendPlanDoc(wt, doc)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(doc)
	if err != nil {
		t.Fatal(err)
	}
	wrote, err := ReturnPlanDoc(wt, rel, doc)
	if err != nil || wrote {
		t.Fatalf("return of an untouched doc = (%v, %v), want (false, nil)", wrote, err)
	}
	after, err := os.Stat(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an untouched document was rewritten anyway")
	}
}

// An empty copy is damage, not an edit — the workspace document is the only
// record of the work and must not be destroyed by one.
func TestReturnPlanDocRefusesToOverwriteWithAnEmptyCopy(t *testing.T) {
	wt := t.TempDir()
	doc := filepath.Join(t.TempDir(), "phase-1-thing.md")
	writeFile(t, doc, "# Phase 1\n\nreal content\n")
	rel, err := LendPlanDoc(wt, doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wt, rel), "   \n\n")

	wrote, err := ReturnPlanDoc(wt, rel, doc)
	if wrote {
		t.Error("an empty copy overwrote the workspace document")
	}
	if err == nil {
		t.Error("an empty copy should be reported, not silently ignored")
	}
	if got := readFile(t, doc); !strings.Contains(got, "real content") {
		t.Errorf("the workspace document lost its content:\n%s", got)
	}
}

// A deleted or already-torn-down copy is nothing to return, and nothing to
// complain about — this runs on the failure path too, where the worktree may
// already be gone.
func TestReturnPlanDocToleratesAMissingCopy(t *testing.T) {
	wt := t.TempDir()
	doc := filepath.Join(t.TempDir(), "phase-1-thing.md")
	writeFile(t, doc, "# Phase 1\n")
	wrote, err := ReturnPlanDoc(wt, filepath.Join(PlanDocDir, "phase-1-thing.md"), doc)
	if wrote || err != nil {
		t.Errorf("missing copy = (%v, %v), want (false, nil)", wrote, err)
	}
	if wrote, err := ReturnPlanDoc(wt, "", doc); wrote || err != nil {
		t.Errorf("no rel path = (%v, %v), want (false, nil)", wrote, err)
	}
}
