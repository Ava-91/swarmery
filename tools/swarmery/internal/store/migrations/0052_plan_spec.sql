-- Spec criteria parsed from plan/spec.md (wsingest owns these rows; prune-by-exclusion per task on each parse).
CREATE TABLE IF NOT EXISTS spec_criteria (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_task_id INTEGER NOT NULL,
    pos               INTEGER NOT NULL DEFAULT 0,  -- order of appearance in spec.md
    cid               TEXT NOT NULL,               -- e.g. "SC-1"
    text              TEXT NOT NULL DEFAULT '',
    done              INTEGER NOT NULL DEFAULT 0,
    line              INTEGER NOT NULL DEFAULT 0,  -- 0-based source line in spec.md
    UNIQUE(workspace_task_id, cid)
);
CREATE INDEX IF NOT EXISTS idx_spec_criteria_task ON spec_criteria(workspace_task_id);

-- Phase docs may declare which spec criteria they deliver: **Covers:** SC-1, SC-3
ALTER TABLE epic_phases ADD COLUMN covers TEXT NOT NULL DEFAULT '[]'; -- JSON array of cids, mirrors depends_on
