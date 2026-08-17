package wsingest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/taskdir"
)

// TestScanIndexesAMintedMicroPlan is the compat anchor between the two packages
// that must agree about what a task dir looks like: internal/taskdir writes one,
// internal/wsingest parses one, and nothing else connects them.
//
// It runs the REAL scanner over a REAL minted tree rather than calling
// parsePlanTable directly. The parser is only one of the contracts a generated tree
// has to satisfy — the dir-name date, the derived external_id, the card README's
// title and status, the plan/ dir making the task an epic, and the phase doc's
// checkbox count are the rest, and a unit test of the table alone would pass while
// any of those was broken.
func TestScanIndexesAMintedMicroPlan(t *testing.T) {
	db := testDB(t)
	root := t.TempDir()
	mustExec(t, db, `INSERT INTO projects (id, path, slug, name, first_seen)
		VALUES (1, '/work/projalpha', 'projalpha', 'Proj Alpha', '2026-06-01T00:00:00Z')`)

	minted, err := taskdir.MintMicroPlan(root, "projalpha", taskdir.Card{
		ExternalID: "T-42",
		Title:      "Fix the janitor sweep",
		Prompt:     "The janitor sweeps foreign dirs.\nMake it skip dirs it does not own.",
		RepoPath:   "/work/projalpha",
	}, time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("MintMicroPlan: %v", err)
	}

	stats, err := New(db, Config{WorkspaceRoot: root}).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stats.EpicPhases != 1 {
		t.Errorf("epic phases = %d, want 1", stats.EpicPhases)
	}

	// The external_id the scanner derives from the path — the value everything else
	// joins on.
	var (
		taskID int64
		title  string
		status string
	)
	if err := db.QueryRow(`SELECT id, title, status FROM tasks
		 WHERE source='workspace' AND external_id='2026-08-17-card-t-42'`).Scan(&taskID, &title, &status); err != nil {
		t.Fatalf("the minted micro-plan was not indexed: %v", err)
	}
	if title != "Fix the janitor sweep" {
		t.Errorf("title = %q, want the card's", title)
	}
	if status != "running" {
		t.Errorf("status = %q, want running (from `- **Status**: active`)", status)
	}

	// The plan/ dir is what makes it an epic; the phase row is what the Plans page
	// renders, and its checkbox total is the honesty contract in numbers.
	var (
		seq     int
		docPath string
		total   int
		done    int
	)
	if err := db.QueryRow(`SELECT seq, doc_path, checkboxes_total, checkboxes_done
		  FROM epic_phases WHERE workspace_task_id = ?`, taskID).Scan(&seq, &docPath, &total, &done); err != nil {
		t.Fatalf("no phase row for the micro-plan: %v", err)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}
	if want := filepath.Join(minted, "plan", taskdir.PhaseDocName); docPath != want {
		t.Errorf("doc_path = %q, want %q", docPath, want)
	}
	if total != 2 || done != 0 {
		t.Errorf("checkboxes = %d/%d, want 0/2", done, total)
	}
}

// A tick in the generated doc has to move the same rollup a hand-written plan's
// does — otherwise the micro-plan looks like a plan but does not behave like one.
func TestScanCountsTicksInAMintedMicroPlan(t *testing.T) {
	db := testDB(t)
	root := t.TempDir()
	mustExec(t, db, `INSERT INTO projects (id, path, slug, name, first_seen)
		VALUES (1, '/work/projalpha', 'projalpha', 'Proj Alpha', '2026-06-01T00:00:00Z')`)
	minted, err := taskdir.MintMicroPlan(root, "projalpha", taskdir.Card{
		ExternalID: "T-42", Title: "t", Prompt: "p",
	}, time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db, Config{WorkspaceRoot: root})
	if _, err := scanner.Scan(); err != nil {
		t.Fatal(err)
	}

	// The executor ticks the first criterion, as its contract instructs.
	doc := filepath.Join(minted, "plan", taskdir.PhaseDocName)
	raw := mustRead(t, doc)
	mustWriteFile(t, doc, replaceFirst(raw, "- [ ] ", "- [x] "))

	if _, err := scanner.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT checkboxes_done FROM epic_phases`); got != 1 {
		t.Errorf("checkboxes_done = %d, want 1 after the executor's tick", got)
	}
}
