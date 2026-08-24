-- 0061: saved system analyses of a retro window (retro improvement loop).
--
-- Distinct from agent_change_proposals (0021), which cannot be reused here:
-- that table is wired to ONE agent definition file (agent, agent_path,
-- base_sha256, "one open proposal per agent") because its product is a diff.
-- This table's product is prose about the whole system — agents, skills,
-- commands, hooks and processes — and it is a decision record, not a patch.
--
-- Lifecycle: running → proposed → accepted | dismissed, then accepted → planned.
--   * running    — the headless improver is generating; the daemon owns the row
--   * proposed   — output passed validation (three required sections, at least
--                  one [E:kind:id] citation, the change section within budget)
--   * failed     — terminal-retriable: the runner errored, or validation
--                  rejected the output. `error` carries the human reason —
--                  an uncited analysis is a FAILURE, never a quiet 'proposed',
--                  because prose that looks checkable and is not is worse than
--                  no analysis at all
--   * accepted   — the operator's explicit gate; nothing is written to any
--                  repository or workspace before this
--   * dismissed  — rejected; can never start planning
--   * planned    — an accepted analysis was handed to Planning Mode, and
--                  planning_session_uuid points at that session
--
-- digest_sha256 pins the analysis to the exact evidence it was written from,
-- so a reader can tell whether the window has moved on underneath it.
CREATE TABLE retro_analyses (
  id            INTEGER PRIMARY KEY,
  window_from   TEXT NOT NULL,      -- YYYY-MM-DD, inclusive
  window_to     TEXT NOT NULL,      -- YYYY-MM-DD, inclusive
  scope         TEXT,               -- project slug; NULL = the whole fleet
  digest_sha256 TEXT NOT NULL,      -- sha256 of the digest the analysis was built on
  markdown      TEXT NOT NULL DEFAULT '',
  citations     INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'running'
                CHECK (status IN ('running','proposed','accepted','dismissed','planned','failed')),
  error         TEXT,               -- populated when status='failed'
  planning_session_uuid TEXT,       -- set when status becomes 'planned'
  created_at    TEXT NOT NULL,
  decided_at    TEXT
);

-- The UI only ever asks for the newest analysis (optionally within a scope).
CREATE INDEX idx_retro_analyses_created ON retro_analyses(created_at DESC);
