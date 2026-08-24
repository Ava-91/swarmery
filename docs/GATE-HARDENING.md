# Gate hardening — the warn→block decision record

A guard that starts blocking on a **date** is a guard that gets switched off. This document is the
gate for every warn-mode hook rule in `plugins/core/hooks/`: a rule moves from `warn` to `block` when
its row here is filled from counted hits and a false-positive review — never because a date arrived.

The rule is deliberately conservative, and it comes from the guards' own experience: *a rule that is
too quiet costs one retro line, a rule that is too loud costs the whole guard.*

## How to fill a row

1. Read the counts:

   ```bash
   scripts/guard-hits.sh --from 2026-08-24        # per-rule hits + distinct sessions
   scripts/guard-hits.sh --rule multi-mutation --raw   # the actual commands, to review
   ```

   The reader resolves the same log path the hook writes to (`bash-shape-guard.jsonl` in the
   project's workspace metrics dir; override with `BASH_SHAPE_GUARD_LOG`).

2. Review **every** hit for that rule, or a documented sample if the count is large, and decide for
   each whether the command it refused was genuinely malformed. Record how many were not.
3. Fill the row: hits, sessions, false positives, decision, date reviewed.
4. Only then raise that rule's exit code in the hook. Enforcement is **per rule** — one noisy rule
   must not hold back the clean ones, and must not be dragged into blocking by them.

A rule with an unreviewed false positive stays in `warn`, independently of every other rule.

## `bash-shape-guard.sh`

Log: `bash-shape-guard.jsonl` · Reader: `scripts/guard-hits.sh` · Review deadline: **2026-10-01**
(`ENFORCE_FROM` in the hook is that deadline, not a trigger — nothing in the hook reads it to decide
anything.)

| Rule | Hits | Sessions | False positives reviewed | Decision | Reviewed on |
|---|---|---|---|---|---|
| `heredoc` | not yet counted | not yet counted | not yet reviewed | stay in warn | — |
| `multi-mutation` | not yet counted | not yet counted | not yet reviewed | stay in warn | — |
| `sleep-before-read` | not yet counted | not yet counted | not yet reviewed | stay in warn | — |

**Why the counts are empty.** The counter shipped with this document; the burn-in window has not been
read yet. The three rules shipped earlier in warn mode with their decisions going only to stderr,
which reaches the model but is not durably queryable — so no honest number exists for the period
before the counter. Do not backfill these rows from transcripts; start the window at the counter.

## Rules retired from this table

None yet. When a rule reaches `block`, leave its row in place with the evidence that justified the
flip — the row is the record of *why*, and deleting it turns an argued decision back into a bare
constant in a script.
