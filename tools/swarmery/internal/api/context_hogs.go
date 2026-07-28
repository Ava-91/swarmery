package api

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ctxhogs"
)

// transcriptsRoot is the Claude Code projects root (~/.claude/projects) the
// daemon ingests from — the single source of truth for uuid→transcript lookups,
// wired from cmd/swarmery via AttachProjectsRoot (pattern: AttachOverlaysDir).
// Empty when unset (tests that don't touch the context-hogs endpoint, or an
// unusual serve config): the endpoint then 404s, never panics.
var transcriptsRoot string

// AttachProjectsRoot wires the ingest projects root into the on-demand
// transcript-parsing endpoints (currently the context-hogs analyzer).
func AttachProjectsRoot(root string) { transcriptsRoot = root }

// getSessionContextHogs serves GET /api/sessions/{id}/context-hogs — the
// per-tool context attribution for a session, computed on demand by parsing the
// session's transcript JSONL (no store, no caching: a Go parse of even a 20 MB
// transcript is sub-second and the panel opens rarely). `id` is the numeric row
// id or the session UUID (same resolution as getSession/getSessionHandoff).
// 404 when the session is unknown OR its transcript is not on disk.
func (h *Handler) getSessionContextHogs(w http.ResponseWriter, r *http.Request) {
	idArg := r.PathValue("id")
	where := `session_uuid = ?`
	if _, err := strconv.ParseInt(idArg, 10, 64); err == nil {
		where = `id = ?`
	}

	var sessionUUID string
	err := h.DB.QueryRow(
		`SELECT session_uuid FROM sessions WHERE `+where+` LIMIT 1`, idArg).Scan(&sessionUUID)
	if errors.Is(err, sql.ErrNoRows) {
		writeClientErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	// Resolve uuid → transcript path: <projectsRoot>/<slug>/<uuid>.jsonl, the
	// same glob the ingest stub-heal uses (internal/ingest/heal.go). First match
	// wins; no transcript on disk is a legitimate 404 (hook-only session, or a
	// deleted/rewound transcript).
	if transcriptsRoot == "" {
		writeClientErr(w, http.StatusNotFound, "no transcript root configured")
		return
	}
	matches, _ := filepath.Glob(filepath.Join(transcriptsRoot, "*", sessionUUID+".jsonl"))
	if len(matches) == 0 {
		writeClientErr(w, http.StatusNotFound, "no transcript on disk for session")
		return
	}

	report, err := ctxhogs.Analyze(matches[0])
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, report, nil)
}
