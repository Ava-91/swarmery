-- Planning wizard durable state (interactive planning v2) + phase-run state.

CREATE TABLE planning_sessions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER NOT NULL,
    session_uuid     TEXT NOT NULL UNIQUE,
    status           TEXT NOT NULL DEFAULT 'generating',
        -- generating|awaiting_answer|proceeding|done|failed|cancelled
    idea             TEXT NOT NULL,
    running_plan     TEXT,   -- JSON PlanningSummary (latest)
    current_question TEXT,   -- JSON PlanningQuestion, NULL when none/parse-failed
    raw_reply        TEXT,   -- latest assistant text when parse failed (fallback UI)
    plan_dir         TEXT,   -- from the PLAN SAVED: line
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX idx_planning_sessions_project ON planning_sessions(project_id, id DESC);

CREATE TABLE planning_turns (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    planning_session_id INTEGER NOT NULL REFERENCES planning_sessions(id) ON DELETE CASCADE,
    seq                 INTEGER NOT NULL,
    question            TEXT NOT NULL,  -- JSON PlanningQuestion
    answer              TEXT,           -- JSON {kind:'answer'|'refine', selectedOptionIds:[], otherText?, instructions?}
    reasoning           TEXT,           -- pre-JSON analysis + thinking blocks
    created_at          TEXT NOT NULL,
    UNIQUE(planning_session_id, seq)
);

-- Phase-run state (plans↔board separation): a phase executes directly, no board task.
ALTER TABLE epic_phases ADD COLUMN run_session_uuid TEXT;
ALTER TABLE epic_phases ADD COLUMN run_state TEXT NOT NULL DEFAULT 'idle';
    -- idle|running|done|failed
ALTER TABLE epic_phases ADD COLUMN run_started_at TEXT;
ALTER TABLE epic_phases ADD COLUMN run_error TEXT;
