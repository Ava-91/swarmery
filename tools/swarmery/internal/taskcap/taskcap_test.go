package taskcap_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/taskcap"
)

// testDB opens a migrated store and seeds the project + session a captured card
// points at (origin_session_id is a real FK and foreign keys are enforced).
func testDB(t *testing.T) (*sql.DB, int64, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	res, err := db.Exec(
		`INSERT INTO projects (path, slug, first_seen) VALUES ('/tmp/cap-proj', '-tmp-cap-proj', '2026-07-10T00:00:00.000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := res.LastInsertId()
	res, err = db.Exec(
		`INSERT INTO sessions (project_id, session_uuid, status, started_at)
		 VALUES (?, 'cap-session', 'completed', '2026-07-10T00:00:00.000Z')`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := res.LastInsertId()
	return db, projectID, sessionID
}

// TestInsertCapturedTaskIdempotent is the property the whole capture design
// rests on: capture re-reads the same transcript on every re-tail and restart,
// so "insert" has to mean "insert once per capture_key".
func TestInsertCapturedTaskIdempotent(t *testing.T) {
	db, projectID, sessionID := testDB(t)

	in := taskcap.Input{
		ProjectID:       projectID,
		Title:           "extract the retry helper",
		Prompt:          "the session's TODO said to extract it",
		Origin:          "session",
		OriginSessionID: &sessionID,
		CaptureKey:      "todo:cap-session:a1b2c3d4e5f6",
	}

	id, inserted, err := taskcap.InsertCapturedTask(db, in)
	if err != nil || !inserted || id == 0 {
		t.Fatalf("first insert = (%d, %v, %v), want a real insert", id, inserted, err)
	}

	// Replay with a deliberately different title — the KEY wins, not the payload.
	replay := in
	replay.Title = "different title on the replay"
	id2, inserted2, err := taskcap.InsertCapturedTask(db, replay)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if inserted2 {
		t.Error("replay reported inserted=true; capture_key did not dedupe")
	}
	if id2 != id {
		t.Errorf("replay id = %d, want the original %d", id2, id)
	}

	var title, origin, column, source, status string
	var priority int
	var originSession sql.NullInt64
	if err := db.QueryRow(`
		SELECT title, origin, board_column, source, status, priority, origin_session_id
		  FROM tasks WHERE id = ?`, id,
	).Scan(&title, &origin, &column, &source, &status, &priority, &originSession); err != nil {
		t.Fatal(err)
	}
	if title != in.Title {
		t.Errorf("title = %q, want the original %q (a replay is a no-op, not an update)", title, in.Title)
	}
	if origin != "session" || column != "triage" || source != "queue" || status != "queued" {
		t.Errorf("row = %q/%q/%q/%q, want session/triage/queue/queued", origin, column, source, status)
	}
	if priority != taskcap.NormalPriority {
		t.Errorf("priority = %d, want %d", priority, taskcap.NormalPriority)
	}
	if !originSession.Valid || originSession.Int64 != sessionID {
		t.Errorf("origin_session_id = %v, want %d", originSession, sessionID)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM tasks WHERE capture_key = ?`, in.CaptureKey); got != 1 {
		t.Errorf("rows with that key = %d, want exactly 1", got)
	}

	// Dedupe is per key, not global.
	other := in
	other.CaptureKey = "sess:cap-session"
	otherID, otherInserted, err := taskcap.InsertCapturedTask(db, other)
	if err != nil || !otherInserted || otherID == id {
		t.Errorf("second key = (%d, %v, %v), want a distinct real insert", otherID, otherInserted, err)
	}
}

// TestInsertCapturedTaskInsideTransaction: ingest captures inside the tail
// transaction, so a card must never survive a batch that rolls back. This is why
// the parameter is an interface and not *sql.DB.
func TestInsertCapturedTaskInsideTransaction(t *testing.T) {
	db, projectID, sessionID := testDB(t)

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, inserted, err := taskcap.InsertCapturedTask(tx, taskcap.Input{
		ProjectID: projectID, Title: "rolled back", Prompt: "p",
		Origin: "session", OriginSessionID: &sessionID, CaptureKey: "todo:cap-session:rollback",
	}); err != nil || !inserted {
		t.Fatalf("insert in tx = %v, %v; want a real insert", inserted, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM tasks`); got != 0 {
		t.Errorf("tasks after rollback = %d, want 0 — the card outlived its batch", got)
	}

	// The same key is free again afterwards.
	if _, inserted, err := taskcap.InsertCapturedTask(db, taskcap.Input{
		ProjectID: projectID, Title: "committed", Prompt: "p",
		Origin: "session", OriginSessionID: &sessionID, CaptureKey: "todo:cap-session:rollback",
	}); err != nil || !inserted {
		t.Errorf("post-rollback insert = %v, %v; want a real insert", inserted, err)
	}
}

// TestInsertCapturedTaskValidation: this is the internal write path, so bad
// input is an error rather than an HTTP status. 'manual' is not a capture.
func TestInsertCapturedTaskValidation(t *testing.T) {
	db, projectID, sessionID := testDB(t)

	base := taskcap.Input{
		ProjectID: projectID, Title: "t", Prompt: "p",
		Origin: "session", OriginSessionID: &sessionID, CaptureKey: "k-base",
	}
	cases := map[string]func(c *taskcap.Input){
		"empty title":    func(c *taskcap.Input) { c.Title = "  " },
		"empty prompt":   func(c *taskcap.Input) { c.Prompt = "" },
		"missing key":    func(c *taskcap.Input) { c.CaptureKey = "" },
		"blank key":      func(c *taskcap.Input) { c.CaptureKey = "   " },
		"manual origin":  func(c *taskcap.Input) { c.Origin = "manual" },
		"fix origin":     func(c *taskcap.Input) { c.Origin = "verify-fix" },
		"unknown origin": func(c *taskcap.Input) { c.Origin = "telepathy" },
		"empty origin":   func(c *taskcap.Input) { c.Origin = "" },
		"capital origin": func(c *taskcap.Input) { c.Origin = "Session" },
	}
	for name, mutate := range cases {
		in := base
		in.CaptureKey = "k-" + name
		mutate(&in)
		if _, _, err := taskcap.InsertCapturedTask(db, in); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
	// The baseline still works, so the cases above failed on their own defect.
	if _, inserted, err := taskcap.InsertCapturedTask(db, base); err != nil || !inserted {
		t.Errorf("baseline = %v, %v; want a clean insert", inserted, err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM tasks`); got != 1 {
		t.Errorf("tasks = %d, want only the baseline row", got)
	}
}

// TestInsertCapturedTaskTrims: the key and the visible fields are trimmed, so a
// stray newline in a todo does not mint a second card for the same item.
func TestInsertCapturedTaskTrims(t *testing.T) {
	db, projectID, sessionID := testDB(t)

	if _, inserted, err := taskcap.InsertCapturedTask(db, taskcap.Input{
		ProjectID: projectID, Title: "  spaced title  ", Prompt: "  body  ",
		Origin: "llm", OriginSessionID: &sessionID, CaptureKey: "  llm:k1  ",
	}); err != nil || !inserted {
		t.Fatalf("insert = %v, %v", inserted, err)
	}
	var title, prompt, key string
	if err := db.QueryRow(`SELECT title, prompt, capture_key FROM tasks`).Scan(&title, &prompt, &key); err != nil {
		t.Fatal(err)
	}
	if title != "spaced title" || prompt != "body" || key != "llm:k1" {
		t.Errorf("stored (%q, %q, %q), want them trimmed", title, prompt, key)
	}
	if _, inserted, err := taskcap.InsertCapturedTask(db, taskcap.Input{
		ProjectID: projectID, Title: "spaced title", Prompt: "body",
		Origin: "llm", OriginSessionID: &sessionID, CaptureKey: "llm:k1",
	}); err != nil || inserted {
		t.Errorf("untrimmed replay = %v, %v; want the trimmed key to dedupe", inserted, err)
	}
}

// TestValidOrigin pins the closed set one origin at a time: the three from
// 0048 plus the verifier's fix-chain marker, which verify/service.go had been
// writing while this validator rejected it.
func TestValidOrigin(t *testing.T) {
	cases := map[string]bool{
		"manual":     true,
		"session":    true,
		"llm":        true,
		"verify-fix": true,
		"":           false,
		"Session":    false,
		"queue":      false,
		"telepathy":  false,
		"verify_fix": false,
	}
	for o, want := range cases {
		if got := taskcap.ValidOrigin(o); got != want {
			t.Errorf("ValidOrigin(%q) = %v, want %v", o, got, want)
		}
	}
}

// TestInsertCapturedTaskStoresProvenance: the turn, the quote and the files
// land in their own columns (0066) and the prompt is stored as given — capture
// no longer folds the quote into it.
func TestInsertCapturedTaskStoresProvenance(t *testing.T) {
	db, projectID, sessionID := testDB(t)
	id, inserted, err := taskcap.InsertCapturedTask(db, taskcap.Input{
		ProjectID: projectID, Title: "t", Prompt: "body only", Origin: "session",
		OriginSessionID: &sessionID, OriginTurnUUID: "rec-uuid-1",
		OriginQuote: "what the session was asked", OriginFiles: []string{"a.go", "b.go"},
		CaptureKey: "todo:cap-session:prov",
	})
	if err != nil || !inserted {
		t.Fatalf("insert = %v, %v", inserted, err)
	}
	var prompt, turn, quote, files string
	if err := db.QueryRow(
		`SELECT prompt, origin_turn_uuid, origin_quote, origin_files FROM tasks WHERE id = ?`, id,
	).Scan(&prompt, &turn, &quote, &files); err != nil {
		t.Fatal(err)
	}
	if prompt != "body only" || turn != "rec-uuid-1" || quote != "what the session was asked" || files != `["a.go","b.go"]` {
		t.Errorf("stored (%q, %q, %q, %q)", prompt, turn, quote, files)
	}
}

func TestNewExternalID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := taskcap.NewExternalID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 8 || !strings.HasPrefix(id, "T-") {
			t.Fatalf("id = %q, want T- + 6 chars", id)
		}
		if strings.Trim(id[2:], "0123456789abcdefghijklmnopqrstuvwxyz") != "" {
			t.Fatalf("id %q is not base36", id)
		}
		seen[id] = true
	}
	if len(seen) < 60 {
		t.Errorf("%d distinct ids out of 64 — not random enough", len(seen))
	}
}

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}
