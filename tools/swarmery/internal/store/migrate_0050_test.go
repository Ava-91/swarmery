package store

import "testing"

func TestMigrate0050FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 50`).Scan(&name); err != nil {
		t.Fatalf("migration 50 not recorded: %v", err)
	}
	if name != "0050_worktree_sweeps.sql" {
		t.Errorf("migration 50 name: want 0050_worktree_sweeps.sql, got %s", name)
	}
	mustHaveColumns(t, db, "worktree_sweeps",
		"ts", "project_id", "path", "branch", "verdict", "reason",
		"salvage_branch", "files", "removed", "error")

	// The ts index is what keeps the dashboard's newest-first query off a full
	// scan once the journal has a few thousand rows.
	var idxSQL string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_worktree_sweeps_ts'`,
	).Scan(&idxSQL); err != nil {
		t.Fatalf("idx_worktree_sweeps_ts missing: %v", err)
	}
}
