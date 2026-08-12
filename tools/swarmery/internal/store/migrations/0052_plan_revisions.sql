-- Plan revisions: a staged, reviewable proposal to change an already-saved plan.
-- Files live in the DB until the operator applies them; the plan dir on disk is
-- untouched while a revision is 'staged'.

CREATE TABLE plan_revisions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_task_id INTEGER NOT NULL,
    plan_dir          TEXT    NOT NULL,          -- absolute plan/ dir resolved at staging time
    session_uuid      TEXT,                      -- the revise planning session that produced it
    status            TEXT    NOT NULL DEFAULT 'staged',
        -- staged|applied|rejected|superseded|failed
    origin            TEXT    NOT NULL DEFAULT 'operator_revise',
        -- operator_revise|phase_diagnosis
    trigger_phase_id  INTEGER,                   -- epic_phases.id when started from a diagnosis
    reason            TEXT    NOT NULL DEFAULT '',
    summary           TEXT,                      -- JSON PlanningSummary snapshot
    error             TEXT,                      -- validation/apply failure detail
    created_at        TEXT    NOT NULL,
    decided_at        TEXT,
    decided_by        TEXT                       -- 'operator' on apply/reject; 'system' on supersede
);
CREATE INDEX idx_plan_revisions_task ON plan_revisions(workspace_task_id, id DESC);
CREATE INDEX idx_plan_revisions_status ON plan_revisions(status);

CREATE TABLE plan_revision_files (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id  INTEGER NOT NULL REFERENCES plan_revisions(id) ON DELETE CASCADE,
    doc_path     TEXT    NOT NULL,   -- plan-dir-RELATIVE target path (e.g. "phase-3-api.md")
    action       TEXT    NOT NULL,   -- create|update|delete|rename
    rename_from  TEXT,               -- plan-dir-relative source, set iff action='rename'
    base_hash    TEXT,               -- sha256 hex of the live file when staged; NULL for create
    proposed     TEXT,               -- full proposed content; NULL for delete
    pre_image    TEXT,               -- bytes replaced at apply time (rollback + audit)
    applied_hash TEXT,               -- sha256 hex actually written
    UNIQUE(revision_id, doc_path)
);

-- Revise mode: a planning session may target an existing plan instead of minting one.
ALTER TABLE planning_sessions ADD COLUMN mode TEXT NOT NULL DEFAULT 'plan';   -- plan|revise
ALTER TABLE planning_sessions ADD COLUMN revise_task_id INTEGER;
