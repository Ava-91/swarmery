-- Why the wizard is answerable again. A resume that fails (dead cwd, missing
-- binary, a `claude -r` that cannot find the transcript) rolls the row back to
-- awaiting_answer, which is indistinguishable from "the planner asked the same
-- question again" — the operator re-answers forever with no signal. The reason
-- is stamped here on rollback and cleared the moment the next action starts.
ALTER TABLE planning_sessions ADD COLUMN last_error TEXT;
