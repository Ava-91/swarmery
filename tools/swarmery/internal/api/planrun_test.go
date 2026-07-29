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
	"sync"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planrun"
)

// planrunStubRunner implements planrun.Runner without spawning a process. block,
// when set, holds the run in flight (a Cancel unblocks it via ctx).
type planrunStubRunner struct {
	mu    sync.Mutex
	block chan struct{}
	specs []planrun.RunSpec
}

func (r *planrunStubRunner) Start(ctx context.Context, spec planrun.RunSpec) (*planrun.Run, error) {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	block := r.block
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return &planrun.Run{SessionUUID: spec.SessionUUID, ExitCode: -1}, nil
		}
	}
	return &planrun.Run{SessionUUID: spec.SessionUUID, ExitCode: 0}, nil
}

func (r *planrunStubRunner) lastSpec() planrun.RunSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.specs) == 0 {
		return planrun.RunSpec{}
	}
	return r.specs[len(r.specs)-1]
}

// attachPlanRun wires a stub-backed planrun service (package var, reset on
// cleanup). sync=true runs the spawn inline so a POST response implies the run
// finished (deterministic end-state assertions).
func attachPlanRun(t *testing.T, db *sql.DB, r planrun.Runner, sync bool) *planrun.Service {
	t.Helper()
	svc := planrun.NewService(db, r, phaseStubWt{})
	svc.UUID = func() string { return "plan-uuid-1" }
	if sync {
		svc.Go = func(fn func()) { fn() }
	}
	AttachPlanRun(svc)
	t.Cleanup(func() { AttachPlanRun(nil) })
	return svc
}

func planRunURL(srv *httptest.Server, taskID int64) string {
	return srv.URL + "/api/epics/" + i64(taskID) + "/run"
}

// waitForAPI polls until cond() or 2s — for the async (real-goroutine) run.
func waitForAPI(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// postJSON posts a body (nil ⇒ no body) and returns the response.
func postPlanRun(t *testing.T, url, body string) *http.Response {
	t.Helper()
	var r *http.Response
	var err error
	if body == "" {
		r, err = http.Post(url, "application/json", nil)
	} else {
		r, err = http.Post(url, "application/json", strings.NewReader(body))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	return r
}

func TestRunPlan503WhenUnattached(t *testing.T) {
	srv, _, taskID, _ := epicFixture(t)
	AttachPlanRun(nil)
	resp := postPlanRun(t, planRunURL(srv, taskID), "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when the service is not attached", resp.StatusCode)
	}
}

func TestRunPlanHappyPath(t *testing.T) {
	srv, db, taskID, planDir := epicFixture(t)
	if err := os.WriteFile(filepath.Join(planDir, "README.md"),
		[]byte("# My Epic\n\nObjective: ship it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &planrunStubRunner{}
	attachPlanRun(t, db, r, true)

	resp := postPlanRun(t, planRunURL(srv, taskID), `{"agent":"tech-lead","mode":"subagents"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["sessionUuid"] != "plan-uuid-1" || body["agent"] != "tech-lead" || body["mode"] != "subagents" {
		t.Errorf("body = %v, want the uuid, agent and mode echoed back", body)
	}
	if got := r.lastSpec().Agent; got != "tech-lead" {
		t.Errorf("spec.Agent = %q, want tech-lead", got)
	}

	// The run is visible in the epic DTO the page reads.
	var state, mode string
	if err := db.QueryRow(`SELECT run_state, mode FROM plan_runs WHERE workspace_task_id=?`,
		taskID).Scan(&state, &mode); err != nil {
		t.Fatal(err)
	}
	if state != "done" || mode != "subagents" {
		t.Errorf("plan_runs = (%q, %q), want (done, subagents)", state, mode)
	}
}

func TestRunPlanBadTaskID(t *testing.T) {
	srv, db, _, _ := epicFixture(t)
	attachPlanRun(t, db, &planrunStubRunner{}, true)
	resp := postPlanRun(t, srv.URL+"/api/epics/not-a-number/run", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRunPlanUnknownPlan404(t *testing.T) {
	srv, db, _, _ := epicFixture(t)
	attachPlanRun(t, db, &planrunStubRunner{}, true)
	resp := postPlanRun(t, planRunURL(srv, 9999), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestRunPlan409WhenPhaseRunActive(t *testing.T) {
	srv, db, taskID, planDir := epicFixture(t)
	os.WriteFile(filepath.Join(planDir, "README.md"), []byte("# Epic\n"), 0o644)
	if _, err := db.Exec(`UPDATE epic_phases SET run_state='running' WHERE workspace_task_id=?`, taskID); err != nil {
		t.Fatal(err)
	}
	attachPlanRun(t, db, &planrunStubRunner{}, true)

	resp := postPlanRun(t, planRunURL(srv, taskID), "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while a phase run holds the docs", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"], "phase run") {
		t.Errorf("error = %q, want it to name the conflicting phase run", body["error"])
	}
}

func TestCancelPlanRun(t *testing.T) {
	srv, db, taskID, planDir := epicFixture(t)
	os.WriteFile(filepath.Join(planDir, "README.md"), []byte("# Epic\n"), 0o644)
	r := &planrunStubRunner{block: make(chan struct{})}
	attachPlanRun(t, db, r, false) // real goroutine — the run must stay in flight

	if got := postPlanRun(t, planRunURL(srv, taskID), "").StatusCode; got != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202", got)
	}
	waitForAPI(t, func() bool {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM plan_runs WHERE workspace_task_id=? AND run_state='running'`,
			taskID).Scan(&n)
		return n == 1
	})

	if got := postPlanRun(t, planRunURL(srv, taskID)+"/cancel", "").StatusCode; got != http.StatusAccepted {
		t.Errorf("cancel status = %d, want 202", got)
	}
	waitForAPI(t, func() bool {
		var state string
		db.QueryRow(`SELECT run_state FROM plan_runs WHERE workspace_task_id=?`, taskID).Scan(&state)
		return state == "failed"
	})

	// Cancelling an idle plan is a 409, not a silent success.
	if got := postPlanRun(t, planRunURL(srv, taskID)+"/cancel", "").StatusCode; got != http.StatusConflict {
		t.Errorf("idle cancel status = %d, want 409", got)
	}
}
