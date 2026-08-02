package api

// On-demand LLM task extraction (phase 6 — internal/extract): the operator's
// "Extract tasks" button on a session. Deterministic capture (internal/ingest)
// reads what a session wrote down; this asks a model what the session left
// behind, and is the OPTIONAL, manual, paid complement to it.
//
// Why this endpoint answers with a count instead of firing and forgetting like
// POST /api/tasks/{id}/verify: verification stamps a verdict on a row the
// operator is already watching, so a 202 and a later WS frame tell the whole
// story. An extraction's outcome is a NUMBER the human who pressed the button
// is waiting for ("3 tasks suggested" / "nothing new"), and its most useful
// failure — the model answered in prose — has no row to land on and would
// otherwise vanish into the daemon log. So the run is awaited and its result is
// the response body. The 202 status is kept from the verify shape (the work is
// accepted and the cards arrive over the bus); the bound is the runner's own
// 5-minute timeout, not the request.

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/extract"
)

// extractSvc is attached once at daemon startup (nil ⇒ the endpoint 503s).
// Mirrors verifySvc / dispatchSvc.
var extractSvc *extract.Service

// AttachExtract wires the extraction service into the api layer and gives it
// the api-owned task_updated emitter, so a suggested card reaches the board
// over the FROZEN WS bus — no new message type. Called from cmd/swarmery. Left
// nil in unit tests so no board write can trigger a real headless run.
func AttachExtract(s *extract.Service) {
	if s != nil {
		s.Notify = publishTaskUpdated
	}
	extractSvc = s
}

// extractSessionTasks — POST /api/sessions/{id}/extract-tasks: run one model
// pass over a session and put the tasks it names on the board as suggested
// Triage cards (origin='llm').
//
// 404 unknown session; 409 the session may not mint cards (dispatched run,
// System project) or an extraction is already in flight; 502 the model answered
// something that is not the contract; 503 the service is not attached.
// requireLocalOrigin. Idempotent: re-running over an unchanged session inserts
// nothing and reports 0.
func (h *Handler) extractSessionTasks(w http.ResponseWriter, r *http.Request) {
	if extractSvc == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "task extraction not attached")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid session id")
		return
	}

	// Pre-flight the cheap gates synchronously so an ineligible session costs no
	// model run at all — the same "answer 404/409 before the expensive part"
	// split verify.go makes, just with the expensive part awaited rather than
	// spawned.
	switch reason, ok, err := extractSvc.Eligible(id); {
	case errors.Is(err, extract.ErrSessionNotFound):
		writeClientErr(w, http.StatusNotFound, "session not found")
		return
	case err != nil:
		writeClientErr(w, http.StatusInternalServerError, err.Error())
		return
	case !ok:
		writeClientErr(w, http.StatusConflict, "this session cannot produce board cards: "+reason)
		return
	}
	if extractSvc.Running(id) {
		writeClientErr(w, http.StatusConflict, "an extraction is already running for this session")
		return
	}

	inserted, err := extractSvc.ExtractTasks(r.Context(), id)
	switch {
	case errors.Is(err, extract.ErrAlreadyRunning):
		writeClientErr(w, http.StatusConflict, "an extraction is already running for this session")
		return
	case errors.Is(err, extract.ErrSessionNotFound):
		writeClientErr(w, http.StatusNotFound, "session not found")
		return
	case err != nil:
		var skipped *extract.ErrSkipped
		if errors.As(err, &skipped) {
			writeClientErr(w, http.StatusConflict, "this session cannot produce board cards: "+skipped.Reason)
			return
		}
		// Everything left is the model or the run failing: a bad answer, a
		// timeout, a missing binary. 502 — the daemon is fine, its upstream is
		// not — and the detail is surfaced verbatim so the operator can tell a
		// timeout from a prose answer without reading the log.
		log.Printf("error: extract: session %d: %v", id, err)
		writeClientErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{
		"status":    "extracted",
		"sessionId": id,
		"inserted":  inserted,
	})
}
