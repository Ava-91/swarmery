-- Plan runs: hand a WHOLE plan to one agent (one headless session, one
-- worktree), as opposed to the per-phase runs of migration 0034.
--
-- Its own table rather than columns on `tasks`: only workspace tasks that are
-- epics can have a plan run, and board tasks must not carry four dead columns.
-- One row per plan (PK = the workspace task), rewritten on each run — a plan
-- run has no history requirement; the transcript is the record.

CREATE TABLE plan_runs (
    workspace_task_id INTEGER PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    agent             TEXT,   -- --agent passed to claude; NULL = account default
    mode              TEXT NOT NULL DEFAULT 'auto',
        -- auto|subagents|inline — how the controller executes the phases.
        -- 'auto' leaves the run-plan skill's own DAG triage authoritative; the
        -- other two are the one call the skill cannot derive from the manifest
        -- (dispatch per-phase executors, or do the work in this session).
    run_state         TEXT NOT NULL DEFAULT 'idle',
        -- idle|running|done|failed
    run_session_uuid  TEXT,
    run_started_at    TEXT,
    run_error         TEXT
);
