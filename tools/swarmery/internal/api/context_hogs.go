package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ctxhogs"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
)

// transcriptsRoots are the Claude Code projects roots (~/.claude/projects,
// ~/.claude-<account>/projects, … — one per config dir on a multi-subscription
// machine) the daemon ingests from: the single source of truth for
// uuid→transcript lookups, wired from cmd/swarmery via AttachProjectsRoots
// (pattern: AttachOverlaysDir). Empty when unset (tests that don't touch the
// context-hogs endpoint, or an unusual serve config): the endpoint then 404s,
// never panics.
var transcriptsRoots []string

// AttachProjectsRoots wires the ingest projects roots into the on-demand
// transcript-parsing endpoints (currently the context-hogs analyzer). Lookups
// search the roots in order, first match wins.
func AttachProjectsRoots(roots []string) { transcriptsRoots = roots }

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

	// Resolve uuid → transcript path: <root>/<slug>/<uuid>.jsonl across EVERY
	// configured root, through the same lookup the ingest stub-heal uses
	// (internal/ingest.FindTranscript). First match wins; no transcript on disk
	// is a legitimate 404 (hook-only session, or a deleted/rewound transcript).
	if len(transcriptsRoots) == 0 {
		writeClientErr(w, http.StatusNotFound, "no transcript root configured")
		return
	}
	transcript := ingest.FindTranscript(transcriptsRoots, sessionUUID)
	if transcript == "" {
		writeClientErr(w, http.StatusNotFound, "no transcript on disk for session")
		return
	}

	report, err := ctxhogs.Analyze(transcript)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, report, nil)
}
