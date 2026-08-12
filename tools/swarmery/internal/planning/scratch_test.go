package planning

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestSweepScratchOrphans covers the predicate over the full status set plus
// the no-row and no-root cases.
func TestSweepScratchOrphans(t *testing.T) {
	db := testDB(t)
	root := filepath.Join(t.TempDir(), "revisions")

	// One scratch dir per scenario; the dir name IS the session uuid.
	scenarios := map[string]struct {
		status string // "" = no planning_sessions row at all
		orphan bool
	}{
		"uuid-no-row":     {"", true},
		"uuid-generating": {StatusGenerating, false},
		"uuid-awaiting":   {StatusAwaiting, false},
		"uuid-proceeding": {StatusProceeding, false},
		"uuid-done":       {StatusDone, true},
		"uuid-failed":     {StatusFailed, true},
		"uuid-cancelled":  {StatusCancelled, true},
	}
	var wantRemoved []string
	for uuid, sc := range scenarios {
		if err := os.MkdirAll(filepath.Join(root, uuid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, uuid, "revision.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if sc.status != "" {
			if _, err := db.Exec(
				`INSERT INTO planning_sessions(project_id, session_uuid, status, idea, mode, created_at, updated_at)
				 VALUES(1, ?, ?, 'i', 'revise', '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z')`,
				uuid, sc.status); err != nil {
				t.Fatal(err)
			}
		}
		if sc.orphan {
			wantRemoved = append(wantRemoved, uuid)
		}
	}
	// A stray FILE at the root is never touched (only dirs are scratch dirs).
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepScratchOrphans(db, root)
	if err != nil {
		t.Fatalf("SweepScratchOrphans: %v", err)
	}
	sort.Strings(removed)
	sort.Strings(wantRemoved)
	if len(removed) != len(wantRemoved) {
		t.Fatalf("removed %v, want %v", removed, wantRemoved)
	}
	for i := range removed {
		if removed[i] != wantRemoved[i] {
			t.Fatalf("removed %v, want %v", removed, wantRemoved)
		}
	}
	for uuid, sc := range scenarios {
		_, err := os.Stat(filepath.Join(root, uuid))
		if sc.orphan && !os.IsNotExist(err) {
			t.Errorf("orphan %s survived the sweep (err=%v)", uuid, err)
		}
		if !sc.orphan && err != nil {
			t.Errorf("live session %s lost its scratch dir: %v", uuid, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "stray.txt")); err != nil {
		t.Errorf("sweep touched a non-dir entry: %v", err)
	}

	// Missing root: clean no-op.
	if removed, err := SweepScratchOrphans(db, filepath.Join(root, "does-not-exist")); err != nil || removed != nil {
		t.Fatalf("missing root: removed=%v err=%v, want nil/nil", removed, err)
	}
}
