-- 0063: `resolved` — a recommendation whose condition stopped reproducing.
--
-- The lifecycle had only human verbs plus a measured one: proposed →
-- accepted | dismissed → adopted → verified. Nothing described the most common
-- honest ending, which is that the thing simply stopped happening: the stale
-- map was refreshed, the error group went quiet, the fat session was never
-- repeated.
--
-- Without that state a recommendation row outlives its own rule. In the live
-- store, both R7 (stale architecture map) rows sat open and actionable — one of
-- them created three weeks earlier — while a fresh advisor pass proposed
-- nothing at all, because both maps were current. The rule had gone quiet; its
-- rows had not. So the Retro page could only ever accumulate, and an operator
-- who had just executed an improvement plan saw a screen identical to the one
-- before it and concluded, reasonably, that nothing had happened.
--
-- `resolved` is deliberately distinct from the neighbours it could have been
-- folded into:
--   * `dismissed` — a person judged it not worth doing;
--   * `verified`  — the improvement was measured against a baseline;
--   * `resolved`  — nobody judged and nothing was measured; the condition is
--                   gone. Collapsing this into either of the others loses the
--                   one signal the page was missing.
--
-- SQLite cannot ALTER a CHECK constraint, so the table is rebuilt. Data is
-- copied verbatim: no row changes status here, the vocabulary only widens.

PRAGMA foreign_keys = OFF;

CREATE TABLE recommendations_new (
  id          INTEGER PRIMARY KEY,
  rule        TEXT NOT NULL,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('tool','agent','error_group','process','config','project','session')),
  target      TEXT NOT NULL,
  title       TEXT NOT NULL,
  detail      TEXT NOT NULL,
  evidence    TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'proposed'
              CHECK (status IN ('proposed','accepted','dismissed','adopted','verified','resolved')),
  dedup_key   TEXT NOT NULL UNIQUE,
  baseline    TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

INSERT INTO recommendations_new
  (id, rule, target_kind, target, title, detail, evidence, status, dedup_key, baseline, created_at, updated_at)
SELECT id, rule, target_kind, target, title, detail, evidence, status, dedup_key, baseline, created_at, updated_at
  FROM recommendations;

DROP TABLE recommendations;
ALTER TABLE recommendations_new RENAME TO recommendations;
CREATE INDEX idx_recommendations_status ON recommendations(status);

PRAGMA foreign_keys = ON;
