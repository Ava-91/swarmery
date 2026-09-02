-- 0066: card provenance as columns, and the opening-prompt quote MOVED out of
-- prompt into one of them.
--
-- A captured card (origin 'session' | 'llm', 0048) so far recorded only WHICH
-- session it came from. The rest of what a human needs to judge it later —
-- which turn minted it, what the session was asked to do, which files it was
-- touching — was either not kept at all or, for the opening-prompt excerpt,
-- appended as prose to the card's prompt after the literal marker
-- "That session opened with:". Prose in the prompt column has two costs: the
-- board cannot render or trim it separately, and it rides into every
-- dispatched run verbatim, so the dispatcher could not add its own provenance
-- block without doubling the quote.
--
--   origin_turn_uuid  — the transcript record (envelope uuid) the card was
--                       minted from; NULL for manual cards.
--   origin_quote      — the session's opening prompt, clipped; NULL for manual.
--   origin_files      — JSON array of files the session had touched by the time
--                       the card was captured; NULL when none / manual.
--   dispatched_prompt — the exact first-stage prompt the dispatcher handed the
--                       runner, written at dispatch time; NULL until then.
--
-- The backfill below is a MOVE, not a copy: the text after the marker becomes
-- origin_quote and the marker plus the text are cut from prompt, so the quote
-- exists exactly once. Scoped to capture origins by the exact marker — a manual
-- card whose author happened to type the phrase is left untouched.
ALTER TABLE tasks ADD COLUMN origin_turn_uuid TEXT;
ALTER TABLE tasks ADD COLUMN origin_quote TEXT;
ALTER TABLE tasks ADD COLUMN origin_files TEXT;
ALTER TABLE tasks ADD COLUMN dispatched_prompt TEXT;

UPDATE tasks
   SET origin_quote = substr(
         prompt,
         instr(prompt, char(10, 10) || 'That session opened with:' || char(10))
           + length(char(10, 10) || 'That session opened with:' || char(10)))
 WHERE origin IN ('session', 'llm')
   AND origin_quote IS NULL
   AND instr(prompt, char(10, 10) || 'That session opened with:' || char(10)) > 0;

UPDATE tasks
   SET prompt = rtrim(
         substr(prompt, 1, instr(prompt, char(10, 10) || 'That session opened with:' || char(10)) - 1),
         char(10, 32))
 WHERE origin IN ('session', 'llm')
   AND origin_quote IS NOT NULL
   AND instr(prompt, char(10, 10) || 'That session opened with:' || char(10)) > 0;
