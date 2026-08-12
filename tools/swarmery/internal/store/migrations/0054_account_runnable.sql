-- The authoritative "can the CLI run under this account" verdict
-- (internal/claudeprobe). One row per account, latest verdict wins.
--
-- Keyed by account KEY (ingest.AccountFor's spelling, 'default' for ~/.claude),
-- not by config dir: the dir can move, the key is what every other table and the
-- project binding already speak.
--
-- Absence of a row means NEVER PROBED, which readers render as unknown — not as
-- "not ready". reason is a short fixed phrase from internal/claudeprobe; NO row
-- here ever carries credential material.
CREATE TABLE account_runnable (
  account    TEXT PRIMARY KEY,
  status     TEXT NOT NULL,           -- 'ready' | 'no-login' | 'unknown'
  reason     TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL,        -- unix seconds
  source     TEXT NOT NULL            -- 'probe' | 'run' (phase 4) | 'pty-login'
);
