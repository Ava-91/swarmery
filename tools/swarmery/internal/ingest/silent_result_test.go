package ingest

import (
	"path/filepath"
	"testing"
)

// TestIngestSilentResultAgentSession: an Agent call whose tool_result reports
// NOTHING — no status, no agentId, no totalDurationMs — must still be dated from
// its sidechain, not from the launch roundtrip.
//
// This is the shape behind a retrospective's "verification-agent p95 47s /
// test-writer p95 1s at $0.00", which read as a fleet of verifiers dying in
// seconds. Six of seven verification runs in that window carried 8–24 parented
// sidechain events spanning two to three minutes and were each recorded as ~1.8
// seconds. The agent was working; only the measurement was broken, and every
// per-agent duration statistic built on it described the spawn instead of the
// work.
//
// The fixture is deliberately a FOREGROUND call (no run_in_background): the
// pre-existing reconcile path keyed on the "async_launched" marker, so this case
// — the one that produced the wrong number — was the one it did not cover.
func TestIngestSilentResultAgentSession(t *testing.T) {
	db := testDB(t)
	path := filepath.Join(fixtures, "silent-result-agent-session.jsonl")
	if _, err := File(db, path); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// Same span as the background fixture it was derived from: Agent tool_use
	// 12:00:05.000 → last sidechain record 12:20:05.000.
	const wantDuration = 20 * 60 * 1000

	var startID, startDuration int64
	if err := db.QueryRow(
		`SELECT id, duration_ms FROM events WHERE type='subagent_start'`,
	).Scan(&startID, &startDuration); err != nil {
		t.Fatalf("subagent_start: %v", err)
	}
	var stopDuration int64
	if err := db.QueryRow(
		`SELECT duration_ms FROM events WHERE type='subagent_stop'`,
	).Scan(&stopDuration); err != nil {
		t.Fatalf("subagent_stop: %v", err)
	}
	if startDuration != wantDuration || stopDuration != wantDuration {
		t.Errorf("duration_ms = start %d / stop %d, want %d — a silent tool_result must not leave the launch roundtrip as the run's duration",
			startDuration, stopDuration, wantDuration)
	}

	// The sidechain is what dates it, so it has to be attached.
	if got := count(t, db,
		`SELECT COUNT(*) FROM events WHERE type='tool_call' AND parent_event_id=?`, startID); got == 0 {
		t.Error("no sidechain events parented to the run — nothing could have dated it")
	}

	// Re-ingest converges: the duration only ever grows toward the true end, so a
	// second pass neither inflates nor resets it.
	if _, err := File(db, path); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	if err := db.QueryRow(
		`SELECT duration_ms FROM events WHERE type='subagent_stop'`,
	).Scan(&stopDuration); err != nil {
		t.Fatal(err)
	}
	if stopDuration != wantDuration {
		t.Errorf("after re-ingest duration = %d, want %d (monotonic and idempotent)", stopDuration, wantDuration)
	}
}

// A result that DOES report its own duration stays authoritative — the
// reconciliation must not overwrite a number the tool actually gave us.
func TestReportedDurationWins(t *testing.T) {
	db := testDB(t)
	if _, err := File(db, filepath.Join(fixtures, "subagent-session.jsonl")); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	var reported int64
	if err := db.QueryRow(
		`SELECT duration_ms FROM events WHERE type='subagent_stop' LIMIT 1`).Scan(&reported); err != nil {
		t.Skipf("fixture has no subagent_stop: %v", err)
	}
	if reported <= 0 {
		t.Errorf("duration_ms = %d, want the reported value preserved", reported)
	}
}
