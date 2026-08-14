-- One-off cleanup of PHANTOM sessions: rows minted from a transcript that
-- carried no record timestamp at all.
--
-- Claude Code sometimes writes a transcript whose only line is
--   {"type":"ai-title","aiTitle":"…","sessionId":"…"}
-- with no envelope record — so no cwd, no clock. Ingest used to mint a
-- session row from it: started_at '', project '(unknown)', status frozen at
-- whatever the mtime heuristic guessed on insert. Frozen at 'active' those
-- rows reported dozens of live agents that never existed, rendered as
-- "unknown day / started —", and broke keyset pagination (an '' started_at
-- cursor was rejected as malformed).
--
-- The three defects are fixed in code (ingest refuses to mint such a row,
-- RecomputeStatuses closes any timestamp-less row, the cursor accepts an
-- empty started_at). This migration removes the rows already in the database.
--
-- DELETE, not hide: a phantom carries no content and no references, so there
-- is nothing to preserve — the predicate below is deliberately paranoid and
-- deletes ONLY rows that are provably empty and unreferenced. Any row that
-- has a single turn, event, file change, permission request, task link,
-- eval/trajectory score or handoff attached is left untouched, and so is any
-- row that ever learned a start time, a cwd, a pid, or an approval.
--
-- The cwd / pid / status guards are what keep a LIVE session safe: a row the
-- hooks channel minted for a session that has not written its first
-- timestamped record yet still carries the cwd the hook posted (and usually a
-- pid), so it does not match here.
DELETE FROM sessions
 WHERE (started_at IS NULL OR started_at = '')
   AND (cwd IS NULL OR cwd = '' OR cwd = '(unknown)')
   AND pid IS NULL
   AND status <> 'waiting_approval'
   AND NOT EXISTS (SELECT 1 FROM turns                WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM events               WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM file_changes         WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM permission_requests  WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM tasks                WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM eval_results         WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM task_sessions        WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM trajectory_scores    WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM trajectory_judgments WHERE session_id = sessions.id)
   AND NOT EXISTS (SELECT 1 FROM handoffs             WHERE session_id = sessions.id);

-- The deletions may have orphaned the '(unknown)' placeholder project. Same
-- predicate as ingest.DropUnknownProjectIfOrphaned (internal/ingest/heal.go)
-- so the migration and the live path can never disagree about when the
-- placeholder is safe to drop.
DELETE FROM projects
 WHERE path = '(unknown)'
   AND NOT EXISTS (SELECT 1 FROM sessions      WHERE project_id = projects.id)
   AND NOT EXISTS (SELECT 1 FROM tasks         WHERE project_id = projects.id)
   AND NOT EXISTS (SELECT 1 FROM agents        WHERE project_id = projects.id)
   AND NOT EXISTS (SELECT 1 FROM skills        WHERE project_id = projects.id)
   AND NOT EXISTS (SELECT 1 FROM daily_rollups WHERE project_id = projects.id)
   AND NOT EXISTS (SELECT 1 FROM workspaces    WHERE project_id = projects.id);
