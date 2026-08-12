package store

import "testing"

func TestMigrate0047FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 47`).Scan(&name); err != nil {
		t.Fatalf("migration 47 not recorded: %v", err)
	}
	if name != "0047_session_account.sql" {
		t.Errorf("migration 47 name: want 0047_session_account.sql, got %s", name)
	}
	mustHaveColumns(t, db, "sessions", "account")
}

// TestMigrate0047OnPopulatedDB: the column lands on a database already holding
// pre-0047 sessions, and every one of them reads ” — the "unknown / stock
// account" value the ingest fill-only-when-empty rule keys off. NOT NULL with a
// DEFAULT means no reader ever has to handle a NULL account.
func TestMigrate0047OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 46)

	if cols := columnSet(t, db, "sessions"); cols["account"] {
		t.Fatal("sessions.account exists before 0047 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, first_seen) VALUES (1, '/tmp/p', 'p', '2026-07-31T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (project_id, session_uuid, status, started_at, source)
		 VALUES (1, 'u-pre-0047', 'completed', '2026-07-31T00:00:00Z', 'jsonl')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}
	mustHaveColumns(t, db, "sessions", "account")

	var account string
	if err := db.QueryRow(
		`SELECT account FROM sessions WHERE session_uuid = 'u-pre-0047'`).Scan(&account); err != nil {
		t.Fatalf("read migrated session: %v", err)
	}
	if account != "" {
		t.Errorf("account = %q, want '' (unknown / stock account)", account)
	}

	// A row inserted without the column keeps the same default — the hooks
	// channel (internal/approvals) writes exactly this shape.
	if _, err := db.Exec(
		`INSERT INTO sessions (project_id, session_uuid, cwd, status, started_at, source)
		 VALUES (1, 'u-hook', '/tmp/p', 'active', '2026-07-31T01:00:00Z', 'hook')`); err != nil {
		t.Fatalf("insert hook session: %v", err)
	}
	if err := db.QueryRow(
		`SELECT account FROM sessions WHERE session_uuid = 'u-hook'`).Scan(&account); err != nil {
		t.Fatalf("read hook session: %v", err)
	}
	if account != "" {
		t.Errorf("hook session account = %q, want ''", account)
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
