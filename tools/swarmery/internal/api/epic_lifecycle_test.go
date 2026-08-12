package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planrev"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/wsingest"
)

// lifecycleFixture builds a server over a REAL scanned temp workspace (one
// epic task working/2026/07/20/demo with a card README + plan/ dir), so the
// lifecycle file operations run against the exact layout wsingest indexes.
// Returns the server, db, the task id, and the workspace/ dir (parent of
// working/ + archive/).
func lifecycleFixture(t *testing.T) (*httptest.Server, *sql.DB, int64, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	root := t.TempDir()
	wsDir := filepath.Join(root, "demo", "workspace")
	taskDir := filepath.Join(wsDir, "working", "2026", "07", "20", "demo")
	planDir := filepath.Join(taskDir, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(taskDir, "README.md"): "# Task: Demo epic\n\n" +
			"- **Статус**: active\n- **Старт**: 2026-07-20 · **Завершено**: —\n- **Ціль**: demo goal\n",
		filepath.Join(planDir, "README.md"): "# Demo plan\n\n" +
			"| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
			"| 1 | Demo | `phase-1-demo.md` | — |\n",
		filepath.Join(planDir, "phase-1-demo.md"): "# Phase 1 — Demo\n\n" +
			"## Acceptance criteria\n- [x] a\n- [ ] b\n",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := wsingest.New(db, wsingest.Config{WorkspaceRoot: root}).Scan(); err != nil {
		t.Fatalf("workspace scan: %v", err)
	}
	var taskID int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE external_id='2026-07-20-demo'`).Scan(&taskID); err != nil {
		t.Fatalf("demo task row: %v", err)
	}

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db, taskID, wsDir
}

// postLifecycle issues the action and returns (statusCode, derived status).
func postLifecycle(t *testing.T, url string, taskID int64, action string) (int, string) {
	t.Helper()
	resp, err := http.Post(url+"/api/epics/"+itoa(taskID)+"/lifecycle",
		"application/json", strings.NewReader(`{"action":"`+action+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body.Status
}

// epicStatus fetches the task's derived status from GET /api/epics.
func epicStatus(t *testing.T, url string, taskID int64) string {
	t.Helper()
	var epics []epicDTO
	getJSON(t, url+"/api/epics", &epics)
	for _, e := range epics {
		if e.TaskID == taskID {
			return e.Status
		}
	}
	t.Fatalf("task %d not in GET /api/epics", taskID)
	return ""
}

// TestEpicLifecycleRoundTrip drives pause → resume → archive → restore against
// the scanned workspace, asserting README rewrites, the zone move + parent
// pruning, direct tasks-row updates, the derived status in GET /api/epics, and
// exactly one plan_updated bus frame per action.
func TestEpicLifecycleRoundTrip(t *testing.T) {
	bus := ingest.NewBus()
	AttachBus(bus)
	t.Cleanup(func() { AttachBus(nil) })

	srv, db, taskID, wsDir := lifecycleFixture(t)
	workingDir := filepath.Join(wsDir, "working", "2026", "07", "20", "demo")
	archiveDir := filepath.Join(wsDir, "archive", "2026", "07", "20", "demo")

	frames, cancel := bus.Subscribe(64)
	t.Cleanup(cancel)
	expectFrame := func(step string) {
		t.Helper()
		select {
		case n := <-frames:
			if n.Type != ingest.NotePlanUpdated || n.TaskID != taskID {
				t.Errorf("%s: frame = %+v, want plan_updated task %d", step, n, taskID)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s: no plan_updated bus frame", step)
		}
		// Exactly one frame per action — the channel must now be empty.
		select {
		case n := <-frames:
			t.Errorf("%s: unexpected extra frame %+v", step, n)
		default:
		}
	}
	readme := func(dir string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("README: %v", err)
		}
		return string(raw)
	}
	taskRow := func() (status string, archived bool) {
		t.Helper()
		var archivedAt sql.NullString
		if err := db.QueryRow(`SELECT status, archived_at FROM tasks WHERE id=?`, taskID).
			Scan(&status, &archivedAt); err != nil {
			t.Fatal(err)
		}
		return status, archivedAt.Valid
	}

	// ── pause ──
	code, derived := postLifecycle(t, srv.URL, taskID, "pause")
	if code != http.StatusOK || derived != "paused" {
		t.Fatalf("pause = %d %q, want 200 paused", code, derived)
	}
	if got := readme(workingDir); !strings.Contains(got, "**Статус**: paused") {
		t.Errorf("pause README = %q, want status paused", got)
	}
	if entries, err := os.ReadDir(filepath.Join(workingDir, ".backups")); err != nil || len(entries) == 0 {
		t.Errorf("pause left no README backup under .backups: %v", err)
	}
	if status, archived := taskRow(); status != "paused" || archived {
		t.Errorf("pause row = %s/%v, want paused/false", status, archived)
	}
	if got := epicStatus(t, srv.URL, taskID); got != "paused" {
		t.Errorf("GET /api/epics after pause = %q, want paused", got)
	}
	expectFrame("pause")

	// ── resume ──
	code, derived = postLifecycle(t, srv.URL, taskID, "resume")
	if code != http.StatusOK || derived != "active" {
		t.Fatalf("resume = %d %q, want 200 active", code, derived)
	}
	if got := readme(workingDir); !strings.Contains(got, "**Статус**: active") {
		t.Errorf("resume README = %q, want status active", got)
	}
	if status, archived := taskRow(); status != "running" || archived {
		t.Errorf("resume row = %s/%v, want running/false", status, archived)
	}
	if got := epicStatus(t, srv.URL, taskID); got != "active" {
		t.Errorf("GET /api/epics after resume = %q, want active", got)
	}
	expectFrame("resume")

	// ── archive ──
	code, derived = postLifecycle(t, srv.URL, taskID, "archive")
	if code != http.StatusOK || derived != "archived" {
		t.Fatalf("archive = %d %q, want 200 archived", code, derived)
	}
	if _, err := os.Stat(archiveDir); err != nil {
		t.Fatalf("task dir not moved to archive zone: %v", err)
	}
	// Now-empty working/2026 date parents are pruned; working/ itself stays.
	if _, err := os.Stat(filepath.Join(wsDir, "working", "2026")); !os.IsNotExist(err) {
		t.Errorf("working/2026 not pruned after archive (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "working")); err != nil {
		t.Errorf("working/ zone root must survive the prune: %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got := readme(archiveDir); !strings.Contains(got, "**Статус**: done") ||
		!strings.Contains(got, "**Завершено**: "+today) {
		t.Errorf("archive README = %q, want status done + completion date %s", got, today)
	}
	if status, archived := taskRow(); status != "done" || !archived {
		t.Errorf("archive row = %s/%v, want done/true", status, archived)
	}
	// The plan artifact + phase doc paths moved with the dir (doc endpoints
	// must keep resolving without waiting for a rescan).
	var planPath string
	if err := db.QueryRow(`SELECT path FROM task_artifacts WHERE task_id=? AND kind='plan'`, taskID).
		Scan(&planPath); err != nil {
		t.Fatal(err)
	}
	if planPath != filepath.Join(archiveDir, "plan") {
		t.Errorf("plan artifact path = %q, want it under the archive dir", planPath)
	}
	var docPath string
	if err := db.QueryRow(`SELECT doc_path FROM epic_phases WHERE workspace_task_id=?`, taskID).
		Scan(&docPath); err != nil {
		t.Fatal(err)
	}
	if docPath != filepath.Join(archiveDir, "plan", "phase-1-demo.md") {
		t.Errorf("phase doc_path = %q, want it under the archive dir", docPath)
	}
	if got := epicStatus(t, srv.URL, taskID); got != "archived" {
		t.Errorf("GET /api/epics after archive = %q, want archived", got)
	}
	expectFrame("archive")

	// ── restore ──
	code, derived = postLifecycle(t, srv.URL, taskID, "restore")
	if code != http.StatusOK || derived != "active" {
		t.Fatalf("restore = %d %q, want 200 active", code, derived)
	}
	if _, err := os.Stat(workingDir); err != nil {
		t.Fatalf("task dir not moved back to working zone: %v", err)
	}
	if got := readme(workingDir); !strings.Contains(got, "**Статус**: active") ||
		!strings.Contains(got, "**Завершено**: —") {
		t.Errorf("restore README = %q, want status active + cleared date", got)
	}
	if status, archived := taskRow(); status != "running" || archived {
		t.Errorf("restore row = %s/%v, want running/false", status, archived)
	}
	if got := epicStatus(t, srv.URL, taskID); got != "active" {
		t.Errorf("GET /api/epics after restore = %q, want active", got)
	}
	expectFrame("restore")

	// A follow-up rescan converges (idempotent upsert on workspace_id +
	// external_id — no duplicate task rows, paths already fresh). NotifyPlan
	// must stay silent: the endpoint already wrote the moved
	// task_artifacts.path directly, so the rescan hits the unchanged
	// hash+path gate — no duplicate plan_updated notification.
	var rescanFired []int64
	if _, err := wsingest.New(db, wsingest.Config{
		WorkspaceRoot: filepath.Dir(filepath.Dir(wsDir)),
		NotifyPlan:    func(id int64) { rescanFired = append(rescanFired, id) },
	}).Scan(); err != nil {
		t.Fatalf("converging rescan: %v", err)
	}
	if len(rescanFired) != 0 {
		t.Errorf("converging rescan fired NotifyPlan for %v, want none (hash+path unchanged)", rescanFired)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE external_id='2026-07-20-demo'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("tasks rows after round-trip + rescan = %d, want 1", n)
	}
}

// TestEpicLifecycleErrors covers the 400/404/409 semantics.
func TestEpicLifecycleErrors(t *testing.T) {
	srv, _, taskID, _ := lifecycleFixture(t)

	// Unknown action / malformed body → 400.
	if code, _ := postLifecycle(t, srv.URL, taskID, "explode"); code != http.StatusBadRequest {
		t.Errorf("unknown action = %d, want 400", code)
	}
	resp, err := http.Post(srv.URL+"/api/epics/"+itoa(taskID)+"/lifecycle",
		"application/json", strings.NewReader(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed body = %d, want 400", resp.StatusCode)
	}

	// Unknown / non-workspace task → 404.
	if code, _ := postLifecycle(t, srv.URL, 999999, "pause"); code != http.StatusNotFound {
		t.Errorf("unknown task = %d, want 404", code)
	}

	// restore on a non-archived plan → 409.
	if code, _ := postLifecycle(t, srv.URL, taskID, "restore"); code != http.StatusConflict {
		t.Errorf("restore non-archived = %d, want 409", code)
	}

	// Archive, then every non-restore action → 409.
	if code, _ := postLifecycle(t, srv.URL, taskID, "archive"); code != http.StatusOK {
		t.Fatalf("archive = %d, want 200", code)
	}
	for _, action := range []string{"pause", "resume", "archive"} {
		if code, _ := postLifecycle(t, srv.URL, taskID, action); code != http.StatusConflict {
			t.Errorf("%s on archived = %d, want 409", action, code)
		}
	}
	// restore brings it back.
	if code, derived := postLifecycle(t, srv.URL, taskID, "restore"); code != http.StatusOK || derived != "active" {
		t.Errorf("restore = %d %q, want 200 active", code, derived)
	}
}

// TestEpicLifecycleCrossOrigin: the write rejects a foreign Origin (D4).
func TestEpicLifecycleCrossOrigin(t *testing.T) {
	srv, _, taskID, _ := lifecycleFixture(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/epics/"+itoa(taskID)+"/lifecycle",
		strings.NewReader(`{"action":"pause"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin lifecycle POST = %d, want 403", resp.StatusCode)
	}
}

// TestWSPlanUpdatedShape: a plan_updated bus note hydrates into the frozen
// thin payload {taskId, projectId} — and a vanished row skips the frame.
func TestWSPlanUpdatedShape(t *testing.T) {
	bus := ingest.NewBus()
	AttachBus(bus)
	t.Cleanup(func() { AttachBus(nil) })

	srv, db, taskID, _ := lifecycleFixture(t)

	h := &Handler{DB: db}
	if gone, err := h.planUpdatedPayload(424242); err != nil || gone != nil {
		t.Errorf("planUpdatedPayload(missing) = %+v, %v; want nil, nil", gone, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "/api/ws"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	readFrame := newFrameReader(t, ctx, c, func() {
		bus.Publish(ingest.Notification{Type: ingest.NotePlanUpdated, TaskID: taskID})
	})
	frame := readFrame()
	assertEnvelope(t, frame, "plan_updated")
	assertPayloadKeys(t, frame, []string{"taskId", "projectId"})
	var p wsPlanPayload
	if err := json.Unmarshal(frame["payload"], &p); err != nil {
		t.Fatal(err)
	}
	if p.TaskID != taskID || p.ProjectID == 0 {
		t.Errorf("plan_updated payload = %+v, want task %d with its project", p, taskID)
	}
}

// TestLifecycleRewritesRevisionPlanDir stages a revision, drives archive →
// restore over HTTP, and asserts plan_revisions.plan_dir follows the zone move
// both ways — and that the revision still APPLIES afterwards (the content
// moved with the dir, so the base hashes still match).
func TestLifecycleRewritesRevisionPlanDir(t *testing.T) {
	srv, db, taskID, wsDir := lifecycleFixture(t)
	workingPlan := filepath.Join(wsDir, "working", "2026", "07", "20", "demo", "plan")
	archivePlan := filepath.Join(wsDir, "archive", "2026", "07", "20", "demo", "plan")

	live, err := os.ReadFile(filepath.Join(workingPlan, "phase-1-demo.md"))
	if err != nil {
		t.Fatal(err)
	}
	proposed := "# Phase 1 — Demo\n\n## Acceptance criteria\n- [x] a\n- [x] b\n\n## Completion Report\n"
	revID, err := planrev.Insert(db, planrev.Revision{
		WorkspaceTaskID: taskID,
		PlanDir:         workingPlan,
		Reason:          "tick b",
		CreatedAt:       "2026-08-11T00:00:00Z",
	}, []planrev.File{{
		DocPath:  "phase-1-demo.md",
		Action:   planrev.ActionUpdate,
		BaseHash: planrev.Sha256Hex(live),
		Proposed: proposed,
	}})
	if err != nil {
		t.Fatalf("stage revision: %v", err)
	}

	revisionPlanDir := func() string {
		t.Helper()
		var dir string
		if err := db.QueryRow(`SELECT plan_dir FROM plan_revisions WHERE id = ?`, revID).Scan(&dir); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	if code, _ := postLifecycle(t, srv.URL, taskID, "archive"); code != http.StatusOK {
		t.Fatalf("archive: %d", code)
	}
	if got := revisionPlanDir(); got != archivePlan {
		t.Fatalf("after archive plan_dir = %q, want %q", got, archivePlan)
	}

	if code, _ := postLifecycle(t, srv.URL, taskID, "restore"); code != http.StatusOK {
		t.Fatalf("restore: %d", code)
	}
	if got := revisionPlanDir(); got != workingPlan {
		t.Fatalf("after restore plan_dir = %q, want %q", got, workingPlan)
	}

	// The round-tripped revision is still applyable: the dir moved back, the
	// bytes never changed, so the base hash still pins the live file.
	if n, err := planrev.Apply(db, revID, time.Now, nil); err != nil || n != 1 {
		t.Fatalf("apply after round trip: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(workingPlan, "phase-1-demo.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != proposed {
		t.Fatalf("applied doc:\n%s", got)
	}
}
