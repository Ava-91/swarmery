-- 0062: repair subagent durations that were recorded as the LAUNCH ROUNDTRIP
-- instead of the run.
--
-- An Agent tool_result normally reports totalDurationMs. When it reports
-- nothing usable — no status, no agentId, no duration — ingest kept the span
-- between the tool_use and its tool_result, which is the time it took to hand
-- the work over, not the time the work took. The sidechain reconciliation that
-- would have corrected it only ran for background launches (the
-- "async_launched" marker), so foreground calls with a silent result kept the
-- wrong number forever.
--
-- The damage is measurement, not data loss, and it is severe: in one live store,
-- six of seven `verification-agent` runs carried 8–24 parented sidechain events
-- spanning two to three MINUTES and were each recorded as ~1.8 seconds. A
-- retrospective read the resulting p95 and concluded the fleet's verifiers were
-- dying on start-up. They were not; the ruler was wrong.
--
-- The repair is the same rule ingest now applies, expressed once over history:
-- a run's duration is the span from its subagent_start to its LAST sidechain
-- event. Applied only where it GROWS the number, so a correctly-reported short
-- run can never be inflated, and re-running this is a no-op.
--
-- Scope, deliberately narrow — it touches only rows that are provably wrong:
--   * the stop row reports no status (the "result told us nothing" shape); a
--     stop that carried a real status is left alone, since there the tool did
--     report and its number is authoritative;
--   * the run has at least one non-stop child event to date it by;
--   * the recorded duration is SHORTER than that child span.

-- The start rows.
UPDATE events AS start
   SET duration_ms = (
        SELECT CAST((julianday(MAX(c.ts)) - julianday(start.ts)) * 86400000 AS INTEGER)
          FROM events c
         WHERE c.parent_event_id = start.id AND c.type != 'subagent_stop')
 WHERE start.type = 'subagent_start'
   AND EXISTS (SELECT 1 FROM events stop
                WHERE stop.parent_event_id = start.id
                  AND stop.type = 'subagent_stop'
                  AND COALESCE(json_extract(stop.payload, '$.status'), '') = '')
   AND EXISTS (SELECT 1 FROM events c
                WHERE c.parent_event_id = start.id AND c.type != 'subagent_stop')
   AND COALESCE(start.duration_ms, 0) < (
        SELECT CAST((julianday(MAX(c.ts)) - julianday(start.ts)) * 86400000 AS INTEGER)
          FROM events c
         WHERE c.parent_event_id = start.id AND c.type != 'subagent_stop');

-- The matching stop rows, kept equal to their start (the pair is read as one
-- run everywhere: analytics folds durations off the start, the agent hub reads
-- either, and a split pair would make the two disagree).
UPDATE events AS stop
   SET duration_ms = (SELECT s.duration_ms FROM events s WHERE s.id = stop.parent_event_id)
 WHERE stop.type = 'subagent_stop'
   AND COALESCE(json_extract(stop.payload, '$.status'), '') = ''
   AND EXISTS (SELECT 1 FROM events s
                WHERE s.id = stop.parent_event_id
                  AND s.type = 'subagent_start'
                  AND COALESCE(s.duration_ms, 0) > COALESCE(stop.duration_ms, 0));
