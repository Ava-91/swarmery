-- Verify-for-phases (execution-engine unification, phase 5): a phase run can opt
-- into the same read-only verification board cards get.
--
-- 0057, not 0056: the micro-plans phase took that number on a sibling branch.
-- Migrations apply in filename order and nothing requires them to be contiguous,
-- so a gap is harmless while a collision is not.
--
-- run_start_point mirrors tasks.start_point (0051): the SHA the run's worktree was
-- pinned to. Without it a verifier diffs the branch against itself and grades an
-- empty change set as "nothing was done" — the defect 0051 fixed for board cards,
-- which would arrive here intact if the base were re-derived instead of recorded.
ALTER TABLE epic_phases ADD COLUMN run_start_point TEXT;

-- The verdict and its detail. INPUTS to the phase's diagnosis, never a second
-- status: checkboxes remain the single truth about progress (decision D5), and a
-- fail surfaces as a `verify-failed` blocker beside the outcome rather than
-- replacing it.
ALTER TABLE epic_phases ADD COLUMN verify_verdict TEXT;   -- pass | fail | inconclusive
ALTER TABLE epic_phases ADD COLUMN verify_detail  TEXT;

-- The opt-in, authored by the phase DOC's header (`**Verify:** strict`), which is
-- why it is doc-owned like seq/name/covers and re-derived on every scan — unlike
-- the three above, which are daemon-owned and must survive a doc rename.
-- Default off: a plan keeps today's behaviour unless its doc asks for verification.
ALTER TABLE epic_phases ADD COLUMN verify_mode TEXT NOT NULL DEFAULT 'off'; -- off | normal | strict
