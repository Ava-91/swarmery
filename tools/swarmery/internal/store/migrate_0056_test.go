package store

import "testing"

// TestMigrate0056FreshDB verifies the rollback-reason column is registered.
func TestMigrate0056FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 56`).Scan(&name); err != nil {
		t.Fatalf("migration 56 not recorded: %v", err)
	}
	if name != "0056_planning_last_error.sql" {
		t.Errorf("migration 56 name: want 0056_planning_last_error.sql, got %s", name)
	}
	mustHaveColumns(t, db, "planning_sessions", "last_error")
}

// TestMigrate0056OnPopulatedDB: a pre-0056 wizard row survives and reads NULL —
// "no failure recorded", which is what every historical row means.
func TestMigrate0056OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 55)

	if columnSet(t, db, "planning_sessions")["last_error"] {
		t.Fatal("last_error exists before 0056 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, first_seen) VALUES (1, '/tmp/p', 'p', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO planning_sessions (id, project_id, session_uuid, status, idea, created_at, updated_at)
		 VALUES (1, 1, 'uuid-pre-0056', 'awaiting_answer', 'an idea', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert planning session: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}
	mustHaveColumns(t, db, "planning_sessions", "last_error")

	var lastError *string
	if err := db.QueryRow(
		`SELECT last_error FROM planning_sessions WHERE id = 1`).Scan(&lastError); err != nil {
		t.Fatalf("read last_error: %v", err)
	}
	if lastError != nil {
		t.Errorf("last_error = %q, want NULL for a pre-0056 row", *lastError)
	}
}
