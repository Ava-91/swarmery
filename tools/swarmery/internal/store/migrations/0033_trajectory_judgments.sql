-- Verification Contour v2, Phase 2: advisory LLM-judge trajectory verdicts.
-- Subjective axis, one row per (session, agent, judge model). Never gates.
CREATE TABLE trajectory_judgments (
    id          INTEGER PRIMARY KEY,
    session_id  INTEGER NOT NULL REFERENCES sessions(id),
    agent       TEXT NOT NULL,
    model       TEXT NOT NULL,            -- judge model id (provenance)
    judged_at   TEXT NOT NULL,
    end_result             INTEGER NOT NULL,   -- 1..5
    instruction_compliance INTEGER NOT NULL,   -- 1..5
    pitfalls               INTEGER NOT NULL,   -- 1..5 (higher = fewer pitfalls)
    tool_calls             INTEGER NOT NULL,   -- 1..5
    overall     REAL NOT NULL,            -- mean of the 4 dims
    review      TEXT NOT NULL,            -- written review with evidence lines
    UNIQUE(session_id, agent, model)
);

CREATE INDEX idx_trajectory_judgments_agent ON trajectory_judgments(agent);
