-- Extend recommendations.target_kind to allow 'project' and 'session'.
--
-- The original CHECK (0019) listed only tool/agent/error_group/process/config.
-- R7 (stale architecture map) already emits target_kind='project' and R9 (fat
-- sessions) emits 'session' — both tripped the CHECK at upsert time (R7 was
-- masked because it rarely fires; R9 surfaced it). SQLite can't ALTER a CHECK
-- in place, so rebuild the table. No foreign keys reference recommendations,
-- so a straight copy is safe inside the migration transaction.

CREATE TABLE recommendations_new (
  id          INTEGER PRIMARY KEY,
  rule        TEXT NOT NULL,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('tool','agent','error_group','process','config','project','session')),
  target      TEXT NOT NULL,
  title       TEXT NOT NULL,
  detail      TEXT NOT NULL,
  evidence    TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'proposed'
              CHECK (status IN ('proposed','accepted','dismissed','adopted','verified')),
  dedup_key   TEXT NOT NULL UNIQUE,
  baseline    TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

INSERT INTO recommendations_new
  (id, rule, target_kind, target, title, detail, evidence, status, dedup_key, baseline, created_at, updated_at)
SELECT
  id, rule, target_kind, target, title, detail, evidence, status, dedup_key, baseline, created_at, updated_at
FROM recommendations;

DROP TABLE recommendations;
ALTER TABLE recommendations_new RENAME TO recommendations;
CREATE INDEX idx_recommendations_status ON recommendations(status);
