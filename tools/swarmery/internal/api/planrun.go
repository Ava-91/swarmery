package api

// Plan-run endpoints: execute a WHOLE plan by handing it to one agent — a
// headless claude run in an isolated worktree via internal/planrun, state on
// plan_runs (migration 0040). The run's procedure is core's `run-plan` skill;
// this layer only starts and stops it. Per-phase progress keeps flowing through
// wsingest as the controller ticks the phase docs.
//
//	POST /api/epics/{taskId}/run        {agent?} → 202 {status, sessionUuid, agent}
//	POST /api/epics/{taskId}/run/cancel          → 202 {status}
//
// The service is attached once at daemon startup (AttachPlanRun) — the same
// package-var idiom as AttachPhaseRun/AttachPlanning — so httptest handlers
// built with &Handler{DB: db} stay hermetic (nil ⇒ endpoints 503, no spawn).
// Its Notify is wired to publishPlanUpdated, the SAME plan_updated frame
// wsingest's NotifyPlan uses, so the Plans page refetches on run edges.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planrun"
)

// planrunSvc is attached once at daemon startup (nil ⇒ plan-run endpoints 503
// and no notify wiring).
var planrunSvc *planrun.Service

// AttachPlanRun wires the plan-run service into the api layer and gives it the
// api-owned plan_updated emitter (keyed by workspace task id — the same frame
// wsingest publishes on checkbox rescans, so an open Plans page refetches on
// run edges with no new WS type). Called from cmd/swarmery after the service is
// constructed; left nil in unit tests so doc writes never trigger a real
// headless spawn.
func AttachPlanRun(s *planrun.Service) {
	if s != nil {
		s.Notify = publishPlanUpdated
	}
	planrunSvc = s
}

// planRunBody is the optional POST body: which agent the plan is handed to and
// how it executes the phases. Absent/empty ⇒ planrun.DefaultAgent() +
// planrun.ModeAuto (the run-plan skill's own route triage).
type planRunBody struct {
	Agent string `json:"agent"`
	Mode  string `json:"mode"` // auto | subagents | inline
}

// parsePlanRunTask parses {taskId}. Writes the error response itself.
func parsePlanRunTask(w http.ResponseWriter, r *http.Request) (int64, bool) {
	taskID, err := strconv.ParseInt(r.PathValue("taskId"), 10, 64)
	if err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid task id")
		return 0, false
	}
	return taskID, true
}

// runPlan — POST /api/epics/{taskId}/run. requireLocalOrigin.
// 202 {status:"running", sessionUuid, agent}; 404 unknown plan; 409 already
// running / a phase run is active / paused-archived / no phases / already
// complete / unreadable README / pathless project; 503 not attached.
func (h *Handler) runPlan(w http.ResponseWriter, r *http.Request) {
	if planrunSvc == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "plan runs not attached")
		return
	}
	taskID, ok := parsePlanRunTask(w, r)
	if !ok {
		return
	}
	// The body is optional — a bare POST means "use the default agent".
	var body planRunBody
	if raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<10)); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &body) // malformed body ⇒ default agent, not a 400
	}
	agent := strings.TrimSpace(body.Agent)
	mode := planrun.ValidMode(body.Mode)

	uuid, err := planrunSvc.Start(taskID, agent, string(mode))
	switch {
	case errors.Is(err, planrun.ErrPlanNotFound):
		writeClientErr(w, http.StatusNotFound, "plan not found")
	case errors.Is(err, planrun.ErrRunning):
		writeClientErr(w, http.StatusConflict, "a run is already active for this plan")
	case errors.Is(err, planrun.ErrPhaseRunning):
		writeClientErr(w, http.StatusConflict, "a phase run is active — cancel it before running the whole plan")
	case errors.Is(err, planrun.ErrNotActive):
		writeClientErr(w, http.StatusConflict, "plan is not active")
	case errors.Is(err, planrun.ErrNoPhases):
		writeClientErr(w, http.StatusConflict, "plan has no phases")
	case errors.Is(err, planrun.ErrComplete):
		writeClientErr(w, http.StatusConflict, "every phase of this plan is already done")
	case errors.Is(err, planrun.ErrNoDoc):
		writeClientErr(w, http.StatusConflict, "plan README is unreadable")
	case errors.Is(err, planrun.ErrNoPath):
		writeClientErr(w, http.StatusConflict, "project has no known path to run in")
	case err != nil:
		writeErr(w, err)
	default:
		if agent == "" {
			agent = planrun.DefaultAgent()
		}
		writeJSONStatus(w, http.StatusAccepted, map[string]string{
			"status":      "running",
			"sessionUuid": uuid,
			"agent":       agent,
			"mode":        string(mode),
		})
	}
}

// cancelPlanRun — POST /api/epics/{taskId}/run/cancel. requireLocalOrigin.
// 202 when an in-flight run was cancelled (its goroutine stamps
// failed/cancelled); 409 nothing running; 503 not attached.
func (h *Handler) cancelPlanRun(w http.ResponseWriter, r *http.Request) {
	if planrunSvc == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "plan runs not attached")
		return
	}
	taskID, ok := parsePlanRunTask(w, r)
	if !ok {
		return
	}
	if !planrunSvc.Cancel(taskID) {
		writeClientErr(w, http.StatusConflict, "no run is active for this plan")
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}
