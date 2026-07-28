package api

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
)

// getSessionHandoff serves GET /api/sessions/{id}/handoff — the latest
// daemon-generated handoff brief for a session (fat-session wave, migration
// 0039). `id` is the numeric row id or the session UUID (same resolution as
// getSession). Returns {markdown, path, createdAt}; 404 when the session has no
// handoff row, or when the recorded file is gone from disk.
func (h *Handler) getSessionHandoff(w http.ResponseWriter, r *http.Request) {
	idArg := r.PathValue("id")
	where := `s.session_uuid = ?`
	if _, err := strconv.ParseInt(idArg, 10, 64); err == nil {
		where = `s.id = ?`
	}

	var path, createdAt string
	err := h.DB.QueryRow(`
		SELECT ho.path, ho.created_at
		FROM sessions s
		JOIN handoffs ho ON ho.session_id = s.id
		WHERE `+where+`
		ORDER BY ho.created_at DESC, ho.id DESC
		LIMIT 1`, idArg).Scan(&path, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeClientErr(w, http.StatusNotFound, "no handoff for session")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// The row points at a file that has since been deleted — treat as absent.
		writeClientErr(w, http.StatusNotFound, "handoff file missing")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, map[string]string{
		"markdown":  string(body),
		"path":      path,
		"createdAt": createdAt,
	}, nil)
}
