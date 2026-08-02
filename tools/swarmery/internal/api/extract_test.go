package api

// Phase 6 — POST /api/sessions/{id}/extract-tasks.
//
// The service's own behaviour is covered in internal/extract; what these tests
// own is the HTTP contract, i.e. the part an operator's browser actually sees:
// which failure becomes which status code, and — the one that costs money if it
// regresses — that an ineligible session is refused BEFORE a model run is paid
// for.
//
// Every test attaches a fake-runner service and detaches it again. A leaked
// attachment would make some other test in this package spawn a real headless
// claude, which is precisely what AttachExtract's nil default exists to prevent.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/extract"
)

// fakeExtractRunner is the model seam: canned output, recorded calls.
type fakeExtractRunner struct {
	out string
	err error
	mu  sync.Mutex
	n   int
}

func (r *fakeExtractRunner) Run(context.Context, string) (string, error) {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
	return r.out, r.err
}

func (r *fakeExtractRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// postExtract POSTs the endpoint for a session id and returns status + body.
func postExtract(t *testing.T, base string, sessionID int64) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		base+"/api/sessions/"+strconv.FormatInt(sessionID, 10)+"/extract-tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// extractTestSession seeds a project + session with an opening prompt and one
// assistant turn — a session eligible for capture, with enough in the DB for a
// non-empty digest. Its own project (not the fixture's) so the dispatched-link
// test can attach a task row without disturbing other tests' rows.
func extractTestSession(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	const (
		path = "/Users/user/work/extract-endpoint-app"
		uuid = "ccbbaa99-1111-4000-8000-000000000001"
	)
	if _, err := db.Exec(
		`INSERT INTO projects (path, slug, name, first_seen) VALUES (?, ?, ?, ?)`,
		path, strings.ReplaceAll(path, "/", "-"), "extract-endpoint-app",
		"2026-07-10T09:00:00.000Z"); err != nil {
		t.Fatal(err)
	}
	var projectID int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE path = ?`, path).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`
		INSERT INTO sessions (project_id, session_uuid, status, started_at, cwd, source)
		VALUES (?, ?, 'active', '2026-07-10T09:00:00.000Z', ?, 'jsonl')`,
		projectID, uuid, path)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO turns (session_id, seq, role, started_at, text)
		VALUES (?, 1, 'assistant', '2026-07-10T09:00:01.000Z', ?)`,
		sessionID, "I refactored the retry helper but left the backoff jitter TODO."); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO events (session_id, ts, type, payload, dedup_key)
		VALUES (?, '2026-07-10T09:00:00.500Z', 'user_prompt', ?, ?)`,
		sessionID, `{"content":"Clean up the retry helper in internal/retry"}`,
		"evt-extract-"+uuid); err != nil {
		t.Fatal(err)
	}
	return sessionID
}

// TestExtractEndpointInsertsAndReportsCount is the happy path: 202 with the
// number of cards the click produced, and the cards really on the board.
func TestExtractEndpointInsertsAndReportsCount(t *testing.T) {
	srv, db := testServerWithDB(t)
	sessionID := extractTestSession(t, db)

	run := &fakeExtractRunner{out: "```json\n[" +
		`{"title":"Add backoff jitter","prompt":"add jitter to internal/retry"},` +
		`{"title":"Document the retry budget","prompt":"write it down"}` +
		"]\n```"}
	svc := &extract.Service{DB: db, Run: run}
	AttachExtract(svc)
	t.Cleanup(func() { AttachExtract(nil) })

	code, body := postExtract(t, srv.URL, sessionID)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %v", code, body)
	}
	if got, ok := body["inserted"].(float64); !ok || int(got) != 2 {
		t.Errorf("inserted = %v, want 2", body["inserted"])
	}
	if body["status"] != "extracted" {
		t.Errorf("status field = %v, want \"extracted\"", body["status"])
	}
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE origin = 'llm' AND origin_session_id = ?`,
		sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("llm cards on the board = %d, want 2", n)
	}

	// Second click over an unchanged session: 202 and an honest 0.
	code, body = postExtract(t, srv.URL, sessionID)
	if code != http.StatusAccepted {
		t.Fatalf("re-run status = %d, want 202", code)
	}
	if got, ok := body["inserted"].(float64); !ok || int(got) != 0 {
		t.Errorf("re-run inserted = %v, want 0", body["inserted"])
	}
}

// TestExtractEndpointGarbageOutputIs502: a model that answered in prose must
// surface as an error the UI can show, not as a silent zero — the two are
// indistinguishable from a count alone.
func TestExtractEndpointGarbageOutputIs502(t *testing.T) {
	srv, db := testServerWithDB(t)
	sessionID := extractTestSession(t, db)

	svc := &extract.Service{DB: db, Run: &fakeExtractRunner{out: "there is nothing to do here really"}}
	AttachExtract(svc)
	t.Cleanup(func() { AttachExtract(nil) })

	code, body := postExtract(t, srv.URL, sessionID)
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %v", code, body)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE origin = 'llm'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("cards after a bad answer = %d, want 0", n)
	}
}

// TestExtractEndpointDispatchedSessionIs409AndCostsNothing is the money test:
// a session capture refuses is refused here too, and the refusal happens BEFORE
// the model runs.
func TestExtractEndpointDispatchedSessionIs409AndCostsNothing(t *testing.T) {
	srv, db := testServerWithDB(t)
	sessionID := extractTestSession(t, db)
	var uuid string
	if err := db.QueryRow(`SELECT session_uuid FROM sessions WHERE id = ?`, sessionID).Scan(&uuid); err != nil {
		t.Fatal(err)
	}
	// The dispatcher parks the uuid on the task row before it spawns.
	if _, err := db.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, dispatch_session_uuid)
		VALUES (1, 'parent', 'p', 5, 'running', '2026-07-10T09:00:00.000Z',
		        'queue', 'T-parent', 'in_progress', ?)`, uuid); err != nil {
		t.Fatal(err)
	}

	run := &fakeExtractRunner{out: "```json\n[]\n```"}
	AttachExtract(&extract.Service{DB: db, Run: run})
	t.Cleanup(func() { AttachExtract(nil) })

	code, body := postExtract(t, srv.URL, sessionID)
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %v", code, body)
	}
	if run.calls() != 0 {
		t.Errorf("the model ran %d time(s) for a refused session — the gate must precede the spend", run.calls())
	}
}

// TestExtractEndpointUnknownSessionIs404.
func TestExtractEndpointUnknownSessionIs404(t *testing.T) {
	srv, db := testServerWithDB(t)
	AttachExtract(&extract.Service{DB: db, Run: &fakeExtractRunner{out: "```json\n[]\n```"}})
	t.Cleanup(func() { AttachExtract(nil) })

	if code, body := postExtract(t, srv.URL, 999999); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %v", code, body)
	}
}

// TestExtractEndpointUnattachedIs503: the daemon started without the service
// (or a unit test left it nil) answers 503 rather than panicking.
func TestExtractEndpointUnattachedIs503(t *testing.T) {
	srv, db := testServerWithDB(t)
	sessionID := extractTestSession(t, db)
	AttachExtract(nil)

	if code, body := postExtract(t, srv.URL, sessionID); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %v", code, body)
	}
}

// TestExtractEndpointRejectsCrossOrigin: the route is a WRITE behind
// requireLocalOrigin — a page on another origin must not be able to spend the
// operator's tokens.
func TestExtractEndpointRejectsCrossOrigin(t *testing.T) {
	srv, db := testServerWithDB(t)
	sessionID := extractTestSession(t, db)
	run := &fakeExtractRunner{out: "```json\n[]\n```"}
	AttachExtract(&extract.Service{DB: db, Run: run})
	t.Cleanup(func() { AttachExtract(nil) })

	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/api/sessions/"+strconv.FormatInt(sessionID, 10)+"/extract-tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin status = %d, want 403", resp.StatusCode)
	}
	if run.calls() != 0 {
		t.Errorf("the model ran %d time(s) for a rejected origin", run.calls())
	}
}

// TestAttachExtractWiresNotify: the service must publish over the api-owned
// emitter, so a suggested card reaches the board on the FROZEN bus rather than
// waiting for the dashboard's 60s reconcile.
func TestAttachExtractWiresNotify(t *testing.T) {
	svc := &extract.Service{}
	AttachExtract(svc)
	t.Cleanup(func() { AttachExtract(nil) })
	if svc.Notify == nil {
		t.Error("AttachExtract left Notify nil — new cards would never be announced")
	}
	// Nil-safe: the composition root may hand over nothing (serve --no-ingest).
	AttachExtract(nil)
	if extractSvc != nil {
		t.Error("AttachExtract(nil) did not clear the service")
	}
}

// TestExtractSkipErrorMapsTo409 pins the error→status mapping directly, so the
// endpoint's switch cannot silently start reporting a scope refusal as a 502
// (which would read to an operator as "the model is broken").
func TestExtractSkipErrorMapsTo409(t *testing.T) {
	var skipped *extract.ErrSkipped
	err := error(&extract.ErrSkipped{Reason: "System-project sessions are the daemon's own bookkeeping"})
	if !errors.As(err, &skipped) {
		t.Fatal("ErrSkipped is not matchable with errors.As — the handler's branch would fall through to 502")
	}
	if !strings.Contains(err.Error(), "bookkeeping") {
		t.Error("the reason is not carried in the error text")
	}
}
