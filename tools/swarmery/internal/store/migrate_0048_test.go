package store

import (
	"strings"
	"testing"
)

func TestMigrate0048FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 48`).Scan(&name); err != nil {
		t.Fatalf("migration 48 not recorded: %v", err)
	}
	if name != "0048_task_capture.sql" {
		t.Errorf("migration 48 name: want 0048_task_capture.sql, got %s", name)
	}
	mustHaveColumns(t, db, "tasks", "agent", "origin", "origin_session_id", "capture_key")

	// The partial unique index is the whole point of the migration: it is what
	// makes a capture replay a no-op instead of a duplicate card.
	var idxSQL string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_tasks_capture_key'`,
	).Scan(&idxSQL); err != nil {
		t.Fatalf("idx_tasks_capture_key missing: %v", err)
	}
	if !containsAll(idxSQL, "UNIQUE", "capture_key", "WHERE") {
		t.Errorf("idx_tasks_capture_key is not a partial unique index: %s", idxSQL)
	}
}

// TestMigrate0048OnPopulatedDB: the columns land on a database that already
// holds board tasks, and every pre-existing row reads origin='manual' with no
// capture identity — no backfill, and no reader ever sees a NULL origin.
func TestMigrate0048OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 47)

	if cols := columnSet(t, db, "tasks"); cols["origin"] {
		t.Fatal("tasks.origin exists before 0048 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, first_seen) VALUES (1, '/tmp/p', 'p', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, prompt, status, created_at, source, external_id, board_column)
		 VALUES (1, 1, 'pre-0048 card', 'p', 'queued', '2026-08-01T00:00:00Z', 'queue', 'T-old111', 'todo')`); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}
	mustHaveColumns(t, db, "tasks", "agent", "origin", "origin_session_id", "capture_key")

	var origin string
	var agent, captureKey, originSession any
	if err := db.QueryRow(
		`SELECT origin, agent, capture_key, origin_session_id FROM tasks WHERE id = 1`,
	).Scan(&origin, &agent, &captureKey, &originSession); err != nil {
		t.Fatalf("read migrated task: %v", err)
	}
	if origin != "manual" {
		t.Errorf("origin = %q, want 'manual' (every pre-capture card is hand-written)", origin)
	}
	if agent != nil || captureKey != nil || originSession != nil {
		t.Errorf("pre-0048 row got non-NULL capture identity: agent=%v key=%v session=%v",
			agent, captureKey, originSession)
	}

	// The partial index leaves NULL capture_key rows out entirely, so any number
	// of manual cards coexist — only a repeated non-NULL key collides.
	if _, err := db.Exec(
		`INSERT INTO tasks (project_id, title, prompt, status, created_at, source, external_id)
		 VALUES (1, 'another manual', 'p', 'queued', '2026-08-01T00:01:00Z', 'queue', 'T-old222')`); err != nil {
		t.Fatalf("second manual card rejected by the partial index: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (project_id, title, prompt, status, created_at, source, external_id, capture_key)
		 VALUES (1, 'captured', 'p', 'queued', '2026-08-01T00:02:00Z', 'queue', 'T-cap111', 'todo:u:h')`); err != nil {
		t.Fatalf("first captured card: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (project_id, title, prompt, status, created_at, source, external_id, capture_key)
		 VALUES (1, 'captured dup', 'p', 'queued', '2026-08-01T00:03:00Z', 'queue', 'T-cap222', 'todo:u:h')`); err == nil {
		t.Error("duplicate capture_key was accepted — the unique index is not enforcing")
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}

// containsAll reports whether s contains every substring in want.
func containsAll(s string, want ...string) bool {
	for _, w := range want {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
