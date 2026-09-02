package store

import (
	"database/sql"
	"strings"
	"testing"
)

// oldMarker is the exact prose marker capture used to append the session's
// opening prompt to a card's prompt before 0066 moved it into origin_quote.
const oldMarker = "\n\nThat session opened with:\n"

// seed0065 builds a database as it stood before 0066 with the two fixture
// shapes the backfill must tell apart: a captured card carrying the marker and
// a hand-written one that happens to contain the same words.
func seed0065(t *testing.T) (db *sql.DB, capturedID, manualID int64) {
	t.Helper()
	db = openRaw(t)
	migrateUpTo(t, db, 65)
	if cols := columnSet(t, db, "tasks"); cols["origin_quote"] {
		t.Fatal("tasks.origin_quote exists before 0066 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, path, slug, first_seen) VALUES (1, '/tmp/p', 'p', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, project_id, session_uuid, status, started_at)
		 VALUES (7, 1, 'sess-uuid', 'completed', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	captured := "Extract the retry helper\n\n---\nCaptured from session sess-uuid" +
		oldMarker + "Refactor the retry helper and add tests for the backoff."
	res, err := db.Exec(
		`INSERT INTO tasks (project_id, title, prompt, status, created_at, source, external_id,
		                    board_column, origin, origin_session_id, capture_key)
		 VALUES (1, 'Extract the retry helper', ?, 'queued', '2026-08-01T00:00:00Z', 'queue',
		         'T-cap001', 'triage', 'session', 7, 'todo:sess-uuid:abc')`, captured)
	if err != nil {
		t.Fatal(err)
	}
	capturedID, _ = res.LastInsertId()
	manual := "Write the release notes." + oldMarker + "this is the author's own wording"
	res, err = db.Exec(
		`INSERT INTO tasks (project_id, title, prompt, status, created_at, source, external_id,
		                    board_column, origin)
		 VALUES (1, 'release notes', ?, 'queued', '2026-08-01T00:01:00Z', 'queue', 'T-man001',
		         'todo', 'manual')`, manual)
	if err != nil {
		t.Fatal(err)
	}
	manualID, _ = res.LastInsertId()
	return db, capturedID, manualID
}

func TestMigrate0066FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 66`).Scan(&name); err != nil {
		t.Fatalf("migration 66 not recorded: %v", err)
	}
	if name != "0066_task_provenance.sql" {
		t.Errorf("migration 66 name = %s", name)
	}
	mustHaveColumns(t, db, "tasks", "origin_turn_uuid", "origin_quote", "origin_files", "dispatched_prompt")
}

// TestMigrate0066BackfillMovesQuote: the captured fixture's quote is MOVED —
// it lands in origin_quote and the marker plus the quote are cut out of prompt
// — while the manual fixture, marker and all, is left byte-for-byte alone.
func TestMigrate0066BackfillMovesQuote(t *testing.T) {
	db, capturedID, manualID := seed0065(t)
	var manualBefore string
	if err := db.QueryRow(`SELECT prompt FROM tasks WHERE id = ?`, manualID).Scan(&manualBefore); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}

	var prompt string
	var quote sql.NullString
	if err := db.QueryRow(`SELECT prompt, origin_quote FROM tasks WHERE id = ?`, capturedID).
		Scan(&prompt, &quote); err != nil {
		t.Fatal(err)
	}
	const wantQuote = "Refactor the retry helper and add tests for the backoff."
	if !quote.Valid || quote.String != wantQuote {
		t.Errorf("origin_quote = %v, want %q", quote, wantQuote)
	}
	if strings.Contains(prompt, "That session opened with:") {
		t.Errorf("prompt still carries the marker after the move:\n%s", prompt)
	}
	if strings.Contains(prompt, wantQuote) {
		t.Errorf("prompt still carries the quote — the backfill copied instead of moving:\n%s", prompt)
	}
	if want := "Extract the retry helper\n\n---\nCaptured from session sess-uuid"; prompt != want {
		t.Errorf("prompt = %q, want %q (everything before the marker, trailing newlines trimmed)", prompt, want)
	}

	var manualAfter string
	var manualQuote sql.NullString
	if err := db.QueryRow(`SELECT prompt, origin_quote FROM tasks WHERE id = ?`, manualID).
		Scan(&manualAfter, &manualQuote); err != nil {
		t.Fatal(err)
	}
	if manualAfter != manualBefore {
		t.Errorf("manual prompt changed:\n%q\n→\n%q", manualBefore, manualAfter)
	}
	if manualQuote.Valid {
		t.Errorf("manual card got origin_quote %q, want NULL", manualQuote.String)
	}

	// A captured card WITHOUT the marker (a session card, whose prompt IS the
	// opening prompt) is untouched too, and a second Migrate is a no-op.
	if _, err := db.Exec(
		`INSERT INTO tasks (project_id, title, prompt, status, created_at, source, external_id,
		                    board_column, origin, origin_session_id, capture_key)
		 VALUES (1, 'sess card', 'Ship it\n\n---\nCaptured from session sess-uuid', 'queued',
		         '2026-08-01T00:02:00Z', 'queue', 'T-sess01', 'triage', 'session', 7, 'sess:sess-uuid')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE origin_quote IS NOT NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows with origin_quote = %d, want 1 (only the marker-bearing captured card)", n)
	}
}
