package store

import (
	"os"
	"testing"
)

// TestMigrate0055FreshDB pins that the phantom-session cleanup is registered.
func TestMigrate0055FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 55`).Scan(&name); err != nil {
		t.Fatalf("migration 55 not recorded: %v", err)
	}
	if name != "0055_drop_phantom_sessions.sql" {
		t.Errorf("migration 55 name: want 0055_drop_phantom_sessions.sql, got %s", name)
	}
}

// TestMigrate0055DropsOnlyPhantoms replays the cleanup against rows planted
// AFTER the schema is current (the migration is already applied by then, so
// the statements are re-run from the same file the runner embeds) and pins
// the predicate: a timestamp-less, content-less, unreferenced session goes,
// everything else stays — including a timestamp-less row that has a single
// event attached, and every row that ever learned a started_at.
func TestMigrate0055DropsOnlyPhantoms(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}

	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES
		(1, '(unknown)', '(unknown)', '(unknown)', ''),
		(2, '/work/p', '-work-p', 'P', '2026-08-01T00:00:00Z')`)
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at, ended_at, cwd, pid) VALUES
		(1, 1, 'phantom-1',  'active',    '', NULL,                     '',          NULL),
		(2, 1, 'phantom-2',  'completed', '', '2026-08-14T09:32:31Z',    '(unknown)', NULL),
		(3, 1, 'has-event',  'active',    '', NULL,                     '',          NULL),
		(4, 2, 'real',       'completed', '2026-08-01T10:00:00.000Z', '2026-08-01T10:30:00.000Z', '/work/p', NULL),
		(5, 2, 'hook-live',  'active',    '', NULL,                     '/work/p',   NULL),
		(6, 1, 'has-pid',    'active',    '', NULL,                     '',          4242)`)
	// Session 3 carries content — no timestamp, but it is not a phantom.
	mustExec(`INSERT INTO events (session_id, ts, type) VALUES (3, '2026-08-01T10:00:00.000Z', 'user_prompt')`)

	sqlBody, err := os.ReadFile("migrations/0055_drop_phantom_sessions.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(sqlBody)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var ids []int64
	rows, err := db.Query(`SELECT id FROM sessions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// 3 has content, 4 has a real start, 5 is a hook-minted row of a live
	// session (cwd known, clock not yet), 6 has a pid — none of them are
	// phantoms. Only 1 and 2 go.
	if len(ids) != 4 || ids[0] != 3 || ids[1] != 4 || ids[2] != 5 || ids[3] != 6 {
		t.Fatalf("surviving sessions = %v, want [3 4 5 6]", ids)
	}

	// Sessions 3 and 6 still hold the placeholder project, so it must survive.
	var unknown int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE path = '(unknown)'`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if unknown != 1 {
		t.Errorf("'(unknown)' project rows = %d, want 1 (still referenced by sessions 3 and 6)", unknown)
	}

	// Once the last reference goes, a re-run drops the placeholder.
	mustExec(`DELETE FROM events WHERE session_id = 3`)
	mustExec(`DELETE FROM sessions WHERE id IN (3, 6)`)
	if _, err := db.Exec(string(sqlBody)); err != nil {
		t.Fatalf("re-apply migration: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE path = '(unknown)'`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if unknown != 0 {
		t.Errorf("'(unknown)' project rows = %d, want 0 once orphaned", unknown)
	}
}
