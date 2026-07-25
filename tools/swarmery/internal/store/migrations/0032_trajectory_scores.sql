-- Verification Contour v2, Pipeline A: post-hoc trajectory scores of real runs.
-- One row per (session, primary agent); recomputed idempotently on the daemon tick.
CREATE TABLE trajectory_scores (
    id          INTEGER PRIMARY KEY,
    session_id  INTEGER NOT NULL REFERENCES sessions(id),
    agent       TEXT NOT NULL,
    first_pass  INTEGER NOT NULL,            -- 0/1
    computed_at TEXT NOT NULL,
    UNIQUE(session_id, agent)
);

CREATE TABLE trajectory_findings (
    id                INTEGER PRIMARY KEY,
    score_id          INTEGER NOT NULL REFERENCES trajectory_scores(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL,          -- search-loop | verify-skip
    severity          TEXT NOT NULL,          -- warn
    evidence_turn_ids TEXT NOT NULL           -- JSON array of turn ids
);

CREATE INDEX idx_trajectory_scores_agent ON trajectory_scores(agent);
