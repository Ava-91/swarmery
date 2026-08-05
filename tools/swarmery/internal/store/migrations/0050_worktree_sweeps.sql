-- 0050: worktree janitor journal — one row per DECISION, not per deletion, so
-- "kept alive" and "kept because unmerged" are visible too. This is the audit
-- trail for a subsystem that acts without asking, and the data source for the
-- dashboard's Worktrees panel; it is written by internal/wtjanitor only.
--
-- Rows are pruned with the rest of retention (internal/prune) after the
-- janitor's RetentionDays, so a long-lived daemon does not grow an unbounded
-- log. Nothing reads a row to make a decision — the classifier re-observes the
-- world on every pass — so losing old rows costs history, never correctness.

CREATE TABLE worktree_sweeps (
    id             INTEGER PRIMARY KEY,
    ts             TEXT NOT NULL,                  -- ISO-8601 UTC
    project_id     INTEGER REFERENCES projects(id),
    path           TEXT NOT NULL,                  -- the worktree checkout path
    branch         TEXT,                           -- NULL when detached
    verdict        TEXT NOT NULL,                  -- skip | keep-unmerged | redundant | salvage
    reason         TEXT NOT NULL,                  -- the classifier's own words
    salvage_branch TEXT,                           -- set when a salvage commit succeeded
    files          INTEGER NOT NULL DEFAULT 0,     -- dirty paths seen
    removed        INTEGER NOT NULL DEFAULT 0,     -- 1 when the checkout was actually deleted
    error          TEXT                            -- non-NULL when an action failed; the worktree survives
);

CREATE INDEX idx_worktree_sweeps_ts ON worktree_sweeps(ts);
