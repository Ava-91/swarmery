package runcore

import (
	"database/sql"
	"testing"
)

// insertSession writes a minimal sessions row and returns its id.
func insertSession(t *testing.T, db *sql.DB, uuid string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO sessions (session_uuid, started_at, project_id)
		VALUES (?, '2026-08-17T10:00:00Z', 1)`, uuid)
	if err != nil {
		t.Fatalf("insert session %s: %v", uuid, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func linkRow(t *testing.T, db *sql.DB, taskID, sessionID int64) (source string, confidence sql.NullFloat64) {
	t.Helper()
	err := db.QueryRow(`SELECT link_source, confidence FROM task_sessions
		 WHERE task_id=? AND session_id=?`, taskID, sessionID).Scan(&source, &confidence)
	if err != nil {
		t.Fatalf("read link (%d,%d): %v", taskID, sessionID, err)
	}
	return source, confidence
}

func TestLinkSession_LinksWhenTheSessionIsIngested(t *testing.T) {
	db := wtFixture(t)
	taskID := insertWorkspaceTask(t, db, "2026-08-17-epic")
	sid := insertSession(t, db, "u-live")

	gotSID, linked := LinkSession(db, "phaserun", taskID, "u-live")
	if !linked || gotSID != sid {
		t.Fatalf("LinkSession = (%d, %v), want (%d, true)", gotSID, linked, sid)
	}
	source, conf := linkRow(t, db, taskID, sid)
	if source != LinkExplicit {
		t.Errorf("link_source = %q, want explicit", source)
	}
	if !conf.Valid || conf.Float64 != 1.0 {
		t.Errorf("confidence = %+v, want 1.0", conf)
	}
}

// At run START the transcript has not been ingested — that is the NORMAL case, not
// an error, and it must leave no state behind for the reconcile arm to trip over.
func TestLinkSession_ParksWhenTheSessionIsNotIngestedYet(t *testing.T) {
	db := wtFixture(t)
	taskID := insertWorkspaceTask(t, db, "2026-08-17-epic")

	if sid, linked := LinkSession(db, "phaserun", taskID, "u-not-yet"); linked || sid != 0 {
		t.Fatalf("LinkSession = (%d, %v), want (0, false)", sid, linked)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_sessions WHERE task_id=?`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("task_sessions rows = %d, want 0 — a parked link must write nothing", n)
	}

	// The reconcile arm: once ingest catches up, the same call links.
	sid := insertSession(t, db, "u-not-yet")
	if got, linked := LinkSession(db, "phaserun", taskID, "u-not-yet"); !linked || got != sid {
		t.Fatalf("re-invoked LinkSession = (%d, %v), want (%d, true)", got, linked, sid)
	}
}

// Linking is idempotent: every engine calls it at least twice (start, end) and
// wsingest may call it again on every reconcile pass.
func TestLinkSession_IsIdempotent(t *testing.T) {
	db := wtFixture(t)
	taskID := insertWorkspaceTask(t, db, "2026-08-17-epic")
	insertSession(t, db, "u-live")

	for i := 0; i < 3; i++ {
		if _, linked := LinkSession(db, "planrun", taskID, "u-live"); !linked {
			t.Fatalf("call %d did not link", i)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_sessions WHERE task_id=?`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("task_sessions rows = %d, want 1", n)
	}
}

func TestLinkSession_IgnoresEmptyInputs(t *testing.T) {
	db := wtFixture(t)
	taskID := insertWorkspaceTask(t, db, "2026-08-17-epic")
	insertSession(t, db, "u-live")
	if _, linked := LinkSession(db, "phaserun", taskID, ""); linked {
		t.Error("linked with no session uuid")
	}
	if _, linked := LinkSession(db, "phaserun", 0, "u-live"); linked {
		t.Error("linked with no task id")
	}
}

// The asymmetry that makes link_source worth having: a guess must never overwrite
// the daemon's assertion, but the assertion upgrades a guess.
func TestUpsertLink_ExplicitNeverDowngrades(t *testing.T) {
	db := wtFixture(t)
	taskID := insertWorkspaceTask(t, db, "2026-08-17-epic")
	sid := insertSession(t, db, "u-live")

	// heuristic → explicit upgrades.
	half := 0.5
	if err := UpsertLink(db, taskID, sid, LinkHeuristic, &half); err != nil {
		t.Fatal(err)
	}
	if _, linked := LinkSession(db, "phaserun", taskID, "u-live"); !linked {
		t.Fatal("explicit link did not land over a heuristic one")
	}
	source, conf := linkRow(t, db, taskID, sid)
	if source != LinkExplicit || conf.Float64 != 1.0 {
		t.Errorf("link = (%q, %v), want explicit/1.0", source, conf.Float64)
	}

	// explicit → heuristic does NOT downgrade, and keeps explicit's confidence.
	nine := 0.9
	if err := UpsertLink(db, taskID, sid, LinkHeuristic, &nine); err != nil {
		t.Fatal(err)
	}
	source, conf = linkRow(t, db, taskID, sid)
	if source != LinkExplicit {
		t.Errorf("link_source = %q, want explicit to survive a heuristic upsert", source)
	}
	if conf.Float64 != 1.0 {
		t.Errorf("confidence = %v, want explicit's 1.0 to survive", conf.Float64)
	}
}

// tasks.session_id is the board's single-session FK: set once, never overwritten.
func TestStampPrimarySession_SetsOnceOnly(t *testing.T) {
	db := wtFixture(t)
	insertBoardRun(t, db, "T-1")
	var taskID int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE external_id='T-1'`).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	first := insertSession(t, db, "u-first")
	second := insertSession(t, db, "u-second")

	StampPrimarySession(db, "dispatch", taskID, first)
	StampPrimarySession(db, "dispatch", taskID, second)

	var got sql.NullInt64
	if err := db.QueryRow(`SELECT session_id FROM tasks WHERE id=?`, taskID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.Int64 != first {
		t.Errorf("session_id = %+v, want the FIRST session (%d) — COALESCE must not overwrite", got, first)
	}
}
