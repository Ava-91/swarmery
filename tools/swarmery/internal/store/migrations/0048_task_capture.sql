-- 0048: agent selection + session-capture provenance on board tasks.
--
-- Two features share one column set. `agent` is dispatch-time agent selection:
-- the registry agent name (agents.name, 0001_init) a board card should be run
-- as; NULL means a plain run with no agent preamble. It is deliberately the
-- NAME and not an agents.id FK — the registry is re-scanned from disk
-- (internal/sysscan) and rows are soft-deleted and re-minted, so an id would
-- dangle across a rescan while the name is the stable thing a dispatch line
-- actually needs.
--
-- The other three columns are capture provenance: where a card came from and
-- what makes it unique. `origin` is a closed set ('manual' | 'session' |
-- 'llm') validated in Go, defaulted to 'manual' so every pre-existing row —
-- and every future plain POST — reads as hand-written without a backfill.
-- `origin_session_id` points at the session a card was captured from, so a
-- later drawer can link back to it; NULL for manual cards.
--
-- `capture_key` is the idempotency key, and it is what makes re-capture safe:
-- capture runs re-read the same transcript (a re-tail, a daemon restart, a
-- second sweep of the same session), so the insert path must be replayable
-- without minting duplicate cards. Shapes: 'todo:<uuid>:<hash>' (one TODO item
-- of a session), 'sess:<uuid>' (the session itself), 'llm:<uuid>:<hash>' (an
-- LLM-suggested card). The uniqueness lives in a PARTIAL index rather than a
-- plain UNIQUE column so the NULLs of every manual card are simply not in the
-- index — SQLite treats NULLs as distinct in a UNIQUE index, but a partial
-- index also keeps them out of it entirely, which is the cheaper shape when
-- the overwhelming majority of rows are manual.
--
-- Writers use INSERT … ON CONFLICT(capture_key) WHERE capture_key IS NOT NULL
-- DO NOTHING (the partial index's predicate must be repeated in the conflict
-- target — SQLite requires it to resolve which index is meant).
ALTER TABLE tasks ADD COLUMN agent TEXT;
ALTER TABLE tasks ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE tasks ADD COLUMN origin_session_id INTEGER REFERENCES sessions(id);
ALTER TABLE tasks ADD COLUMN capture_key TEXT;

CREATE UNIQUE INDEX idx_tasks_capture_key ON tasks(capture_key) WHERE capture_key IS NOT NULL;
