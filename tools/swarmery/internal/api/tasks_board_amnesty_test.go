package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// postBulkArchive posts an amnesty request and hands back the raw response.
func postBulkArchive(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/api/board/tasks/bulk-archive", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type amnestyResult struct {
	Matched  int64 `json:"matched"`
	Archived int64 `json:"archived"`
}

func decodeAmnesty(t *testing.T, resp *http.Response) amnestyResult {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk-archive status = %d, want 200", resp.StatusCode)
	}
	var out amnestyResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// seedAmnestyCard inserts one board row directly. Raw SQL because the cases
// need shapes no endpoint can produce (a captured card, a dispatcher-owned
// card) and timestamps in the past.
func seedAmnestyCard(t *testing.T, db *sql.DB, projectID int64, extID, origin, column, createdAt string, worktree any) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, file_scope, dependencies,
		                   origin, worktree_path)
		VALUES (?, ?, 'p', 5, 'queued', ?, 'queue', ?, ?, '[]', '[]', ?, ?)`,
		projectID, extID, createdAt, extID, column, origin, worktree)
	if err != nil {
		t.Fatalf("seed %s: %v", extID, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func boardColumnOf(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var col string
	if err := db.QueryRow(`SELECT board_column FROM tasks WHERE id = ?`, id).Scan(&col); err != nil {
		t.Fatalf("read task %d: %v", id, err)
	}
	return col
}

// TestBulkArchiveDryRunThenExecute is the amnesty flow the banner drives: count
// first so the operator sees the blast radius, then execute the identical
// predicate. The dry run must leave every row exactly where it was.
func TestBulkArchiveDryRunThenExecute(t *testing.T) {
	srv, db := testServerWithDB(t)

	old := "2026-01-01T00:00:00.000Z"
	// Three eligible captured cards — note column_moved_at is never written,
	// which is the shape capture actually produces.
	a := seedAmnestyCard(t, db, 1, "T-am0001", "session", "triage", old, nil)
	b := seedAmnestyCard(t, db, 1, "T-am0002", "llm", "triage", old, nil)
	c := seedAmnestyCard(t, db, 1, "T-am0003", "session", "triage", old, nil)

	body := `{"projectId":1,"column":"triage","before":"2026-06-01T00:00:00Z","dryRun":true}`
	dry := decodeAmnesty(t, postBulkArchive(t, srv.URL, body))
	if dry.Matched != 3 || dry.Archived != 0 {
		t.Fatalf("dry run = %+v, want matched 3 / archived 0", dry)
	}
	for _, id := range []int64{a, b, c} {
		if col := boardColumnOf(t, db, id); col != "triage" {
			t.Errorf("dry run moved task %d to %q — it must write nothing", id, col)
		}
	}

	real := decodeAmnesty(t, postBulkArchive(t, srv.URL,
		`{"projectId":1,"column":"triage","before":"2026-06-01T00:00:00Z"}`))
	if real.Matched != 3 || real.Archived != 3 {
		t.Fatalf("execute = %+v, want matched 3 / archived 3", real)
	}
	for _, id := range []int64{a, b, c} {
		var col, status string
		var archivedAt sql.NullString
		if err := db.QueryRow(
			`SELECT board_column, status, archived_at FROM tasks WHERE id = ?`, id,
		).Scan(&col, &status, &archivedAt); err != nil {
			t.Fatal(err)
		}
		if col != "archived" || status != "cancelled" || !archivedAt.Valid {
			t.Errorf("task %d = %s/%s/%v, want archived/cancelled/dated", id, col, status, archivedAt)
		}
	}

	// Idempotent: the rows left triage, so a repeat matches nothing.
	again := decodeAmnesty(t, postBulkArchive(t, srv.URL,
		`{"projectId":1,"column":"triage","before":"2026-06-01T00:00:00Z"}`))
	if again.Matched != 0 || again.Archived != 0 {
		t.Errorf("repeat = %+v, want 0/0", again)
	}
}

// TestBulkArchiveExclusions: the amnesty stands on the SAME predicate as the
// TTL sweeper (taskcap.StaleInboxWhere), so a hand-written card, a card the
// dispatcher owns, an already-accepted card and a card younger than the cutoff
// all survive it.
func TestBulkArchiveExclusions(t *testing.T) {
	srv, db := testServerWithDB(t)

	old := "2026-01-01T00:00:00.000Z"
	manual := seedAmnestyCard(t, db, 1, "T-ax0001", "manual", "triage", old, nil)
	owned := seedAmnestyCard(t, db, 1, "T-ax0002", "session", "triage", old, "/tmp/wt/x")
	accepted := seedAmnestyCard(t, db, 1, "T-ax0003", "session", "todo", old, nil)
	fresh := seedAmnestyCard(t, db, 1, "T-ax0004", "session", "triage", "2026-07-15T00:00:00.000Z", nil)
	doomed := seedAmnestyCard(t, db, 1, "T-ax0005", "session", "triage", old, nil)

	got := decodeAmnesty(t, postBulkArchive(t, srv.URL,
		`{"projectId":1,"column":"triage","before":"2026-06-01T00:00:00Z"}`))
	if got.Archived != 1 {
		t.Fatalf("archived = %d, want exactly the 1 eligible card", got.Archived)
	}
	for name, id := range map[string]int64{"manual": manual, "worktree-owned": owned, "accepted": accepted, "fresh": fresh} {
		if col := boardColumnOf(t, db, id); col == "archived" {
			t.Errorf("%s card was archived — it must be excluded", name)
		}
	}
	if col := boardColumnOf(t, db, doomed); col != "archived" {
		t.Errorf("eligible card = %q, want archived", col)
	}
}

// TestBulkArchiveProjectScope: omitting projectId is the "all projects" form,
// and passing one must not reach across the fence.
func TestBulkArchiveProjectScope(t *testing.T) {
	srv, db := testServerWithDB(t)
	res, err := db.Exec(
		`INSERT INTO projects (path, slug, first_seen) VALUES ('/tmp/other', '-tmp-other', '2026-01-01T00:00:00.000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()

	old := "2026-01-01T00:00:00.000Z"
	mine := seedAmnestyCard(t, db, 1, "T-ap0001", "session", "triage", old, nil)
	theirs := seedAmnestyCard(t, db, other, "T-ap0002", "session", "triage", old, nil)

	scoped := decodeAmnesty(t, postBulkArchive(t, srv.URL,
		`{"projectId":1,"column":"triage","before":"2026-06-01T00:00:00Z","dryRun":true}`))
	if scoped.Matched != 1 {
		t.Errorf("scoped dry run matched %d, want only this project's 1 card", scoped.Matched)
	}
	global := decodeAmnesty(t, postBulkArchive(t, srv.URL,
		`{"column":"triage","before":"2026-06-01T00:00:00Z","dryRun":true}`))
	if global.Matched != 2 {
		t.Errorf("unscoped dry run matched %d, want both projects' cards", global.Matched)
	}

	if got := decodeAmnesty(t, postBulkArchive(t, srv.URL,
		`{"projectId":1,"column":"triage","before":"2026-06-01T00:00:00Z"}`)); got.Archived != 1 {
		t.Fatalf("scoped execute = %+v, want 1", got)
	}
	if col := boardColumnOf(t, db, mine); col != "archived" {
		t.Errorf("in-scope card = %q, want archived", col)
	}
	if col := boardColumnOf(t, db, theirs); col != "triage" {
		t.Errorf("out-of-scope card = %q, want it untouched", col)
	}
}

// TestBulkArchiveValidation: a bulk write with a fat finger on the predicate is
// the one mistake there is no undo for, so every ambiguous body is a loud 400
// rather than a broadened sweep.
func TestBulkArchiveValidation(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := seedAmnestyCard(t, db, 1, "T-av0001", "session", "triage", "2026-01-01T00:00:00.000Z", nil)

	for name, body := range map[string]string{
		"missing column":  `{"projectId":1,"before":"2026-06-01T00:00:00Z"}`,
		"non-triage":      `{"projectId":1,"column":"todo","before":"2026-06-01T00:00:00Z"}`,
		"unknown column":  `{"projectId":1,"column":"backlog","before":"2026-06-01T00:00:00Z"}`,
		"missing before":  `{"projectId":1,"column":"triage"}`,
		"blank before":    `{"projectId":1,"column":"triage","before":"   "}`,
		"unparsed before": `{"projectId":1,"column":"triage","before":"last tuesday"}`,
		"not json":        `nope`,
	} {
		resp := postBulkArchive(t, srv.URL, body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
	if col := boardColumnOf(t, db, id); col != "triage" {
		t.Errorf("a rejected request archived a card (now %q)", col)
	}
}

// TestBulkArchiveOriginFenced: the endpoint is a localhost write like every
// other board mutation, so a foreign browser Origin is refused before it runs.
func TestBulkArchiveOriginFenced(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := seedAmnestyCard(t, db, 1, "T-ao0001", "session", "triage", "2026-01-01T00:00:00.000Z", nil)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/board/tasks/bulk-archive",
		strings.NewReader(`{"projectId":1,"column":"triage","before":"2026-06-01T00:00:00Z"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("foreign origin status = %d, want a refusal", resp.StatusCode)
	}
	if col := boardColumnOf(t, db, id); col != "triage" {
		t.Errorf("foreign-origin request archived a card (now %q)", col)
	}
}
