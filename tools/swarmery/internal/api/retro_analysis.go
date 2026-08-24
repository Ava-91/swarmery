package api

// Page-level improvement endpoints: turn the whole retro report into a saved,
// human-gated analysis of the agent system (internal/retroanalysis).
//
// Deliberately NOT part of internal/improve. That service's product is a
// unified diff to exactly one agent definition file, and every part of it —
// the resolver, the base sha, the one-open-proposal invariant, the "minimal
// change" prompt — is built around that. This one's product is prose about
// the whole system, and its gate is a human accepting it. Sharing a table or
// a service between the two would make both worse.
//
// Generation shells out to headless `claude -p` (minutes), so the POST
// validates synchronously, answers 202, and the row carries the outcome —
// the same fire-and-observe pattern as POST /api/retro/*/improve.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/retroanalysis"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/retrodigest"
)

// analysisDigestLimit is the evidence budget for the improver prompt — the
// same 30KB cap internal/improve/bundle.go uses for its own bundle. Not the
// planner's 8000: what has to fit there is the analysis, not its input.
const analysisDigestLimit = reportDigestLimit

// POST /api/retro/analysis — build the report for the requested window, digest
// it, and start a system analysis over it. 202 with the new row; 409 when one
// is already running.
func (h *Handler) startRetroAnalysis(w http.ResponseWriter, r *http.Request) {
	if h.RetroAnalysis == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "analysis not attached")
		return
	}
	dr, err := parseRange(r)
	if err != nil {
		writeClientErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pf, pargs := scopeFilter(r)
	project := r.URL.Query().Get("project")

	rep := h.buildRetroReport(dr, pf, pargs, project)
	digest, _ := retrodigest.Build(reportToDigest(rep), analysisDigestLimit)

	id, err := h.RetroAnalysis.Start(rep.From, rep.To, project, digest, sha256Hex([]byte(digest)))
	switch {
	case errors.Is(err, retroanalysis.ErrAnalysisRunning):
		writeClientErr(w, http.StatusConflict, "an analysis is already running — wait for it to finish")
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	row, err := h.RetroAnalysis.Get(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, row)
}

// GET /api/retro/analysis?project= — the newest analysis for the scope, or
// null when there is none. The UI polls this while a run is in flight.
func (h *Handler) latestRetroAnalysis(w http.ResponseWriter, r *http.Request) {
	if h.RetroAnalysis == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "analysis not attached")
		return
	}
	row, err := h.RetroAnalysis.Latest(r.URL.Query().Get("project"))
	writeJSON(w, map[string]any{"analysis": row}, err)
}

// PATCH /api/retro/analysis/{id} — the operator's gate. {"status":"accepted"}
// or {"status":"dismissed"}, only from `proposed`.
func (h *Handler) patchRetroAnalysis(w http.ResponseWriter, r *http.Request) {
	if h.RetroAnalysis == nil {
		writeClientErr(w, http.StatusServiceUnavailable, "analysis not attached")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid analysis id")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	status := strings.TrimSpace(body.Status)
	if status != "accepted" && status != "dismissed" {
		writeClientErr(w, http.StatusBadRequest, `status must be "accepted" or "dismissed"`)
		return
	}
	row, err := h.RetroAnalysis.Decide(id, status)
	switch {
	case errors.Is(err, retroanalysis.ErrNotFound):
		writeClientErr(w, http.StatusNotFound, "analysis not found")
		return
	case errors.Is(err, retroanalysis.ErrBadTransition):
		writeClientErr(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	writeJSON(w, row, nil)
}
