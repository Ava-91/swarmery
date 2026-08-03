---
name: jira-triage
description: "Parse a Jira ticket past access preflight, run its mandatory reproduction, and classify it into exactly one of five verdicts (already-fixed / cannot-reproduce / needs-fix / needs-info / too-large). NOT for posting the verdict back to Jira (that's jira-writeback) and NOT for resolving jira config or the working repo (that's jira-config)."
version: "0.1.0"
owner: "swarmery-core"
---

# Purpose

Turn a ticket that `jira-access-preflight` has already smoke-read into a
decision: is this actually fixed, genuinely not reproducing, in need of a
real fix, impossible to even attempt yet, or too large to touch under
`/jira-fix`'s autonomy budget? This skill is the only place in `jira-pack`
that runs a reproduction command and assigns a verdict — `jira-writeback`
downstream only renders and posts whatever this skill decided.

# When to use

Called by `@jira-task-runner`'s step 4, strictly after `jira-config` (working
repo + `jira.repro.*` resolved), `jira-access-preflight` (tools pinned,
`cloudId` resolved, ticket smoke-read already succeeded), and
`swarmery-board-card` (card already sitting in `in_progress`). Never run this
skill's reproduction step against an unvalidated repo path or before access
preflight has passed — a failed smoke-read means there is no ticket to triage
yet.

# Step 1 — parse the ticket

Call `getJiraIssue` with
`fields: ["summary", "description", "status", "issuetype", "labels", "components", "comment"]`
(a fuller read than preflight's own smoke-read, which only pulled
`summary`/`status`/`description`/`comment`). Extract:

- the problem description and any "Steps to reproduce" section or its
  equivalent (numbered list, a fenced repro script, a linked test);
- expected vs. actual behavior, as stated;
- any file paths, stack traces, or commit references mentioned in the
  description or comments;
- **prior comments** — read every one. A previous comment often already says
  "fixed in `<commit>`" or "duplicate of `<KEY>`"; missing this is the most
  common way a run wastes its reproduction budget on an already-closed
  question.

# Step 2 — mandatory reproduction

Run `jira.repro.setup` (if the config sets it — absent means skip this
sub-step, not an error) and then `jira.repro.test`, in the working repo
`jira-config` resolved, through `plugins/core/skills/testing`. This is not
optional and not skippable on the theory that the ticket "looks simple" —
every verdict except `too-large` (see the early-exit note below) requires this
step to have actually run.

Capture, verbatim, for whichever verdict follows:

- the **exact command** executed (`jira.repro.setup` and/or `jira.repro.test`
  as configured, plus any additional command from the ticket itself — see
  below);
- its **exit code**;
- a **relevant fragment** of its output — trimmed to the meaningful part, not
  thousands of lines of raw log pasted into a future Jira comment.

If the ticket names a more specific way to reproduce (a dedicated test file, a
script, a particular CLI invocation) — run **that too**, in addition to
`jira.repro.test`. `jira.repro.test` is the baseline every ticket gets; it is
not a ceiling that excuses skipping a more targeted repro the ticket itself
already hands you.

**Budget-bounded retries.** If the *setup* step (or `jira.repro.test` itself)
fails to even execute — missing environment, a dependency that isn't
installed, an unknown service — retry up to `jira.budget.maxAttempts` times
(the same ceiling `jira-config` defaults to 3, matched to
`plugins/core/agents/debugger.md`'s own stop condition). This retry budget is
for **transient/environment** failures only, not for iterating on a fix — this
skill never edits code. Exhausting the budget without a single successful
execution is itself the signal for the `needs-info` verdict below, not a
reason to keep retrying indefinitely.

**Early exit for obvious `too-large` tickets.** A ticket that is self-evidently
an epic or a multi-stage feature (per Step 1's read alone) may be classified
`too-large` **without** running Step 2 at all — a reproduction command has no
meaningful target against an epic. Every other verdict below requires Step 2
to have actually executed.

# Step 3 — classification

Assign **exactly one** of these five states:

| Verdict | Condition | What happens next |
|---|---|---|
| `already-fixed` | The reproduction runs **green**, **and** a commit or test is found that closes the exact behavior described | Comment with the closing commit link + the passing run's evidence, then QA transition |
| `cannot-reproduce` | The reproduction was **executed**, the ticket's reported behavior did **not** occur, and no explanation for the discrepancy was found | Comment with the command + output, then QA transition |
| `needs-fix` | The reproduction runs **red**, or the reported behavior did occur | Phase 7 (delegated fix) |
| `needs-info` | The reproduction **could not be run at all** (no environment, missing repro steps, an unknown/undocumented service) | Comment with concrete questions; **status unchanged** |
| `too-large` | The scope obviously exceeds `jira.budget` before any fix work starts (epic, multi-stage feature) | Phase 7 (escalation to planning) |

## The rule that carries this skill

**"Could not run the reproduction" is not the same verdict as "ran it and it
did not reproduce."**

- Could not run it at all → `needs-info`, and `needs-info` carries **no**
  status transition, ever.
- Ran it, and the reported behavior genuinely did not occur → `cannot-reproduce`,
  and `cannot-reproduce` **does** carry a transition attempt.

A `cannot-reproduce` verdict is **impossible** without an executed command and
a captured fragment of its output — there is no code path that reaches
`cannot-reproduce` from "I read the ticket and it sounds unlikely" or from a
setup failure. Phase 8 enforces this with a golden test case: any run that
reports `cannot-reproduce` without an attached command + output is a bug in
this skill, not an acceptable shortcut. When Step 2 fails to execute for any
reason, the only two admissible verdicts are `needs-info` (budget exhausted
trying) or `too-large` (recognized before Step 2 ever started) — never
`cannot-reproduce`.

# Output (handed to `@jira-task-runner` / `jira-writeback`)

- The verdict (exactly one of the five).
- The evidence bundle backing it: command(s) run, exit code(s), the trimmed
  output fragment, and — for `already-fixed` — the commit/test reference that
  closes the ticket.
- For `needs-info`: the specific step that could not run and why, phrased as
  concrete questions (this becomes `comment-needs-info.md`'s question list,
  not a vague "couldn't reproduce").
- For `too-large`: the reason the scope exceeds budget, handed to Phase 7's
  `jira-escalation` rather than rendered here.

This skill never calls `addCommentToJiraIssue` or `transitionJiraIssue`
itself — `jira-writeback` owns every write, and renders whichever
`plugins/jira-pack/templates/` file matches the verdict above.

# Placeholders / neutrality

Any example this skill's report prints uses only `<PROJECT-KEY>`-shaped keys
and `<jira-base-url>`-style hosts — no real ticket key, hostname, or team name
(`docs/NEUTRALITY.md`; `scripts/scan-flavor.sh` must stay `✓ clean` for
`plugins/**`).

# Self-check before returning

- [ ] Step 2 actually executed, unless the ticket was classified `too-large`
      via the early-exit note
- [ ] `cannot-reproduce` is never assigned without an executed command + a
      captured output fragment
- [ ] `needs-info` never carries a transition recommendation
- [ ] Prior comments were read before concluding `needs-fix`/`cannot-reproduce`
      (an already-fixed-in-`<commit>` comment would have changed the verdict)
- [ ] Exactly one verdict assigned — never two, never "leaning towards"

# Common mistakes to avoid

- Assigning `cannot-reproduce` because the description "sounds like it
  shouldn't happen" without running anything — that is a guess, and guesses
  are what this skill exists to replace with an executed command.
- Retrying `jira.repro.test` indefinitely when the failure is a genuine
  assertion failure (the bug is real) rather than an environment problem —
  that is `needs-fix`, not a retry target.
- Treating `jira.repro.test` as the ceiling of reproduction effort when the
  ticket names a more specific repro path.
- Skipping the previous-comments read and missing an existing "fixed in X".

# Related

- `plugins/jira-pack/skills/jira-config/SKILL.md` — resolves the working repo
  and `jira.repro.setup`/`jira.repro.test`/`jira.budget` this skill consumes.
- `plugins/jira-pack/skills/jira-access-preflight/SKILL.md` — must have fully
  passed, including the ticket smoke-read, before this skill's Step 1 runs.
- `plugins/core/skills/testing/SKILL.md` — how the reproduction commands are
  actually run.
- `plugins/jira-pack/skills/jira-writeback/SKILL.md` — consumes this skill's
  verdict + evidence bundle and owns every Jira write.
- `plugins/core/agents/debugger.md` — source of the `jira.budget.maxAttempts`
  default this skill's retry ceiling matches.
