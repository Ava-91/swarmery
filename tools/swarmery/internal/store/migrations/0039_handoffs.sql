-- 0039: session handoffs — daemon-generated continuation briefs for fat sessions.
-- When a session's context crosses the handoff threshold, the daemon writes a
-- markdown brief to ~/.swarmery/handoffs/<session_uuid>.md and records the row
-- here (path + the context footprint at generation time), so the dashboard can
-- surface a "Handoff" chip + a copy-paste resume command, and the cooldown logic
-- can regenerate only after the context has grown materially past the last brief.
CREATE TABLE handoffs (
    id             INTEGER PRIMARY KEY,
    session_id     INTEGER NOT NULL REFERENCES sessions(id),
    path           TEXT    NOT NULL,             -- absolute path of the generated .md
    context_tokens INTEGER NOT NULL,             -- session context at generation time
    created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX idx_handoffs_session ON handoffs(session_id, created_at DESC);
