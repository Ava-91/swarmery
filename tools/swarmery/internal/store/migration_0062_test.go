package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// Migration 0062 repairs subagent durations that were recorded as the launch
// roundtrip. The rule it encodes is the one ingest now applies: a run lasts from
// its subagent_start to its LAST sidechain event. These tests pin the three
// properties that make a history rewrite safe to ship.
func seedRun(t *testing.T, db *sql.DB, startID int64, startTS, lastChildTS string,
	recorded int64, stopStatus string) {
	t.Helper()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mustExec(`INSERT OR IGNORE INTO projects (id, path, slug, first_seen) VALUES (1,'/p','p','2026-01-01T00:00:00Z')`)
	mustExec(`INSERT OR IGNORE INTO sessions (id, project_id, session_uuid, status, started_at)
	          VALUES (1,1,'s-0062','completed','2026-01-01T00:00:00Z')`)
	mustExec(`INSERT INTO events (id, session_id, ts, type, tool_name, status, duration_ms, payload, dedup_key)
	          VALUES (?,1,?, 'subagent_start','Agent','ok',?,'{"subagent_type":"core:verification-agent"}',?)`,
		startID, startTS, recorded, "start-"+startTS)
	payload := `{"agentId":"","agentType":"","status":"","totalTokens":0}`
	if stopStatus != "" {
		payload = `{"agentId":"a","agentType":"","status":"` + stopStatus + `","totalTokens":0}`
	}
	mustExec(`INSERT INTO events (id, session_id, ts, type, tool_name, status, duration_ms, payload, parent_event_id, dedup_key)
	          VALUES (?,1,?, 'subagent_stop','Agent','ok',?,?,?,?)`,
		startID+1000, lastChildTS, recorded, payload, startID, "stop-"+startTS)
	mustExec(`INSERT INTO events (id, session_id, ts, type, tool_name, status, payload, parent_event_id, dedup_key)
	          VALUES (?,1,?, 'tool_call','Bash','ok','{}',?,?)`,
		startID+2000, lastChildTS, startID, "kid-"+startTS)
}

func durationOf(t *testing.T, db *sql.DB, id int64) int64 {
	t.Helper()
	var d sql.NullInt64
	if err := db.QueryRow(`SELECT duration_ms FROM events WHERE id=?`, id).Scan(&d); err != nil {
		t.Fatalf("read duration %d: %v", id, err)
	}
	return d.Int64
}

// applyRepair runs migration 0062's SQL against an already-migrated DB. The
// migration is idempotent and monotonic by construction, so re-applying it is
// exactly what a fresh install does — and running it here, AFTER the fixture
// rows exist, is the only way to observe what it does to history.
func applyRepair(t *testing.T, db *sql.DB) {
	t.Helper()
	sqlText, err := os.ReadFile(filepath.Join("migrations", "0062_subagent_duration_backfill.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(sqlText)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

func TestMigration0062RepairsUnderstatedDurations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "m62.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// A run whose result said nothing: recorded as the 1.8s launch roundtrip,
	// with a child event three minutes later.
	seedRun(t, db, 1, "2026-08-13T16:23:13.000Z", "2026-08-13T16:26:33.000Z", 1780, "")
	// A run whose result DID report — must be left exactly as it is, even though
	// its child is later. The tool's own number is authoritative there.
	seedRun(t, db, 2, "2026-08-13T17:00:00.000Z", "2026-08-13T17:05:00.000Z", 4200, "completed")
	// A silent run already recorded LONGER than its child span: the repair only
	// ever grows a duration, so this must not shrink.
	seedRun(t, db, 3, "2026-08-13T18:00:00.000Z", "2026-08-13T18:00:10.000Z", 99000, "")

	applyRepair(t, db)

	if got := durationOf(t, db, 1); got < 199_000 || got > 201_000 {
		t.Errorf("understated run = %dms, want ~200000 (start → last child)", got)
	}
	if got := durationOf(t, db, 1001); got != durationOf(t, db, 1) {
		t.Errorf("stop row = %d, start row = %d — the pair must stay equal", got, durationOf(t, db, 1))
	}
	if got := durationOf(t, db, 2); got != 4200 {
		t.Errorf("reported run = %d, want 4200 untouched", got)
	}
	if got := durationOf(t, db, 3); got != 99000 {
		t.Errorf("already-longer run = %d, want 99000 — the repair must never shrink", got)
	}

	// Idempotent: a second pass changes nothing. A history repair that moved a
	// number every time it ran would make the metric unreadable in a different way.
	before := durationOf(t, db, 1)
	applyRepair(t, db)
	if after := durationOf(t, db, 1); after != before {
		t.Errorf("second pass moved the duration %d → %d", before, after)
	}
}
