package store

import "testing"

// TestMigrate0051FreshDB verifies 0051 applies on a brand-new database: both
// truth columns exist and the migration is recorded.
func TestMigrate0051FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 51`).Scan(&name); err != nil {
		t.Fatalf("migration 51 not recorded: %v", err)
	}
	if name != "0051_task_dispatch_truth.sql" {
		t.Errorf("migration 51 name: want 0051_task_dispatch_truth.sql, got %s", name)
	}
	mustHaveColumns(t, db, "tasks", "start_point", "verify_retry_count")
}

// TestMigrate0051OnPopulatedDB: a database created at version 50 migrates
// cleanly. A pre-0051 row gets a NULL start_point (no honest base exists for
// work already dispatched — consumers must fall back, never invent one) and a
// verify_retry_count of 0 (a full, unspent fix budget).
func TestMigrate0051OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 50)

	if cols := columnSet(t, db, "tasks"); cols["start_point"] || cols["verify_retry_count"] {
		t.Fatal("0051 columns exist before 0051 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, first_seen) VALUES (1, '/tmp/p', 'p', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	// A row that already burned dispatch retries: the split must not carry that
	// count over into the verify budget.
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, prompt, status, created_at, source, external_id,
		                    board_column, retry_count)
		 VALUES (1, 1, 'pre-0051 card', 'p', 'queued', '2026-08-01T00:00:00Z', 'queue', 'T-old511', 'todo', 2)`); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}
	mustHaveColumns(t, db, "tasks", "start_point", "verify_retry_count")

	var startPoint any
	var verifyRetry, dispatchRetry int
	if err := db.QueryRow(
		`SELECT start_point, verify_retry_count, retry_count FROM tasks WHERE id = 1`).
		Scan(&startPoint, &verifyRetry, &dispatchRetry); err != nil {
		t.Fatalf("read migrated task: %v", err)
	}
	if startPoint != nil {
		t.Errorf("start_point = %v, want NULL (no honest base for a pre-0051 row)", startPoint)
	}
	if verifyRetry != 0 {
		t.Errorf("verify_retry_count = %d, want 0 (a fresh, unspent verify budget)", verifyRetry)
	}
	if dispatchRetry != 2 {
		t.Errorf("retry_count = %d, want 2 (the dispatch budget is untouched by the split)", dispatchRetry)
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
