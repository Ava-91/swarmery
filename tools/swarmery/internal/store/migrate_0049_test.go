package store

import "testing"

// TestMigrate0049FreshDB verifies 0049 applies on a brand-new database: the
// tasks.labels column exists and is recorded in schema_migrations.
func TestMigrate0049FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 49`).Scan(&name); err != nil {
		t.Fatalf("migration 49 not recorded: %v", err)
	}
	if name != "0049_task_labels.sql" {
		t.Errorf("migration 49 name: want 0049_task_labels.sql, got %s", name)
	}
	mustHaveColumns(t, db, "tasks", "labels")
}

// TestMigrate0049OnPopulatedDB: a database created before 0049 gets the
// labels column added, and every pre-existing row backfills to '[]' — a card
// that existed before labels shipped renders with no labels, not a NULL.
func TestMigrate0049OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 48)

	if cols := columnSet(t, db, "tasks"); cols["labels"] {
		t.Fatal("tasks.labels exists before 0049 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, first_seen) VALUES (1, '/tmp/p', 'p', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, prompt, status, created_at, source, external_id, board_column)
		 VALUES (1, 1, 'pre-0049 card', 'p', 'queued', '2026-08-01T00:00:00Z', 'queue', 'T-old111', 'todo')`); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}
	mustHaveColumns(t, db, "tasks", "labels")

	var labels string
	if err := db.QueryRow(`SELECT labels FROM tasks WHERE id = 1`).Scan(&labels); err != nil {
		t.Fatalf("read migrated task: %v", err)
	}
	if labels != "[]" {
		t.Errorf("labels = %q, want '[]' (pre-0049 rows have no labels)", labels)
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
