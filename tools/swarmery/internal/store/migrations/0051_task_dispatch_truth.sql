-- 0051: make verification measurable against the base the work actually forked
-- from, and stop two unrelated budgets sharing one counter.
--
-- start_point: the SHA the task's worktree was pinned to at admission
-- (worktree.Acquired.StartPoint). Verification diffs start_point...HEAD;
-- NULL on rows dispatched before this migration — consumers must fall back.
-- Before this column existed the verifier diffed the branch against ITSELF,
-- which is always empty, so the scope gate could never fire and the prompt's
-- "diff vs <ref>" instruction pointed at a no-op range.
ALTER TABLE tasks ADD COLUMN start_point TEXT;

-- verify_retry_count: the verify fix-chain budget, split out of retry_count,
-- which the dispatcher's no-progress heal also increments. One counter for
-- two unrelated budgets meant a flaky run could silently eat the fix budget.
-- retry_count stays dispatch-owned (HealDeadProcess); this one is verify-owned
-- (handleFail). Existing rows start at 0: a task that already burned dispatch
-- retries gets a full, honest fix budget — the conservative direction, since
-- the alternative is silently withholding fixes nobody ever authorised.
ALTER TABLE tasks ADD COLUMN verify_retry_count INTEGER NOT NULL DEFAULT 0;
