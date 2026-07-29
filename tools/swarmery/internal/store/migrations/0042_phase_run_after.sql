-- 0042: close the phase-run checkbox interval. run_checkboxes_before (0041) is a
-- closed left edge against a LIVE right edge — checkboxes_done keeps moving after
-- the run ends (the wsingest rescan and TickPhaseChecklist both write it), so a
-- delta computed later can attribute another writer's ticks to this run. Stamping
-- the count at exit closes the interval at the same instant run_ended_at is written.
ALTER TABLE epic_phases ADD COLUMN run_checkboxes_after INTEGER;
