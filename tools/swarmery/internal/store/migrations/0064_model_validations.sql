-- 0064: model_validations — "did the fleet still behave on model X?", dated.
--
-- The fact the PreModelSwitch gate reads. Deliberately NOT folded into
-- trajectory_judgments: that table's `model` column is the JUDGE model
-- (provenance) and its UNIQUE follows the judge, while this row is keyed by
-- the SUBJECT model — the model the agents under test were running on, taken
-- from turns.model. Two meanings in one column is how the logs/sessions.md
-- pid-vs-uuid collision happened; once is enough.
--
-- Grain note: the verdict rests on judged TRAJECTORIES for the subject model,
-- because that is the grain the judging pipeline actually produces today —
-- trajectory_scores/trajectory_judgments hold agent='main' rows only. The
-- golden set's per-agent cases still matter: agents_covered reports how much of
-- the intended surface had evidence, so a pass built on one agent's runs is
-- visibly thinner than a pass built across the roster. When per-agent judging
-- lands, agents_covered starts moving without a schema change.
--
-- One row per (model, golden_set_version): re-running an eval for the same
-- pair updates in place, so "the newest verdict for model X" is never
-- ambiguous. Dropping the golden set version would make a stale pass look
-- current after the set changed, which is the failure mode this table exists
-- to prevent.
CREATE TABLE model_validations (
    id                 INTEGER PRIMARY KEY,
    model              TEXT NOT NULL,     -- SUBJECT model (turns.model), not the judge
    golden_set_version TEXT NOT NULL,
    verdict            TEXT NOT NULL,     -- pass | fail | inconclusive
    score              REAL,              -- mean trajjudge overall, 1..5
    trajectories       INTEGER NOT NULL DEFAULT 0,  -- judged trajectories on this model
    agents_covered     INTEGER NOT NULL DEFAULT 0,  -- distinct golden-set agents with evidence
    detail             TEXT,              -- short human note (why fail / what was thin)
    created_at         TEXT NOT NULL,
    UNIQUE(model, golden_set_version)
);

-- The gate's only query: newest verdict for one model.
CREATE INDEX idx_model_validations_model ON model_validations(model, created_at DESC);
