package api

// Phase-run endpoints (interactive planning v2 phase 5): execute a plan phase
// directly from its phase doc — a headless claude run in an isolated worktree
// via internal/phaserun, state on epic_phases (run_state & co, migrations 0034 +
// 0041 run_checkboxes_before/run_ended_at + 0042 run_checkboxes_after).
// No `tasks` row and no board involvement; checkbox progress keeps flowing
// through wsingest as the executor edits the docs.
//
//	POST /api/epics/{taskId}/phases/{phaseId}/run        → 202 {status, sessionUuid}
//	POST /api/epics/{taskId}/phases/{phaseId}/run/cancel → 202 {status}
//
// The service is attached once at daemon startup (AttachPhaseRun) — the same
// package-var idiom as AttachPlanning/AttachDispatch — so httptest handlers
// built with &Handler{DB: db} stay hermetic (nil ⇒ endpoints 503, no spawn).
// Its Notify is wired to publishPlanUpdated, the SAME plan_updated frame
// wsingest's NotifyPlan uses, so the Plans page refetches on run edges.

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/phaserun"
)

// phaserunSvc is attached once at daemon startup (nil ⇒ phase-run endpoints
// 503 and no notify wiring).
var phaserunSvc *phaserun.Service

// AttachPhaseRun wires the phase-run service into the api layer and gives it
// the api-owned plan_updated emitter (keyed by workspace task id — the same
// frame wsingest publishes on checkbox rescans, so an open Plans page
// refetches on run edges with no new WS type). Called from cmd/swarmery after
// the service is constructed; left nil in unit tests so board/doc writes never
// trigger a real headless spawn.
func AttachPhaseRun(s *phaserun.Service) {
	if s != nil {
		s.Notify = publishPlanUpdated
	}
	phaserunSvc = s
}

// parsePhaseRunParams parses {taskId} + {phaseId} and verifies the phase
// belongs to that workspace task (a mismatched pair is a 404 — the route
// addresses a phase THROUGH its epic). Writes the error response itself.
func (h *Handler) parsePhaseRunParams(w http.ResponseWriter, r *http.Request) (phaseID int64, ok bool) {
	taskID, err := strconv.ParseInt(r.PathValue("taskId"), 10, 64)
	if err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid task id")
		return 0, false
	}
	phaseID, err = strconv.ParseInt(r.PathValue("phaseId"), 10, 64)
	if err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid phase id")
		return 0, false
	}
	var wsTaskID int64
	qerr := h.DB.QueryRow(
		`SELECT workspace_task_id FROM epic_phases WHERE id = ?`, phaseID).Scan(&wsTaskID)
	if errors.Is(qerr, sql.ErrNoRows) || (qerr == nil && wsTaskID != taskID) {
		writeClientErr(w, http.StatusNotFound, "phase not found")
		return 0, false
	}
	if qerr != nil {
		writeErr(w, qerr)
		return 0, false
	}
	return phaseID, true
}

// runPhase — POST /api/epics/{taskId}/phases/{phaseId}/run. requireLocalOrigin.
// 202 {status:"running", sessionUuid}; 404 unknown phase; 409 already running /
// unmet deps (body names them) / unreadable doc / pathless project; 503 not
// attached.
func (h *Handler) runPhase(w http.ResponseWriter, r *http.Request) {
	if phaserunSvc == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "phase runs not attached")
		return
	}
	phaseID, ok := h.parsePhaseRunParams(w, r)
	if !ok {
		return
	}
	uuid, err := phaserunSvc.Start(phaseID)
	var depsErr *phaserun.DepsUnmetError
	switch {
	case errors.Is(err, phaserun.ErrPhaseNotFound):
		writeClientErr(w, http.StatusNotFound, "phase not found")
	case errors.Is(err, phaserun.ErrRunning):
		writeClientErr(w, http.StatusConflict, "a run is already active for this phase")
	case errors.As(err, &depsErr):
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error":     depsErr.Error(),
			"unmetDeps": depsErr.Unmet,
		})
	case errors.Is(err, phaserun.ErrNoDoc):
		writeClientErr(w, http.StatusConflict, "phase doc is unreadable")
	case errors.Is(err, phaserun.ErrNoPath):
		writeClientErr(w, http.StatusConflict, "project has no known path to run in")
	case err != nil:
		writeErr(w, err)
	default:
		writeJSONStatus(w, http.StatusAccepted, map[string]string{
			"status":      "running",
			"sessionUuid": uuid,
		})
	}
}

// cancelPhaseRun — POST /api/epics/{taskId}/phases/{phaseId}/run/cancel.
// requireLocalOrigin. 202 when an in-flight run was cancelled (its goroutine
// stamps failed/cancelled); 409 nothing running; 503 not attached.
func (h *Handler) cancelPhaseRun(w http.ResponseWriter, r *http.Request) {
	if phaserunSvc == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "phase runs not attached")
		return
	}
	phaseID, ok := h.parsePhaseRunParams(w, r)
	if !ok {
		return
	}
	if !phaserunSvc.Cancel(phaseID) {
		writeClientErr(w, http.StatusConflict, "no run is active for this phase")
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}
