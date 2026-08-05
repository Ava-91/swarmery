package prune

import (
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// The janitor's journal ages on the same cutoff as everything else, but it is
// NOT session-derived — so it must be trimmed even on a pass that finds no
// expiring sessions. That gate is exactly where it would otherwise be skipped.
func TestPrune_WorktreeSweepsAgeWithoutAnySessions(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "prune.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`INSERT INTO worktree_sweeps (ts, path, verdict, reason) VALUES
		('2026-01-01T00:00:00Z', '/old', 'redundant', 'old row'),
		('2026-09-01T00:00:00Z', '/new', 'skip', 'fresh row')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st, err := Run(db, "2026-06-01T00:00:00Z", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Sessions != 0 {
		t.Fatalf("fixture has %d expiring sessions; the point is to have none", st.Sessions)
	}
	if st.WorktreeSweeps != 1 {
		t.Errorf("WorktreeSweeps = %d, want 1", st.WorktreeSweeps)
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM worktree_sweeps`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Errorf("rows left = %d, want 1 (the fresh one)", left)
	}
}

// Dry-run reports what WOULD go and deletes nothing.
func TestPrune_WorktreeSweepsDryRunCountsOnly(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "prune-dry.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO worktree_sweeps (ts, path, verdict, reason)
		VALUES ('2026-01-01T00:00:00Z', '/old', 'redundant', 'old row')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st, err := Run(db, "2026-06-01T00:00:00Z", true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.WorktreeSweeps != 1 {
		t.Errorf("dry-run WorktreeSweeps = %d, want 1", st.WorktreeSweeps)
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM worktree_sweeps`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Errorf("dry-run deleted %d rows; it must delete none", 1-left)
	}
}
