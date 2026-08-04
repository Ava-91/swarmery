package api

// phase: projects — PUT /api/projects/{id}/config/{key} writes exactly ONE
// top-level key of a project's .claude/project.json.
//
// This is the only place the daemon writes a project's overlay, and the overlay
// is a file the operator and agent-work.sh both read, so the fence is the plugin
// toggle's fence copied deliberately rather than "simplified": requireLocalOrigin
// at the route, SWARMERY_ONBOARD_ROOTS here, resolveUnderRoots before the write
// (putProjectPlugin, project_plugins.go).
//
// Two checks are this endpoint's own. The key must be one some catalogued pack
// DECLARED (internal/pluginreq) — without that, the route is an arbitrary
// writer for any key into any project.json. And the value must satisfy that
// pack's schema fragment — without that, the daemon happily stores the typo that
// leaves the pack reading a default it was never told about.
//
// Vendor neutrality holds by construction: nothing below knows what any key
// means. Packs declare, pluginreq evaluates, this handler only fences.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/marketplace"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/onboard"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/pluginreq"
)

type putConfigRequest struct {
	// Value is the whole block for the key, raw: it goes to disk verbatim, so
	// decoding it here would reorder its nested keys and float every integer.
	Value json.RawMessage `json:"value"`
}

type putConfigResponse struct {
	Key     string `json:"key"`
	Written bool   `json:"written"`
	Backup  string `json:"backup,omitempty"`
	// Changed is false when the key already held this exact value. The write
	// still happened (indentation is normalised either way); the flag exists so
	// the UI can say "no change" instead of claiming a save that moved nothing.
	Changed bool `json:"changed"`
}

// putConfigInvalid is the 422 body: an error line for the toast plus the
// per-field problems, so the modal can put each one next to its input rather
// than dumping one long sentence at the operator.
type putConfigInvalid struct {
	Error    string   `json:"error"`
	Problems []string `json:"problems"`
}

// declaredRequirement finds the catalogued pack that declared `key`.
//
// It reuses packDir (project_plugins.go) rather than re-joining Catalog.Root
// with Plugin.Source, so the `..` traversal refusal that guards the read path
// guards the write path too — a hand-edited marketplace source must not be able
// to nominate a requirements.json from outside the marketplace root and thereby
// authorise a key.
//
// A pack with a malformed declaration contributes nothing, exactly as in the
// GET: it cannot declare a key, so it cannot authorise a write of one.
func declaredRequirement(key string) (pluginreq.Requirement, bool, error) {
	cdir, err := catalogDir()
	if err != nil {
		return pluginreq.Requirement{}, false, err
	}
	cat, err := marketplace.Read(cdir, pluginMarketplace)
	if err != nil {
		return pluginreq.Requirement{}, false, err
	}
	for _, p := range cat.Plugins {
		dir := packDir(cat, p)
		if dir == "" {
			continue
		}
		reqs, reason := pluginreq.Read(dir)
		if reason != "" {
			continue
		}
		for _, r := range reqs {
			if r.Key == key {
				return r, true, nil
			}
		}
	}
	return pluginreq.Requirement{}, false, nil
}

// putProjectConfig handles PUT /api/projects/{id}/config/{key}.
//
// The check order matches putProjectPlugin's, on purpose: the cheap refusals
// come first, and no branch below can reach the filesystem before the fence has
// admitted the path.
func (h *Handler) putProjectConfig(w http.ResponseWriter, r *http.Request) {
	if len(onboardCfg.Roots) == 0 {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{
			"error": "project config writes are disabled — start the daemon with SWARMERY_ONBOARD_ROOTS set to the allowed parent directories",
		})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid project id"}`, http.StatusBadRequest)
		return
	}

	key := r.PathValue("key")
	req, declared, cerr := declaredRequirement(key)
	if errors.Is(cerr, fs.ErrNotExist) {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{
			"error": "swarmery marketplace is not installed on this machine — run a Claude Code marketplace update",
		})
		return
	}
	if cerr != nil {
		writeErr(w, cerr)
		return
	}
	if !declared {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{
			"error": "unknown config key: " + key,
		})
		return
	}

	var body putConfigRequest
	if derr := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); derr != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	// The value has to be an object: every declared key is a config BLOCK, and
	// a bare scalar could not carry the required leaves the pack asked for.
	// A JSON null unmarshals into a nil map without error, hence the nil test.
	var value map[string]any
	if uerr := json.Unmarshal(body.Value, &value); uerr != nil || value == nil {
		http.Error(w, `{"error":"value must be a JSON object"}`, http.StatusBadRequest)
		return
	}

	var path string
	err = h.DB.QueryRow(`SELECT path FROM projects WHERE id = ?`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	target, err := resolveUnderRoots(path, onboardCfg.Roots)
	if err != nil {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}

	if problems := pluginreq.Validate(value, req); len(problems) > 0 {
		writeJSONStatus(w, http.StatusUnprocessableEntity, putConfigInvalid{
			Error:    key + " does not satisfy the schema declared for it",
			Problems: problems,
		})
		return
	}

	res, werr := onboard.WriteProjectConfig(target, key, body.Value)
	switch {
	case errors.Is(werr, onboard.ErrNoProjectConfig):
		writeJSONStatus(w, http.StatusConflict, map[string]string{
			"error": "not a managed overlay — attach the project first",
		})
		return
	case errors.Is(werr, onboard.ErrBadProjectConfig):
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": werr.Error()})
		return
	case werr != nil:
		writeErr(w, werr)
		return
	}
	writeJSON(w, putConfigResponse{
		Key: key, Written: true, Backup: res.Backup, Changed: res.Changed,
	}, nil)
}
