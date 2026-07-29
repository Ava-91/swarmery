package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/dispatch"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/phaserun"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// phaseStubRunner implements phaserun.Runner without spawning a process.
// block, when set, holds the run in flight (a Cancel unblocks it via ctx).
type phaseStubRunner struct {
	mu    sync.Mutex
	block chan struct{}
}

func (r *phaseStubRunner) Start(ctx context.Context, spec phaserun.RunSpec) (*phaserun.Run, error) {
	r.mu.Lock()
	block := r.block
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return &phaserun.Run{SessionUUID: spec.SessionUUID, ExitCode: -1}, nil
		}
	}
	return &phaserun.Run{SessionUUID: spec.SessionUUID, ExitCode: 0}, nil
}

// phaseStubWt is a no-op WorktreeManager.
type phaseStubWt struct{}

func (phaseStubWt) Acquire(repoRoot, projectSlug, taskID string) (worktree.Acquired, error) {
	return worktree.Acquired{Path: "/wt/" + projectSlug + "/" + taskID, Branch: "swarm/" + taskID}, nil
}
func (phaseStubWt) Remove(repoRoot string, a worktree.Acquired, keepBranch bool) error { return nil }

// No leftover branch in the api tests: reclaim always reports "nothing to do",
// so Start proceeds straight to Acquire.
func (phaseStubWt) ReclaimEmptyBranch(repoRoot, branch string) (int, error) { return 0, nil }
func (phaseStubWt) DeleteBranch(repoRoot, branch string) error              { return nil }

// attachPhaseRun wires a stub-backed phaserun service (package var, reset on
// cleanup). sync=true runs the spawn inline so a POST response implies the run
// finished (deterministic end-state assertions).
func attachPhaseRun(t *testing.T, db *sql.DB, r phaserun.Runner, sync bool) *phaserun.Service {
	t.Helper()
	return attachPhaseRunWt(t, db, r, sync, phaseStubWt{})
}

// attachPhaseRunWt is attachPhaseRun with an explicit worktree manager, for the
// branch-lifecycle paths (dirty reclaim, DeleteRunBranch).
func attachPhaseRunWt(t *testing.T, db *sql.DB, r phaserun.Runner, sync bool, wt dispatch.WorktreeManager) *phaserun.Service {
	t.Helper()
	svc := phaserun.NewService(db, r, wt)
	svc.UUID = func() string { return "phase-uuid-1" }
	if sync {
		svc.Go = func(fn func()) { fn() }
	}
	AttachPhaseRun(svc)
	t.Cleanup(func() { AttachPhaseRun(nil) })
	return svc
}

// fixturePhaseIDs reads the two phase ids the epicFixture inserted (seq order).
func fixturePhaseIDs(t *testing.T, db *sql.DB, taskID int64) (int64, int64) {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM epic_phases WHERE workspace_task_id=? ORDER BY seq`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 2 {
		t.Fatalf("fixture phases = %d, want 2", len(ids))
	}
	return ids[0], ids[1]
}

// stampRunBranch records the branch a run used (migration 0043), which is what
// DeleteRunBranch reads. Fixture phases have never run, so anything exercising the
// branch endpoint has to say which branch it is talking about — the service refuses to
// re-derive one from the row id.
func stampRunBranch(t *testing.T, db *sql.DB, phaseID int64, branch string) {
	t.Helper()
	if _, err := db.Exec(
		`UPDATE epic_phases SET run_state='done', run_branch=? WHERE id=?`, branch, phaseID); err != nil {
		t.Fatal(err)
	}
}

func postPhase(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func phaseRunURL(srv *httptest.Server, taskID, phaseID int64) string {
	return srv.URL + "/api/epics/" + i64(taskID) + "/phases/" + i64(phaseID) + "/run"
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }

func TestPhaseRun_NotAttached_503(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	AttachPhaseRun(nil)
	resp := postPhase(t, phaseRunURL(srv, taskID, p1))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPhaseRun_UnknownPhase_404(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)
	resp := postPhase(t, phaseRunURL(srv, taskID, 99999))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPhaseRun_TaskMismatch_404(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)
	resp := postPhase(t, phaseRunURL(srv, taskID+999, p1))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPhaseRun_Accepted_AndDTOCarriesRunFields(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)

	resp := postPhase(t, phaseRunURL(srv, taskID, p1))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var body struct {
		Status      string `json:"status"`
		SessionUUID string `json:"sessionUuid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "running" || body.SessionUUID != "phase-uuid-1" {
		t.Errorf("body = %+v", body)
	}

	// The epic list DTO carries the run fields (sync spawn ⇒ the run is done).
	listResp, err := http.Get(srv.URL + "/api/epics?projectId=1")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var epics []epicDTO
	if err := json.NewDecoder(listResp.Body).Decode(&epics); err != nil {
		t.Fatal(err)
	}
	if len(epics) != 1 || len(epics[0].Phases) != 2 {
		t.Fatalf("epics = %+v", epics)
	}
	ph := epics[0].Phases[0]
	if ph.RunState != "done" {
		t.Errorf("runState = %q, want done", ph.RunState)
	}
	if ph.RunSessionUUID == nil || *ph.RunSessionUUID != "phase-uuid-1" {
		t.Errorf("runSessionUuid = %v", ph.RunSessionUUID)
	}
	if ph.RunStartedAt == nil || *ph.RunStartedAt == "" {
		t.Error("runStartedAt missing")
	}
	if ph.RunError != nil {
		t.Errorf("runError = %v, want nil", *ph.RunError)
	}
	// An untouched phase reads idle.
	if epics[0].Phases[1].RunState != "idle" {
		t.Errorf("phase 2 runState = %q, want idle", epics[0].Phases[1].RunState)
	}
}

func TestPhaseRun_DepsUnmet_409(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	_, p2 := fixturePhaseIDs(t, db, taskID)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)

	// epicFixture's phase 1 is 1/2 checkboxes and idle — phase 2's dep is unmet.
	resp := postPhase(t, phaseRunURL(srv, taskID, p2))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body struct {
		Error     string `json:"error"`
		UnmetDeps []int  `json:"unmetDeps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.UnmetDeps) != 1 || body.UnmetDeps[0] != 1 {
		t.Errorf("unmetDeps = %v, want [1]", body.UnmetDeps)
	}
	if !strings.Contains(body.Error, "unmet") {
		t.Errorf("error = %q, want it to name unmet deps", body.Error)
	}
}

func TestPhaseRun_AlreadyRunning_409(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)
	if _, err := db.Exec(`UPDATE epic_phases SET run_state='running' WHERE id=?`, p1); err != nil {
		t.Fatal(err)
	}
	resp := postPhase(t, phaseRunURL(srv, taskID, p1))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestPhaseRun_NoDoc_409(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)
	if _, err := db.Exec(`UPDATE epic_phases SET doc_path='/nope/gone.md' WHERE id=?`, p1); err != nil {
		t.Fatal(err)
	}
	resp := postPhase(t, phaseRunURL(srv, taskID, p1))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestPhaseRun_NoProjectPath_409(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)
	if _, err := db.Exec(`UPDATE projects SET path='' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	resp := postPhase(t, phaseRunURL(srv, taskID, p1))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestPhaseRunCancel_NothingRunning_409(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	attachPhaseRun(t, db, &phaseStubRunner{}, true)
	resp := postPhase(t, phaseRunURL(srv, taskID, p1)+"/cancel")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestPhaseRunCancel_NotAttached_503(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	AttachPhaseRun(nil)
	resp := postPhase(t, phaseRunURL(srv, taskID, p1)+"/cancel")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPhaseRunCancel_InFlight_202(t *testing.T) {
	srv, db, taskID, _ := epicFixture(t)
	p1, _ := fixturePhaseIDs(t, db, taskID)
	r := &phaseStubRunner{block: make(chan struct{})}
	attachPhaseRun(t, db, r, false) // real goroutine — run stays in flight

	if resp := postPhase(t, phaseRunURL(srv, taskID, p1)); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("run status = %d, want 202", resp.StatusCode)
	}
	// Wait for the running stamp, then cancel.
	waitState := func(want string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			var st string
			if err := db.QueryRow(`SELECT run_state FROM epic_phases WHERE id=?`, p1).Scan(&st); err == nil && st == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("run_state never reached %q", want)
	}
	waitState("running")
	resp := postPhase(t, phaseRunURL(srv, taskID, p1)+"/cancel")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status = %d, want 202", resp.StatusCode)
	}
	waitState("failed")
	var runErr sql.NullString
	if err := db.QueryRow(`SELECT run_error FROM epic_phases WHERE id=?`, p1).Scan(&runErr); err != nil {
		t.Fatal(err)
	}
	if runErr.String != "cancelled" {
		t.Errorf("run_error = %q, want cancelled", runErr.String)
	}
}
