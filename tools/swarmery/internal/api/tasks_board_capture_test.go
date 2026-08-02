package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
)

// seedAgent inserts one registry agent row. scope 'global' + NULL project_id is
// the shape a plugin/global agent has; a project-scoped row passes projectID.
func seedAgent(t *testing.T, db *sql.DB, name, scope string, projectID any) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO agents (name, scope, project_id, file_path, deleted)
		 VALUES (?, ?, ?, ?, 0)`, name, scope, projectID, "/agents/"+name+".md"); err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
}

// TestBoardTaskAgentSelection: a registry agent name round-trips through
// create → GET, an unknown name is a 400, and PATCH can both set and clear it.
func TestBoardTaskAgentSelection(t *testing.T) {
	srv, db := testServerWithDB(t) // fixture ingests one project (id 1)
	seedAgent(t, db, "tech-lead", "global", nil)
	seedAgent(t, db, "proj-only", "project", 1)
	// Soft-deleted agents must not resolve — the registry tombstones an agent
	// whose file is gone, and dispatching to it would fail at spawn time.
	if _, err := db.Exec(
		`INSERT INTO agents (name, scope, project_id, file_path, deleted)
		 VALUES ('retired', 'global', NULL, '/agents/retired.md', 1)`); err != nil {
		t.Fatal(err)
	}

	// --- Create with a valid global agent ---
	resp := postBoard(t, srv.URL, `{"projectId":1,"title":"dispatch me","prompt":"p","agent":"tech-lead"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create with agent = %d, want 201", resp.StatusCode)
	}
	var created boardTaskDTO
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.Agent == nil || *created.Agent != "tech-lead" {
		t.Fatalf("created.Agent = %v, want tech-lead", created.Agent)
	}
	// Capture provenance defaults: a hand-written card is manual with no session.
	if created.Origin != "manual" || created.OriginSessionID != nil {
		t.Errorf("created provenance = %q/%v, want manual/nil", created.Origin, created.OriginSessionID)
	}

	// --- The value survives the read path (GET list), not just the POST echo ---
	var list []boardTaskDTO
	getJSON(t, srv.URL+"/api/board/tasks?projectId=1", &list)
	var found *boardTaskDTO
	for i := range list {
		if list[i].ID == created.ID {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("created task %d missing from GET list", created.ID)
	}
	if found.Agent == nil || *found.Agent != "tech-lead" || found.Origin != "manual" {
		t.Fatalf("GET round-trip = agent %v origin %q, want tech-lead/manual", found.Agent, found.Origin)
	}

	// --- A project-scoped agent resolves for its own project ---
	resp = postBoard(t, srv.URL, `{"projectId":1,"title":"scoped","prompt":"p","agent":"proj-only"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("create with project-scoped agent = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	// --- Rejections → 400 ---
	for name, body := range map[string]string{
		"unknown agent":     `{"projectId":1,"title":"t","prompt":"p","agent":"no-such-agent"}`,
		"deleted agent":     `{"projectId":1,"title":"t","prompt":"p","agent":"retired"}`,
		"non-manual origin": `{"projectId":1,"title":"t","prompt":"p","origin":"session"}`,
	} {
		r := postBoard(t, srv.URL, body)
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", name, r.StatusCode)
		}
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(r.Body).Decode(&e)
		r.Body.Close()
		if e.Error == "" {
			t.Errorf("POST %s: 400 body carried no JSON error", name)
		}
	}

	// --- Omitting agent leaves it null (not "") ---
	resp = postBoard(t, srv.URL, `{"projectId":1,"title":"plain","prompt":"p"}`)
	var plain boardTaskDTO
	json.NewDecoder(resp.Body).Decode(&plain)
	resp.Body.Close()
	if plain.Agent != nil {
		t.Errorf("omitted agent = %v, want nil", *plain.Agent)
	}

	// --- PATCH sets the agent ---
	resp = patchBoard(t, srv.URL, plain.ID, `{"agent":"tech-lead"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch agent = %d, want 200", resp.StatusCode)
	}
	var patched boardTaskDTO
	json.NewDecoder(resp.Body).Decode(&patched)
	resp.Body.Close()
	if patched.Agent == nil || *patched.Agent != "tech-lead" {
		t.Fatalf("patched.Agent = %v, want tech-lead", patched.Agent)
	}

	// --- PATCH with an empty string clears it back to a plain run ---
	resp = patchBoard(t, srv.URL, plain.ID, `{"agent":""}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear agent = %d, want 200", resp.StatusCode)
	}
	var cleared boardTaskDTO
	json.NewDecoder(resp.Body).Decode(&cleared)
	resp.Body.Close()
	if cleared.Agent != nil {
		t.Errorf("cleared agent = %v, want nil", *cleared.Agent)
	}

	// --- PATCH with an unknown agent is a 400 and changes nothing ---
	resp = patchBoard(t, srv.URL, plain.ID, `{"agent":"ghost"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("patch unknown agent = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestBoardTaskProvenanceImmutable: origin/captureKey/originSessionId are
// capture-owned. A PATCH naming any of them is a loud 400 — the alternative
// (silently ignoring an unknown field) would let a caller believe it rewrote a
// card's provenance.
func TestBoardTaskProvenanceImmutable(t *testing.T) {
	srv, _ := testServerWithDB(t)
	resp := postBoard(t, srv.URL, `{"projectId":1,"title":"prov","prompt":"p"}`)
	var created boardTaskDTO
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	for _, patch := range []string{
		`{"origin":"session"}`,
		`{"captureKey":"todo:abc:1"}`,
		`{"originSessionId":7}`,
		`{"boardColumn":"todo","origin":"llm"}`, // mixed with a legal field
	} {
		r := patchBoard(t, srv.URL, created.ID, patch)
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("PATCH %s = %d, want 400", patch, r.StatusCode)
		}
		r.Body.Close()
	}

	// The mixed patch above must not have applied its legal half either.
	var after boardTaskDTO
	resp = patchBoard(t, srv.URL, created.ID, `{}`)
	json.NewDecoder(resp.Body).Decode(&after)
	resp.Body.Close()
	if after.BoardColumn != "triage" || after.Origin != "manual" {
		t.Errorf("after rejected patches = %s/%s, want triage/manual", after.BoardColumn, after.Origin)
	}
}

// TestInsertCapturedTaskIdempotent: the whole point of capture_key. Capture
// re-reads the same transcript on every re-tail, so a second insert with the
// same key must return the SAME row and report inserted=false rather than mint
// a duplicate card.
func TestInsertCapturedTaskIdempotent(t *testing.T) {
	_, db := testServerWithDB(t)

	// origin_session_id is a real FK (0048) and foreign keys are enforced, so
	// take an id the fixture actually ingested rather than assuming 1.
	var sessionID int64
	if err := db.QueryRow(`SELECT id FROM sessions ORDER BY id LIMIT 1`).Scan(&sessionID); err != nil {
		t.Fatalf("fixture has no session to capture from: %v", err)
	}

	in := capturedTaskInput{
		ProjectID:       1,
		Title:           "extract the retry helper",
		Prompt:          "the session's TODO said to extract it",
		Origin:          "session",
		OriginSessionID: &sessionID,
		CaptureKey:      "todo:11111111-2222-3333-4444-555555555555:a1b2c3",
	}

	id, inserted, err := insertCapturedTask(db, in)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !inserted || id == 0 {
		t.Fatalf("first insert = (%d, %v), want a real insert", id, inserted)
	}

	// Replay: same key, deliberately different title/prompt — the key wins.
	replay := in
	replay.Title = "different title on the replay"
	id2, inserted2, err := insertCapturedTask(db, replay)
	if err != nil {
		t.Fatalf("replay insert: %v", err)
	}
	if inserted2 {
		t.Error("replay reported inserted=true; capture_key did not dedupe")
	}
	if id2 != id {
		t.Errorf("replay id = %d, want the original %d", id2, id)
	}

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE capture_key = ?`, in.CaptureKey).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows with capture_key = %d, want exactly 1", n)
	}
	// The stored row keeps the FIRST title — a replay is a no-op, not an update.
	var title, origin, column, source string
	var originSession sql.NullInt64
	var priority int
	if err := db.QueryRow(
		`SELECT title, origin, board_column, source, origin_session_id, priority
		   FROM tasks WHERE id = ?`, id,
	).Scan(&title, &origin, &column, &source, &originSession, &priority); err != nil {
		t.Fatal(err)
	}
	if title != in.Title {
		t.Errorf("title = %q, want the original %q (replay must not update)", title, in.Title)
	}
	if origin != "session" || column != "triage" || source != "queue" {
		t.Errorf("captured row = origin %q column %q source %q; want session/triage/queue",
			origin, column, source)
	}
	if !originSession.Valid || originSession.Int64 != sessionID {
		t.Errorf("origin_session_id = %v, want %d", originSession, sessionID)
	}
	if priority != priorityLabels["normal"] {
		t.Errorf("priority = %d, want normal (%d)", priority, priorityLabels["normal"])
	}

	// A second, DIFFERENT key is a real insert — dedupe is per key, not global.
	other := in
	other.CaptureKey = "sess:11111111-2222-3333-4444-555555555555"
	otherID, otherInserted, err := insertCapturedTask(db, other)
	if err != nil {
		t.Fatalf("second key insert: %v", err)
	}
	if !otherInserted || otherID == id {
		t.Errorf("second key = (%d, %v), want a distinct real insert", otherID, otherInserted)
	}

	// Captured cards are readable through the board API with their provenance.
	h := &Handler{DB: db}
	d, err := h.boardTaskByID(id)
	if err != nil || d == nil {
		t.Fatalf("boardTaskByID(%d) = %+v, %v", id, d, err)
	}
	if d.Origin != "session" || d.OriginSessionID == nil || *d.OriginSessionID != sessionID {
		t.Errorf("DTO provenance = %q/%v, want session/%d", d.Origin, d.OriginSessionID, sessionID)
	}
	if d.Agent != nil {
		t.Errorf("captured card agent = %v, want nil", *d.Agent)
	}
}

// TestInsertCapturedTaskPublishesOnlyOnInsert: a replay changed nothing, so it
// must stay off the wire. Emitting task_updated on every re-capture would make
// each re-tail of an old transcript look like live board activity to every
// connected client.
func TestInsertCapturedTaskPublishesOnlyOnInsert(t *testing.T) {
	bus := ingest.NewBus()
	AttachBus(bus)
	t.Cleanup(func() { AttachBus(nil) })

	notes, unsubscribe := bus.Subscribe(8)
	t.Cleanup(unsubscribe)

	_, db := testServerWithDB(t)
	in := capturedTaskInput{
		ProjectID: 1, Title: "publish once", Prompt: "p",
		Origin: "llm", CaptureKey: "llm:pub:once",
	}

	id, inserted, err := insertCapturedTask(db, in)
	if err != nil || !inserted {
		t.Fatalf("first insert = %v, %v; want a real insert", inserted, err)
	}
	select {
	case n := <-notes:
		if n.Type != ingest.NoteTaskUpdated || n.TaskID != id {
			t.Fatalf("first notification = %+v, want task_updated for %d", n, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a real insert published no task_updated")
	}

	// Replay: no insert, therefore no frame.
	if _, inserted, err := insertCapturedTask(db, in); err != nil || inserted {
		t.Fatalf("replay = %v, %v; want inserted=false", inserted, err)
	}
	select {
	case n := <-notes:
		t.Errorf("replay published %+v; a no-op must not reach the wire", n)
	case <-time.After(250 * time.Millisecond):
		// Expected: silence.
	}
}

// TestInsertCapturedTaskValidation: the helper is the internal write path, so
// it reports bad input as an error rather than an HTTP status.
func TestInsertCapturedTaskValidation(t *testing.T) {
	_, db := testServerWithDB(t)

	base := capturedTaskInput{
		ProjectID: 1, Title: "t", Prompt: "p", Origin: "session", CaptureKey: "k-base",
	}
	cases := map[string]func(c *capturedTaskInput){
		"empty title":    func(c *capturedTaskInput) { c.Title = "  " },
		"empty prompt":   func(c *capturedTaskInput) { c.Prompt = "" },
		"missing key":    func(c *capturedTaskInput) { c.CaptureKey = "" },
		"manual origin":  func(c *capturedTaskInput) { c.Origin = "manual" },
		"unknown origin": func(c *capturedTaskInput) { c.Origin = "telepathy" },
	}
	for name, mutate := range cases {
		in := base
		in.CaptureKey = "k-" + name
		mutate(&in)
		if _, _, err := insertCapturedTask(db, in); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}

	// The valid baseline still works, so the cases above failed on their own
	// defect and not on some shared problem with the fixture.
	if _, inserted, err := insertCapturedTask(db, base); err != nil || !inserted {
		t.Errorf("baseline insert = %v, %v; want a clean insert", inserted, err)
	}
}
