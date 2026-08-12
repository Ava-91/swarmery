package store

import "testing"

// TestMigrate0052FreshDB verifies 0052 applies on a brand-new database: both
// revision tables exist, planning_sessions gains its revise-mode columns, and
// the migration is recorded.
func TestMigrate0052FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 52`).Scan(&name); err != nil {
		t.Fatalf("migration 52 not recorded: %v", err)
	}
	if name != "0052_plan_revisions.sql" {
		t.Errorf("migration 52 name: want 0052_plan_revisions.sql, got %s", name)
	}
	for _, table := range []string{"plan_revisions", "plan_revision_files"} {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
			t.Fatalf("look up table %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s missing after 0052", table)
		}
	}
	mustHaveColumns(t, db, "plan_revisions",
		"workspace_task_id", "plan_dir", "session_uuid", "status", "origin",
		"trigger_phase_id", "reason", "summary", "error", "created_at", "decided_at", "decided_by")
	mustHaveColumns(t, db, "plan_revision_files",
		"revision_id", "doc_path", "action", "rename_from", "base_hash",
		"proposed", "pre_image", "applied_hash")
	mustHaveColumns(t, db, "planning_sessions", "mode", "revise_task_id")
}

// TestMigrate0052OnPopulatedDB: a database created at version 51 migrates
// cleanly. A pre-0052 planning session gets mode='plan' (every session before
// revise mode existed was a plan-minting session) and a NULL revise_task_id.
func TestMigrate0052OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 51)

	if cols := columnSet(t, db, "planning_sessions"); cols["mode"] || cols["revise_task_id"] {
		t.Fatal("0052 columns exist before 0052 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, first_seen) VALUES (1, '/tmp/p', 'p', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO planning_sessions (id, project_id, session_uuid, status, idea, created_at, updated_at)
		 VALUES (1, 1, 'pre-0052-uuid', 'done', 'an idea', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert planning session: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}
	mustHaveColumns(t, db, "planning_sessions", "mode", "revise_task_id")

	var mode string
	var reviseTaskID any
	if err := db.QueryRow(
		`SELECT mode, revise_task_id FROM planning_sessions WHERE id = 1`).
		Scan(&mode, &reviseTaskID); err != nil {
		t.Fatalf("read migrated planning session: %v", err)
	}
	if mode != "plan" {
		t.Errorf("mode = %q, want \"plan\" (pre-revise sessions were all plan-minting)", mode)
	}
	if reviseTaskID != nil {
		t.Errorf("revise_task_id = %v, want NULL (a plan session targets no existing task)", reviseTaskID)
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
