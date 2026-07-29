-- 0043: stop deriving a phase run's git branch from the row id. phaserun built the
-- branch as "swarm/phase-" || id, so the branch was only findable while the row kept
-- its id — and epic_phases identity is doc_path (wsingest applyEpics upserts on
-- UNIQUE(workspace_task_id, doc_path) and prunes by exclusion). A renamed or
-- regenerated phase doc is therefore a delete + insert: the new row gets a new id and
-- the branch the run committed to becomes unreachable, along with its commits.
--
-- Recording the branch at spawn makes it survive any later change of identity. The
-- backfill pins existing non-idle rows to the name that was in force when they ran, so
-- the stored and the previously-derived value agree from the first boot on this schema.
ALTER TABLE epic_phases ADD COLUMN run_branch TEXT;

UPDATE epic_phases SET run_branch = 'swarm/phase-' || id WHERE run_state <> 'idle';
