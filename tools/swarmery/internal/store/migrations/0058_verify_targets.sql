-- Verify targets: verification's two tables stop being board-task-shaped.
--
-- §5.3 of the execution-engine unification generalizes the verifier's TARGET, not
-- its engine: a board card and a phase run are both "a worktree, a branch, a diff
-- base, and somewhere to stamp the verdict". The single-flight guard and the
-- tree-hash memo were keyed on tasks.id, which is the one thing those two targets do
-- NOT share — and a phase id parked in task_id would be a lie the foreign key
-- rejects outright.
--
-- target_key is "task:<tasks.id>" | "phase:<epic_phases.id>", the same
-- <engine>:<id> shape runcore.SlotKey already uses for run slots, so one glance at a
-- row says which surface it graded.
--
-- task_id survives, nullable, because it is a real FK with a real cascade: deleting
-- a board card must take its verification history with it, and the api layer's
-- in-flight probe (internal/api/verify.go) reads it. A phase row leaves it NULL
-- rather than gaining an epic_phases FK of its own: epic_phases identity is
-- doc_path, so its rows are REPLACED whenever a doc is renamed, and a cascade there
-- would erase the record of runs that really happened.
--
-- Both tables are rebuilt rather than ALTERed: SQLite cannot relax a NOT NULL, drop
-- a column from a primary key, or re-point a partial unique index in place. Nothing
-- references either table, so the copy is safe inside the migration transaction
-- (same shape as 0038_recommendations_target_kinds).

CREATE TABLE verification_runs_new (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  target_key          TEXT NOT NULL,        -- "task:<id>" | "phase:<id>"
  task_id             INTEGER REFERENCES tasks(id) ON DELETE CASCADE, -- NULL for a phase target
  session_id          INTEGER,             -- the VERIFIED dispatched session (FK-shaped; nullable)
  verify_session_uuid TEXT,                -- the verifier's OWN headless session uuid (explicit link)
  status              TEXT NOT NULL         -- running|pass|fail|inconclusive|error
                      CHECK (status IN ('running','pass','fail','inconclusive','error')),
  tree_hash           TEXT,                -- git HEAD^{tree} of the graded worktree
  detail              TEXT,                -- verdict reasons (<=4KB, truncated); 'cache' for a cache-hit row
  started_at          TEXT NOT NULL,
  finished_at         TEXT
);

INSERT INTO verification_runs_new
  (id, target_key, task_id, session_id, verify_session_uuid, status, tree_hash,
   detail, started_at, finished_at)
SELECT
  id, 'task:' || task_id, task_id, session_id, verify_session_uuid, status, tree_hash,
  detail, started_at, finished_at
FROM verification_runs;

DROP TABLE verification_runs;
ALTER TABLE verification_runs_new RENAME TO verification_runs;

CREATE INDEX idx_verification_task ON verification_runs(task_id);
CREATE INDEX idx_verification_target ON verification_runs(target_key);
-- Single-flight, now per TARGET: at most one in-flight verification per board card
-- OR per phase. The second concurrent VerifyTarget's INSERT hits this immediately,
-- which is what makes the INSERT itself the lock (durable, survives a restart).
CREATE UNIQUE INDEX idx_verification_running
  ON verification_runs(target_key)
  WHERE status = 'running';

-- The tree-hash memo, same generalization. (tree_hash, target_key) preserves the
-- original property — the same tree graded for a DIFFERENT target is a distinct
-- entry, because acceptance criteria differ per target — while admitting a phase.
-- Still pass/fail only: INCONCLUSIVE is never cached (a transient env failure must
-- not permanently wedge a target at amber).
CREATE TABLE verification_cache_new (
  tree_hash  TEXT NOT NULL,
  target_key TEXT NOT NULL,
  verdict    TEXT NOT NULL CHECK (verdict IN ('pass','fail')),
  created_at TEXT NOT NULL,
  PRIMARY KEY (tree_hash, target_key)
);

INSERT INTO verification_cache_new (tree_hash, target_key, verdict, created_at)
SELECT tree_hash, 'task:' || task_id, verdict, created_at FROM verification_cache;

DROP TABLE verification_cache;
ALTER TABLE verification_cache_new RENAME TO verification_cache;
