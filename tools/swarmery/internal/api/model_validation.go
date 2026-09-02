package api

import (
	"net/http"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/modeleval"
)

// modelValidation answers the one question the PreModelSwitch hook asks:
// "is there a passing verdict for this model?".
//
// 404 means never evaluated, which the hook must treat as unknown rather than
// fine — an unvalidated model is exactly what the gate exists to catch. The
// hook, not this handler, decides what to do about it: this endpoint reports,
// it never blocks.
func (h *Handler) modelValidation(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("id")
	if model == "" {
		writeClientErr(w, http.StatusBadRequest, "model id is required")
		return
	}

	res, ok, err := modeleval.Newest(h.DB, model)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeClientErr(w, http.StatusNotFound, "no validation recorded for "+model)
		return
	}

	writeJSON(w, map[string]any{
		"model":            res.Model,
		"goldenSetVersion": res.GoldenSetVersion,
		"verdict":          res.Verdict,
		"score":            res.Score,
		"trajectories":     res.Trajectories,
		"agentsCovered":    res.AgentsCovered,
		"detail":           res.Detail,
	}, nil)
}
