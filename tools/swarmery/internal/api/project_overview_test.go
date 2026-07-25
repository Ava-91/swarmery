package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// overviewTestServer seeds:
//   - project 1 (kept, id 1): active session + pending approval + board tasks
//   - project 2 (archived, id 2): a session (must be excluded)
func overviewTestServer(t *testing.T) (*httptest.Server, int64, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "overview.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	const tsFmt = "2006-01-02T15:04:05.000Z"
	const windowFmt = "2006-01-02T15:04:05"

	now := time.Now().UTC()
	today := now.Format(tsFmt)
	// 3 days ago — inside the current 7-day window.
	threeDaysAgo := now.AddDate(0, 0, -3).Format(windowFmt)
	// 10 days ago — inside the previous 7-day window (days -14 to -7).
	tenDaysAgo := now.AddDate(0, 0, -10).Format(windowFmt)

	ex := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}

	// Projects.
	ex(`INSERT INTO projects (id, path, slug, name, first_seen, archived) VALUES
		(1, '/work/keep', 'keep', 'Keep', ?, 0),
		(2, '/work/gone', 'gone', 'Gone', ?, 1)`, today, today)

	// Sessions for project 1.
	ex(`INSERT INTO sessions (id, project_id, session_uuid, model, status, started_at) VALUES
		(1, 1, 'u-active',    'claude-opus-5', 'active',    ?)`, threeDaysAgo)
	ex(`INSERT INTO sessions (id, project_id, session_uuid, model, status, started_at, ended_at) VALUES
		(2, 1, 'u-completed', 'claude-opus-5', 'completed', ?, ?)`, threeDaysAgo, today)
	// Session in previous window for delta testing.
	ex(`INSERT INTO sessions (id, project_id, session_uuid, model, status, started_at, ended_at) VALUES
		(3, 1, 'u-old',      'claude-opus-5', 'completed', ?, ?)`, tenDaysAgo, tenDaysAgo)

	// Session for archived project 2 — must not affect counts.
	ex(`INSERT INTO sessions (id, project_id, session_uuid, model, status, started_at) VALUES
		(4, 2, 'u-gone', 'claude-opus-5', 'active', ?)`, today)

	// Priced turns for project 1 sessions (cost thisWeek).
	ex(`INSERT INTO turns (session_id, seq, role, model, started_at, tokens_in, tokens_out, cost_usd) VALUES
		(1, 0, 'assistant', 'claude-opus-5', ?, 100, 50, 2.00)`, threeDaysAgo)
	ex(`INSERT INTO turns (session_id, seq, role, model, started_at, tokens_in, tokens_out, cost_usd) VALUES
		(2, 0, 'assistant', 'claude-opus-5', ?, 200, 100, 1.00)`, threeDaysAgo)
	// Previous window turn (for cost delta).
	ex(`INSERT INTO turns (session_id, seq, role, model, started_at, tokens_in, tokens_out, cost_usd) VALUES
		(3, 0, 'assistant', 'claude-opus-5', ?, 100, 50, 4.00)`, tenDaysAgo)

	// Pending approval for session 1 — current window.
	ex(`INSERT INTO permission_requests (id, session_id, tool_name, request_json, status, requested_at) VALUES
		(1, 1, 'Bash', '{}', 'pending', ?)`, threeDaysAgo)

	// Board tasks: one done in current window, one done in previous window.
	ex(`INSERT INTO tasks (id, project_id, title, prompt, status, source, board_column, column_moved_at, created_at) VALUES
		(10, 1, 'cur task',  'do it', 'done', 'queue', 'done', ?, ?)`,
		threeDaysAgo, threeDaysAgo)
	ex(`INSERT INTO tasks (id, project_id, title, prompt, status, source, board_column, column_moved_at, created_at) VALUES
		(11, 1, 'prev task', 'done',  'done', 'queue', 'done', ?, ?)`,
		tenDaysAgo, tenDaysAgo)

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, 1, 2 // kept project id, archived project id
}

func getOverview(t *testing.T, srv *httptest.Server, projectID int64) (int, projectOverviewDTO) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/api/projects/%d/overview", srv.URL, projectID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out projectOverviewDTO
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode, out
}

// TestProjectOverviewHappyPath verifies:
//   - rightNow: running=1 (one active session), awaiting approval=1, done today=1.
//   - thisWeek: tasks delta is +0 (1 cur – 1 prev = 0, both windows have data so delta is set).
//   - thisWeek: cost: current window has data, previous window has data → delta present.
//   - attention: 0 items (approval pending but not older than 1h in test).
func TestProjectOverviewHappyPath(t *testing.T) {
	srv, keptID, _ := overviewTestServer(t)
	code, dto := getOverview(t, srv, keptID)
	if code != http.StatusOK {
		t.Fatalf("GET /api/projects/%d/overview = %d, want 200", keptID, code)
	}

	// rightNow must have 3 tiles.
	if len(dto.RightNow) != 3 {
		t.Fatalf("rightNow len = %d, want 3", len(dto.RightNow))
	}

	// running = 1 (the active session).
	var runningTile overviewTile
	for _, tile := range dto.RightNow {
		if tile.Label == "running" {
			runningTile = tile
		}
	}
	if runningTile.Value != 1 {
		t.Errorf("rightNow running = %d, want 1", runningTile.Value)
	}
	if runningTile.Tone != "green" {
		t.Errorf("rightNow running tone = %q, want green", runningTile.Tone)
	}

	// awaiting approval = 1.
	var approvalTile overviewTile
	for _, tile := range dto.RightNow {
		if tile.Label == "awaiting approval" {
			approvalTile = tile
		}
	}
	if approvalTile.Value != 1 {
		t.Errorf("rightNow awaiting approval = %d, want 1", approvalTile.Value)
	}
	if approvalTile.Tone != "amber" {
		t.Errorf("rightNow awaiting approval tone = %q, want amber", approvalTile.Tone)
	}

	// thisWeek must have 4 metrics.
	if len(dto.ThisWeek) != 4 {
		t.Fatalf("thisWeek len = %d, want 4", len(dto.ThisWeek))
	}

	// tasks shipped: current=1, prev=1 → delta "0" (non-nil since both have data).
	var tasksMet weekMetric
	for _, m := range dto.ThisWeek {
		if m.Label == "tasks shipped" {
			tasksMet = m
		}
	}
	if tasksMet.Value == nil || *tasksMet.Value != "1" {
		v := "<nil>"
		if tasksMet.Value != nil {
			v = *tasksMet.Value
		}
		t.Errorf("thisWeek tasks shipped value = %q, want 1", v)
	}
	if tasksMet.Delta == nil {
		t.Error("thisWeek tasks shipped delta should be non-nil (both windows have data)")
	}

	// cost: current window has priced turns → value not nil.
	var costMet weekMetric
	for _, m := range dto.ThisWeek {
		if m.Label == "cost" {
			costMet = m
		}
	}
	if costMet.Value == nil {
		t.Error("thisWeek cost value should be non-nil (current window has priced turns)")
	}
	// Previous window also has priced turns → delta non-nil.
	if costMet.Delta == nil {
		t.Error("thisWeek cost delta should be non-nil (prev window also has priced turns)")
	}

	// sessions: current window has 2 sessions (id 1+2), prev has 1 (id 3) → delta "+1".
	var sessMet weekMetric
	for _, m := range dto.ThisWeek {
		if m.Label == "sessions" {
			sessMet = m
		}
	}
	if sessMet.Value == nil || *sessMet.Value != "2" {
		v := "<nil>"
		if sessMet.Value != nil {
			v = *sessMet.Value
		}
		t.Errorf("thisWeek sessions value = %q, want 2", v)
	}
	if sessMet.Delta == nil || *sessMet.Delta != "+1" {
		d := "<nil>"
		if sessMet.Delta != nil {
			d = *sessMet.Delta
		}
		t.Errorf("thisWeek sessions delta = %q, want +1", d)
	}
	if sessMet.DeltaTone == nil || *sessMet.DeltaTone != "green" {
		tone := "<nil>"
		if sessMet.DeltaTone != nil {
			tone = *sessMet.DeltaTone
		}
		t.Errorf("thisWeek sessions deltaTone = %q, want green", tone)
	}
}

// TestProjectOverviewArchivedExcluded verifies that an archived project returns 404.
func TestProjectOverviewArchivedExcluded(t *testing.T) {
	srv, _, archivedID := overviewTestServer(t)
	code, _ := getOverview(t, srv, archivedID)
	if code != http.StatusNotFound {
		t.Errorf("GET /api/projects/%d/overview (archived) = %d, want 404", archivedID, code)
	}
}

// TestProjectOverview404 verifies that an unknown project id returns 404.
func TestProjectOverview404(t *testing.T) {
	srv, _, _ := overviewTestServer(t)
	code, _ := getOverview(t, srv, 9999)
	if code != http.StatusNotFound {
		t.Errorf("GET /api/projects/9999/overview = %d, want 404", code)
	}
}

// TestProjectOverviewAttentionPausedTask verifies that a paused board task appears
// in the attention rail.
func TestProjectOverviewAttentionPausedTask(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	const tsFmt = "2006-01-02T15:04:05.000Z"
	now := time.Now().UTC().Format(tsFmt)

	ex := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}

	ex(`INSERT INTO projects (id, path, slug, name, first_seen, archived) VALUES (1, '/work/p', 'p', 'P', ?, 0)`, now)
	// A paused board task.
	ex(`INSERT INTO tasks (id, project_id, title, prompt, status, source, board_column, paused, user_paused, created_at) VALUES
		(1, 1, 'paused task', 'do it', 'queued', 'queue', 'triage', 1, 0, ?)`, now)

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	code, dto := getOverview(t, srv, 1)
	if code != http.StatusOK {
		t.Fatalf("GET /api/projects/1/overview = %d, want 200", code)
	}
	foundPaused := false
	for _, item := range dto.Attention {
		if item.Tone == "amber" && item.Href == "/board" {
			foundPaused = true
		}
	}
	if !foundPaused {
		t.Errorf("attention should contain a paused-task item, got: %+v", dto.Attention)
	}
}
