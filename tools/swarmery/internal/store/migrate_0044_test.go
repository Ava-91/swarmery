package store

import "testing"

func TestMigrate0044FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 44`).Scan(&name); err != nil {
		t.Fatalf("migration 44 not recorded: %v", err)
	}
	if name != "0044_epic_phase_run_branch_index.sql" {
		t.Errorf("migration 44 name: want 0044_epic_phase_run_branch_index.sql, got %s", name)
	}
	mustHaveIndex(t, db, "idx_epic_phases_run_branch")
}
