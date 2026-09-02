-- 0065: delete the judgments trajjudge made of its OWN scoring runs.
--
-- ClaudeRunner executes the judge with cwd ~/.swarmery so its transcripts are
-- readable, which means ingest records every scoring call as an ordinary
-- session. Those sessions were then eligible candidates, so the judge graded
-- its own output. Each is a 2-turn exchange containing no work trajectory, so
-- it scored at the floor of the rubric.
--
-- Measured before this ran: 29 of 595 judgments (4.9%) were self-judgments,
-- averaging 1.20 against 3.01 for real work. 27 of the 29 fell on a single
-- model, dragging its mean to 1.27 and making it look catastrophically worse
-- than every other model on evidence that was never about a model at all. The
-- model-switch gate reads these means, so leaving the rows would have it block
-- a model for the judge's own bookkeeping.
--
-- selectCandidates now excludes these sessions, so this is a one-time cleanup
-- of rows already written. Precedent for data repair in a migration:
-- 0055_drop_phantom_sessions.sql.
DELETE FROM trajectory_judgments
 WHERE session_id IN (
   SELECT DISTINCT session_id FROM turns
    WHERE text LIKE 'You are an expert reviewer scoring one AI coding agent''s execution trajectory.%'
 );
