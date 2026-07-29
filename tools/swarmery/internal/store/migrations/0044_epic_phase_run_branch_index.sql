-- 0044: index the branch stamped by 0043. The Sessions page resolves which plan run
-- spawned a session by matching sessions.git_branch against epic_phases.run_branch —
-- a per-session lookup on a string column, which without an index degrades to a scan
-- of every phase row for every session in the list.
--
-- Partial by design: run_branch is NULL for every phase that never ran, and those rows
-- can never satisfy the equality the index exists to serve.
CREATE INDEX IF NOT EXISTS idx_epic_phases_run_branch
    ON epic_phases(run_branch) WHERE run_branch IS NOT NULL;
