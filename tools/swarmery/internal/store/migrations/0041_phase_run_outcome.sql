-- 0041: phase-run outcome measurement. run_state answers "how did the process end";
-- it cannot answer "did work land". run_checkboxes_before snapshots the phase's
-- ticked-criteria count at spawn time so the delta across a run is measurable
-- (0 delta on a 'done' run ⇒ the executor produced nothing — the case that shipped
-- a green "Run done" chip on a 0/7 phase). run_ended_at gives the UI a duration;
-- only run_started_at was persisted before. Both nullable: historical rows read as
-- a 0 baseline, which is correct for them.
ALTER TABLE epic_phases ADD COLUMN run_checkboxes_before INTEGER;
ALTER TABLE epic_phases ADD COLUMN run_ended_at TEXT;
