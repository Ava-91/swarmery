package runcore

// Session linking — attaching the session a run produced to the work it served.
//
// Every daemon-spawned run already carries a pre-generated `--session-id`, so the
// link is knowable exactly: the run's uuid IS the session's uuid. What was missing
// is that only dispatch ever wrote it down. Phase and plan runs stamped their uuid
// on their own row and stopped there, so the Plans page's sessions panel — which
// reads task_sessions — showed "no sessions ran this plan" while phases were being
// built. That was observed live during the board-redesign run.
//
// The link is best-effort by design: a run is not less real because its transcript
// has not been ingested yet, so a missing sessions row parks the link rather than
// failing anything. Callers re-invoke (at run end, and on wsingest's reconcile
// pass) until it lands.

import (
	"database/sql"
	"errors"
	"log"
)

// task_sessions.link_source values. The column has exactly two: a link is either
// asserted by whoever spawned the run (explicit) or derived from evidence
// (heuristic). Confidence is meaningful only for the second.
const (
	LinkExplicit  = "explicit"
	LinkHeuristic = "heuristic"
)

// UpsertLink writes one task↔session link. Explicit always wins: a heuristic
// upsert never downgrades an existing explicit row, while an explicit upsert
// upgrades a heuristic one.
//
// That asymmetry is the whole point of the column. A heuristic link is a guess from
// cwd overlap or edited paths; an explicit one is the daemon saying "I spawned this
// session for this task". Letting the guess overwrite the assertion would make the
// stronger claim decay into the weaker one on the next reconcile pass.
func UpsertLink(db *sql.DB, taskID, sessionID int64, source string, confidence *float64) error {
	_, err := db.Exec(`
		INSERT INTO task_sessions (task_id, session_id, link_source, confidence)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(task_id, session_id) DO UPDATE SET
			link_source = CASE WHEN task_sessions.link_source = 'explicit'
			                   THEN 'explicit' ELSE excluded.link_source END,
			confidence  = CASE WHEN task_sessions.link_source = 'explicit'
			                   THEN task_sessions.confidence ELSE excluded.confidence END`,
		taskID, sessionID, source, confidence)
	return err
}

// LinkSession records the explicit task↔session link for a run this daemon
// spawned, and returns the resolved sessions.id.
//
// linked=false means the session is not ingested YET (the transcript lands
// asynchronously, and at run START it never exists). That is not an error and not a
// terminal state: the caller re-invokes at run end, and wsingest's reconcile pass
// converges anything still missing. Every failure here is logged and swallowed for
// the same reason — a link is a view onto the work, never a gate on it.
//
// engine only names the log line.
func LinkSession(db *sql.DB, engine string, taskID int64, sessionUUID string) (int64, bool) {
	if sessionUUID == "" || taskID == 0 {
		return 0, false
	}
	var sid int64
	err := db.QueryRow(`SELECT id FROM sessions WHERE session_uuid=?`, sessionUUID).Scan(&sid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false // not ingested yet — park; a later pass links it
	}
	if err != nil {
		log.Printf("error: %s: resolve session for link (task %d): %v", engine, taskID, err)
		return 0, false
	}
	one := 1.0
	if err := UpsertLink(db, taskID, sid, LinkExplicit, &one); err != nil {
		log.Printf("error: %s: insert task_session link (task %d): %v", engine, taskID, err)
		return sid, false
	}
	return sid, true
}

// StampPrimarySession sets tasks.session_id when it is unset — the single-session
// FK the board reads for "the session that did this card".
//
// Only board tasks get this, deliberately. A workspace/epic row has no ONE session:
// it accumulates a plan run, several phase runs and any number of interactive
// sessions, and picking whichever happened to link first would put an arbitrary one
// in a field the UI presents as authoritative. For those rows task_sessions is the
// whole truth.
func StampPrimarySession(db *sql.DB, engine string, taskID, sessionID int64) {
	if _, err := db.Exec(
		`UPDATE tasks SET session_id=COALESCE(session_id, ?) WHERE id=?`, sessionID, taskID); err != nil {
		log.Printf("error: %s: set task.session_id (task %d): %v", engine, taskID, err)
	}
}
