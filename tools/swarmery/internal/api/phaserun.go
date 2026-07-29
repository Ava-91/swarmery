package api

// Phase-run endpoints (interactive planning v2 phase 5): execute a plan phase
// directly from its phase doc — a headless claude run in an isolated worktree
// via internal/phaserun, state on epic_phases (run_state & co, migrations 0034 +
// 0041 run_checkboxes_before/run_ended_at + 0042 run_checkboxes_after).
// No `tasks` row and no board involvement; checkbox progress keeps flowing
// through wsingest as the executor edits the docs.
//
//	POST   /api/epics/{taskId}/phases/{phaseId}/run        → 202 {status, sessionUuid}
//	POST   /api/epics/{taskId}/phases/{phaseId}/run/cancel → 202 {status}
//	GET    /api/epics/{taskId}/phases/{phaseId}/diagnosis  → 200 phasediag.Diagnosis
//	DELETE /api/epics/{taskId}/phases/{phaseId}/branch     → 200 {deleted, branch}
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

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/phasediag"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/phaserun"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// phaserunSvc is attached once at daemon startup (nil ⇒ phase-run endpoints
// 503 and no notify wiring).
var phaserunSvc *phaserun.Service

// phasediagGit is the git boundary for on-demand phase diagnosis, attached once at
// daemon startup (nil ⇒ the endpoint still answers, with branch-derived blockers
// omitted — phasediag.Diagnose degrades by contract). Same package-var idiom as
// phaserunSvc, so httptest handlers built with &Handler{DB: db} stay hermetic.
var phasediagGit worktree.Git

// AttachPhaseDiag wires the git boundary used by the diagnosis endpoint.
func AttachPhaseDiag(g worktree.Git) { phasediagGit = g }

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
	var dirtyErr *phaserun.BranchDirtyError
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
	// The leftover run branch holds commits a retry would collide with. Commit
	// SUBJECTS are deliberately absent here — the UI already fetches /diagnosis,
	// whose branch-dirty blocker carries them, and git ownership stays in phasediag.
	// `base` names what the count was measured against (empty when it could not be
	// named): "2 commits ahead" is only actionable once the user knows ahead of what.
	// Same four fields runPlan emits, so both run surfaces parse as one shape.
	case errors.As(err, &dirtyErr):
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error":        dirtyErr.Error(),
			"branch":       dirtyErr.Branch,
			"commitsAhead": dirtyErr.CommitsAhead,
			"base":         dirtyErr.Base,
		})
	// Start wraps the reclaim failure (fmt.Errorf("reclaim run branch: %w", …)), so
	// errors.Is still matches through the wrap. Without this arm a checked-out run
	// branch surfaces as a raw 500 and the UI can show the user nothing actionable.
	case errors.Is(err, worktree.ErrBranchCheckedOut):
		writeClientErr(w, http.StatusConflict, "the run branch is checked out in another worktree")
	// Same category, same wrap: with no checked-out branch there is no base to
	// measure the leftover run branch against, so reclaim refuses rather than guess
	// one — and a guessed base is what a `git branch -D` must never run on. The user
	// resolves it by checking out a branch, which a raw 500 would never say.
	case errors.Is(err, worktree.ErrDetachedHead):
		writeClientErr(w, http.StatusConflict,
			"the repo is on a detached HEAD, so the run branch cannot be measured against a base — check out a branch first")
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

// phaseDiagnosis — GET /api/epics/{taskId}/phases/{phaseId}/diagnosis.
// 200 phasediag.Diagnosis; 404 unknown phase or a phase that does not belong to
// that epic. A read endpoint, so no requireLocalOrigin (matching the other GETs).
func (h *Handler) phaseDiagnosis(w http.ResponseWriter, r *http.Request) {
	phaseID, ok := h.parsePhaseRunParams(w, r)
	if !ok {
		return
	}
	d, err := phasediag.Diagnose(h.DB, phasediagGit, phaseID)
	switch {
	case errors.Is(err, phasediag.ErrPhaseNotFound):
		writeClientErr(w, http.StatusNotFound, "phase not found")
	case err != nil:
		writeErr(w, err)
	default:
		writeJSON(w, d, nil)
	}
}

// deletePhaseRunBranch — DELETE /api/epics/{taskId}/phases/{phaseId}/branch.
// requireLocalOrigin. The explicit user decision behind a 409 branchDirty: force-
// deletes swarm/phase-<id> INCLUDING its commits. 200 {deleted, branch}; 409 while a
// run is in flight or the branch is checked out; 503 not attached.
func (h *Handler) deletePhaseRunBranch(w http.ResponseWriter, r *http.Request) {
	if phaserunSvc == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "phase runs not attached")
		return
	}
	phaseID, ok := h.parsePhaseRunParams(w, r)
	if !ok {
		return
	}
	branch, err := phaserunSvc.DeleteRunBranch(phaseID)
	switch {
	case errors.Is(err, phaserun.ErrPhaseNotFound):
		writeClientErr(w, http.StatusNotFound, "phase not found")
	case errors.Is(err, phaserun.ErrRunning):
		writeClientErr(w, http.StatusConflict, "a run is active for this phase")
	case errors.Is(err, worktree.ErrBranchCheckedOut):
		writeClientErr(w, http.StatusConflict, "branch is checked out in a worktree")
	case errors.Is(err, phaserun.ErrNoPath):
		writeClientErr(w, http.StatusConflict, "project has no known path")
	case err != nil:
		writeErr(w, err)
	default:
		writeJSON(w, map[string]any{"deleted": true, "branch": branch}, nil)
	}
}
