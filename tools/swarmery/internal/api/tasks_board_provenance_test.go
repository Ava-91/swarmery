package api

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// TestRoutinesTaskCreatorRejectsBlankCards: a routine step can no longer mint a
// card with no title or no prompt — the same guard the board POST has, now
// enforced by the shared constructor.
func TestRoutinesTaskCreatorRejectsBlankCards(t *testing.T) {
	srv, db := testServerWithDB(t)
	defer srv.Close()
	c := NewRoutinesTaskCreator(db)

	if _, err := c.CreateTask(1, "   ", "do the thing", "todo"); err == nil {
		t.Error("blank title: want an error")
	}
	if _, err := c.CreateTask(1, "a title", "", "todo"); err == nil {
		t.Error("empty prompt: want an error")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tasks = %d (%v), want 0 — a rejected card must not leave a row", n, err)
	}

	// The healthy path still lands in the asked-for column with the defaults.
	extID, err := c.CreateTask(1, "a title", "a prompt", "todo")
	if err != nil || !strings.HasPrefix(extID, "T-") {
		t.Fatalf("CreateTask = %q, %v", extID, err)
	}
	var column, origin string
	var movedAt sql.NullString
	if err := db.QueryRow(`SELECT board_column, origin, column_moved_at FROM tasks WHERE external_id = ?`, extID).
		Scan(&column, &origin, &movedAt); err != nil {
		t.Fatal(err)
	}
	if column != "todo" || origin != "manual" || !movedAt.Valid {
		t.Errorf("row = %s/%s/moved=%v, want todo/manual/moved", column, origin, movedAt.Valid)
	}
	// An unknown column falls back to triage rather than failing the step.
	extID, err = c.CreateTask(1, "b", "b", "backlog")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT board_column FROM tasks WHERE external_id = ?`, extID).Scan(&column); err != nil || column != "triage" {
		t.Errorf("unknown column landed in %q (%v), want triage", column, err)
	}
}

// TestBoardTaskExposesProvenanceAndExpiry: a captured card reports its source
// and the instant the inbox sweeper will retire it; a manual card reports null
// for both. dispatchedPrompt reads back whatever the dispatcher recorded.
func TestBoardTaskExposesProvenanceAndExpiry(t *testing.T) {
	srv, db := testServerWithDB(t)
	defer srv.Close()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, project_id, session_uuid, status, started_at)
		 VALUES (9, 1, 'sess-uuid', 'completed', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	sess := int64(9)
	created := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	capturedID, _, err := store.InsertBoardTask(db, store.BoardTaskInput{
		ProjectID: 1, Title: "captured", Prompt: "body", Origin: "session", OriginSessionID: &sess,
		OriginTurnUUID: "rec-1", OriginQuote: "opened with this", OriginFiles: []string{"x.go"},
		CaptureKey: "todo:sess-uuid:1", Now: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	manualID := createdBoardTask(t, srv.URL, "manual")
	if _, err := db.Exec(`UPDATE tasks SET dispatched_prompt = 'what the runner saw' WHERE id = ?`, manualID); err != nil {
		t.Fatal(err)
	}

	var list []boardTaskDTO
	getJSON(t, srv.URL+"/api/board/tasks?projectId=1", &list)
	byID := map[int64]boardTaskDTO{}
	for _, d := range list {
		byID[d.ID] = d
	}

	cap, ok := byID[capturedID]
	if !ok {
		t.Fatal("captured card missing from the list")
	}
	if cap.Source == nil {
		t.Fatal("captured card: source = null")
	}
	if cap.Source.SessionID == nil || *cap.Source.SessionID != sess {
		t.Errorf("source.sessionId = %v, want %d", cap.Source.SessionID, sess)
	}
	if cap.Source.TurnUUID == nil || *cap.Source.TurnUUID != "rec-1" {
		t.Errorf("source.turnUuid = %v, want rec-1", cap.Source.TurnUUID)
	}
	if cap.Source.Quote == nil || *cap.Source.Quote != "opened with this" {
		t.Errorf("source.quote = %v", cap.Source.Quote)
	}
	if len(cap.Source.Files) != 1 || cap.Source.Files[0] != "x.go" {
		t.Errorf("source.files = %v, want [x.go]", cap.Source.Files)
	}
	// NewServer runs with the default 14-day TTL: created_at + 14d.
	if cap.StaleAfter == nil || *cap.StaleAfter != "2026-08-24T12:00:00Z" {
		t.Errorf("staleAfter = %v, want 2026-08-24T12:00:00Z", cap.StaleAfter)
	}
	if cap.DispatchedPrompt != nil {
		t.Errorf("never-dispatched card has dispatchedPrompt %q", *cap.DispatchedPrompt)
	}

	man, ok := byID[manualID]
	if !ok {
		t.Fatal("manual card missing from the list")
	}
	if man.Source != nil {
		t.Errorf("manual card: source = %+v, want null", man.Source)
	}
	if man.StaleAfter != nil {
		t.Errorf("manual card: staleAfter = %q, want null", *man.StaleAfter)
	}
	if man.DispatchedPrompt == nil || *man.DispatchedPrompt != "what the runner saw" {
		t.Errorf("dispatchedPrompt = %v", man.DispatchedPrompt)
	}

	// A handler with the sweep off reports no expiry for anyone.
	off := &Handler{DB: db}
	d, err := off.boardTaskByID(capturedID)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	if d.StaleAfter != nil {
		t.Errorf("sweep off: staleAfter = %q, want null", *d.StaleAfter)
	}
	// Source is a nullable object even on the single-row read, with files never
	// null inside it.
	if d.Source == nil || d.Source.Files == nil {
		t.Errorf("single-row read: source = %+v", d.Source)
	}
}
