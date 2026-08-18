package wsingest

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// planDir is the gamma fixture's plan directory — the only one in testdata with
// real phase docs, which is what makes it the natural subject for path-inferred
// links.
func planDir() string {
	return filepath.Join(fixtureRoot, "projgamma", "workspace", "working", "2026", "07", "08", "gamma-task", "plan")
}

func gammaTaskID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE external_id='2026-07-08-gamma-task'`).Scan(&id); err != nil {
		t.Fatalf("gamma-task row: %v", err)
	}
	return id
}

// recordEdit writes the file_changes row ingest produces for one edited path.
func recordEdit(t *testing.T, db *sql.DB, sessionID int64, path string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO events (session_id, type, ts) VALUES (?, 'file_change', '2026-07-08T10:00:00Z')`, sessionID)
	var eventID int64
	if err := db.QueryRow(`SELECT id FROM events ORDER BY id DESC LIMIT 1`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO file_changes (event_id, session_id, file_path, change_type)
		VALUES (?, ?, ?, 'edit')`, eventID, sessionID, path)
}

func linkOf(t *testing.T, db *sql.DB, taskID, sessionID int64) (string, sql.NullFloat64) {
	t.Helper()
	var (
		source string
		conf   sql.NullFloat64
	)
	err := db.QueryRow(`SELECT link_source, confidence FROM task_sessions
		 WHERE task_id=? AND session_id=?`, taskID, sessionID).Scan(&source, &conf)
	if err != nil {
		t.Fatalf("no link for (task %d, session %d): %v", taskID, sessionID, err)
	}
	return source, conf
}

// TestScanInfersLinksFromPlanDirEdits covers the THIRD execution path — interactive
// sessions. /run-plan, a tech-lead session, an ad-hoc chat: none is daemon-spawned,
// so nothing registers them, and their cost and outcome used to attach to no plan at
// all. Editing a file under the plan's own directory is the evidence that closes
// that gap (decision D3: inference, not registration).
func TestScanInfersLinksFromPlanDirEdits(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	pinMtime(t)
	// s6's cwd is outside every workspace, so the cwd heuristic cannot reach it —
	// which is exactly what makes it a clean subject: any link it gets came from the
	// edited path.
	recordEdit(t, db, 6, filepath.Join(planDir(), "phase-2-parser.md"))

	scan(t, db)

	source, conf := linkOf(t, db, gammaTaskID(t, db), 6)
	if source != "heuristic" {
		t.Errorf("link_source = %q, want heuristic — an inferred link is evidence, not an assertion", source)
	}
	if !conf.Valid || conf.Float64 != planDirLinkConfidence {
		t.Errorf("confidence = %+v, want %v", conf, planDirLinkConfidence)
	}
}

// A session that edited something ELSE entirely must not be linked: the signal is
// the plan directory, not the workspace at large.
func TestScanDoesNotInferLinksFromUnrelatedEdits(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	pinMtime(t)
	recordEdit(t, db, 6, "/work/projgamma/apps/main/src/index.ts")

	scan(t, db)

	if n := count(t, db, `SELECT COUNT(*) FROM task_sessions WHERE task_id=? AND session_id=6`,
		gammaTaskID(t, db)); n != 0 {
		t.Errorf("links = %d, want 0 — an edit outside plan/ is not evidence about the plan", n)
	}
}

// An explicit link — the daemon saying "I spawned this session for this task" — must
// survive a path-inferred pass over the same pair. Otherwise every reconcile would
// decay the stronger claim into the weaker one.
func TestScanPlanDirInferenceNeverDowngradesExplicit(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	pinMtime(t)
	scan(t, db) // creates the task row
	taskID := gammaTaskID(t, db)
	mustExec(t, db, `INSERT INTO task_sessions (task_id, session_id, link_source, confidence)
		VALUES (?, 6, 'explicit', 1.0)`, taskID)
	recordEdit(t, db, 6, filepath.Join(planDir(), "phase-1-schema.md"))

	scan(t, db)

	source, conf := linkOf(t, db, taskID, 6)
	if source != "explicit" || conf.Float64 != 1.0 {
		t.Errorf("link = (%q, %v), want explicit/1.0 to survive", source, conf.Float64)
	}
}

// The convergence arm: a phase or plan run stamps its uuid on its own row, and both
// of its own link attempts can miss (at start the transcript does not exist; at exit
// ingest may still be behind). This pass finishes the job, which is what makes "the
// sessions panel lists every run of this plan" true rather than racy.
func TestScanLinksRunSessionsFromTheirUUIDs(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	pinMtime(t)
	scan(t, db)
	taskID := gammaTaskID(t, db)

	// A phase run and a plan run of this plan, each wearing a session uuid that is
	// already ingested but was never linked.
	mustExec(t, db, `UPDATE epic_phases SET run_session_uuid=?
		 WHERE workspace_task_id=? AND seq=1`,
		"11111111-1111-4111-8111-111111111111", taskID) // s3
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state, run_session_uuid)
		VALUES (?, 'running', ?)`, taskID, "44444444-4444-4444-8444-444444444444") // s6

	scan(t, db)

	for _, sessionID := range []int64{3, 6} {
		source, conf := linkOf(t, db, taskID, sessionID)
		if source != "explicit" {
			t.Errorf("session %d link_source = %q, want explicit — the daemon spawned it with that uuid",
				sessionID, source)
		}
		if conf.Float64 != 1.0 {
			t.Errorf("session %d confidence = %v, want 1.0", sessionID, conf.Float64)
		}
	}
}

// A run whose uuid is not ingested must not produce a link — and must not error the
// scan either.
func TestScanRunSessionLinkSkipsUningestedUUIDs(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	pinMtime(t)
	scan(t, db)
	taskID := gammaTaskID(t, db)
	mustExec(t, db, `UPDATE epic_phases SET run_session_uuid='not-ingested-yet'
		 WHERE workspace_task_id=? AND seq=1`, taskID)

	before := count(t, db, `SELECT COUNT(*) FROM task_sessions`)
	scan(t, db)
	if after := count(t, db, `SELECT COUNT(*) FROM task_sessions`); after != before {
		t.Errorf("task_sessions grew from %d to %d for a uuid with no session row", before, after)
	}
}
