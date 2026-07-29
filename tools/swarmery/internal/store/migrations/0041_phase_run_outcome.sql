-- 0041: phase-run outcome measurement. run_state answers "how did the process end";
-- it cannot answer "did work land". run_checkboxes_before snapshots the phase's
-- ticked-criteria count at spawn time so the delta across a run is measurable
-- (0 delta on a 'done' run ⇒ the executor produced nothing — the case that shipped
-- a green "Run done" chip on a 0/7 phase). run_ended_at gives the UI a duration;
-- only run_started_at was persisted before. Both nullable, and NULL means
-- UNMEASURED — NOT zero. Reading a NULL baseline as 0 would derive a delta equal to
-- the phase's entire ticked count and report a run that "landed" criteria which may
-- predate it entirely (a pre-0041 row at checkboxes_done=7, run_state='done' would
-- claim 7). Readers must treat NULL as "no measurement exists" and refuse to claim
-- progress they cannot prove.
ALTER TABLE epic_phases ADD COLUMN run_checkboxes_before INTEGER;
ALTER TABLE epic_phases ADD COLUMN run_ended_at TEXT;
