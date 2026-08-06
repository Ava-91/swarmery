---
name: jira-triage
description: "Parse a Jira ticket past access preflight, classify it as a defect or a change, run the mandatory evidence step that class demands (reproduction / baseline + absence proof), and assign exactly one of five verdicts (already-fixed / cannot-reproduce / needs-fix / needs-info / too-large). NOT for posting the verdict back to Jira (that's jira-writeback) and NOT for resolving jira config or the working repo (that's jira-config)."
version: "0.2.0"
owner: "swarmery-core"
docs:
  status: generated
  source_sha: 0d923e9ce784
  updated: 2026-08-06
---

# Purpose

Turn a ticket that `jira-access-preflight` has already smoke-read into a
decision: is this actually done, genuinely not reproducing, in need of real
work, impossible to even attempt yet, or too large to touch under
`/jira-fix`'s autonomy budget? This skill is the only place in `jira-pack`
that runs the evidence step and assigns a verdict — `jira-writeback`
downstream only renders and posts whatever this skill decided.

**A tracker holds two kinds of work, and only one of them reproduces.** A
defect describes behavior that exists and is wrong; a change (feature, task,
improvement) describes behavior that does not exist yet. Running a defect's
reproduction against a change ticket returns green — the suite is fine, the
feature simply isn't there — and reading that green as "the reported behavior
did not occur" would classify unimplemented work as `cannot-reproduce` and
move it to QA. Step 1b exists to make that outcome unreachable: every ticket
is classified **before** the evidence step, and the evidence step is chosen by
that class.

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

# Step 1b — classify the ticket: `defect` or `change`

Assign exactly one **class** before running anything. This is not the verdict;
it decides which evidence Step 2 must produce, and which verdicts Step 3 is
even allowed to reach.

| Class | The ticket describes | Signals |
|---|---|---|
| `defect` | Behavior that **exists today and is wrong** — a crash, a wrong value, a regression | issuetype `Bug`; "steps to reproduce"; expected-vs-actual; a stack trace, error text, or failing build; "used to work", "since `<version>`" |
| `change` | Behavior that **does not exist yet** — a feature, a task, an improvement, a refactor | issuetype `Task`/`Story`/`Feature`/`Improvement`/`Epic`; imperative phrasing ("add", "disable X until Y", "support Z"); acceptance criteria instead of repro steps; no current-behavior complaint |

**`issuetype` is a signal, not the decision.** Trackers are configured by
humans: bugs get filed as `Task`, and feature requests get filed as `Bug`
because that was the quickest form. Weigh the ticket's own text over its type
field, and when the two disagree, say so explicitly in the report — the class
you acted on and the type that suggested otherwise.

**When the class is genuinely ambiguous** — the text supports both readings and
no acceptance criteria or repro steps settle it — do **not** pick one to keep
the run moving. Classify the verdict `needs-info` (Step 3) with the specific
question that would settle it: is `<X>` currently broken, or does `<X>` not
exist yet? Guessing `defect` on a change ticket produces exactly the
`cannot-reproduce`-on-unimplemented-work outcome this skill exists to prevent;
guessing `change` on a defect wastes an implementation budget re-building
something that only needed a fix.

The class travels with the verdict through the entire run: it is part of this
skill's output, `jira-writeback`'s comment marker, and `jira-delivery`'s branch
naming and executor choice.

# Step 2 — mandatory evidence, chosen by class

**Both classes have a mandatory, executed evidence step. Neither may be
skipped on the theory that the ticket "looks simple".** What differs is what
counts as evidence.

## Step 2a — `class: defect` → reproduction

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

## Step 2b — `class: change` → baseline + absence proof

A change ticket has no reported behavior to reproduce, so the evidence that
replaces the reproduction is a pair: the suite is green **now**, and the
described behavior is genuinely **not there yet**. Both halves are executed,
neither is asserted from reading the ticket.

1. **Baseline run.** Run `jira.repro.setup` (when configured) and
   `jira.repro.test` in the working repo, through
   `plugins/core/skills/testing` — the same commands, the same capture rules as
   Step 2a. Its purpose here is different: it establishes that the suite is
   green *before* this run touches anything, so a red suite later in
   `jira-delivery` is unambiguously this run's own doing and not pre-existing
   breakage. A baseline that comes back **red** is not this ticket's business:
   report it as part of the evidence bundle and treat the pre-existing failures
   as out of scope for the change (they do not turn the ticket into a
   `defect`).

2. **Absence proof.** Search the working repo for the behavior the ticket asks
   for — the component, handler, flag, or endpoint it names — with `Grep`/`Read`
   against real paths. Capture what you searched for and what you found, in the
   same shape as a command's evidence:
   - **not found / found but demonstrably not doing what the ticket asks** →
     the change is genuinely unimplemented → `needs-fix` (Step 3);
   - **found, and it already does exactly what the ticket describes** →
     `already-fixed`, with the file/symbol reference (and, when a test covers
     it, that test's passing run) as the evidence — the same bar the defect
     path's `already-fixed` has to clear.

3. **Acceptance criteria, made testable.** Extract the ticket's acceptance
   criteria into a numbered list of statements that a test could assert
   ("the LAND control is disabled while any unit reports airborne; it enables
   once every unit reports landed"). This list is what `jira-delivery`'s
   test-first step turns into the failing test, so its quality is load-bearing:
   a criterion no test could assert is not a criterion.

   **If no testable criterion can be extracted** — the ticket says "improve the
   UX" or "make it better" with nothing observable — the verdict is
   `needs-info` with those questions, **not** `needs-fix` handed to an executor
   to invent a specification. That is the change-class equivalent of "could not
   run the reproduction".

## Shared rules (both classes)

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

Assign **exactly one** of these five verdicts. The verdict set is unchanged by
class — what changes is which conditions reach it, and which verdicts a class
can reach at all:

| Verdict | `class: defect` condition | `class: change` condition | What happens next |
|---|---|---|---|
| `already-fixed` | The reproduction runs **green**, **and** a commit or test is found that closes the exact behavior described | Step 2b's search finds the behavior **already implemented** and doing what the ticket asks (file/symbol reference, plus a covering test's passing run when one exists) | Comment with the evidence, then QA transition |
| `cannot-reproduce` | The reproduction was **executed**, the ticket's reported behavior did **not** occur, and no explanation for the discrepancy was found | **UNREACHABLE — see the rule below** | Comment with the command + output, then QA transition |
| `needs-fix` | The reproduction runs **red**, or the reported behavior did occur | The baseline ran, the absence proof shows the behavior is missing, **and** at least one testable acceptance criterion was extracted | `jira-delivery` (delegated work; test-first for `change`) |
| `needs-info` | The reproduction **could not be run at all** (no environment, missing repro steps, an unknown/undocumented service) | The baseline could not be run at all, **or** no testable acceptance criterion could be extracted, **or** the class itself is ambiguous (Step 1b) | Comment with concrete questions; **status unchanged** |
| `too-large` | The scope obviously exceeds `jira.budget` before any fix work starts (epic, multi-stage feature) | Same — an epic, or acceptance criteria spanning more than `jira.budget.maxFiles` worth of work | `jira-escalation` (plan, no transition) |

## The two rules that carry this skill

### 1. A `change` ticket can never be `cannot-reproduce`

**A green run on work that was never implemented is the expected starting
state, not a finding.** For `class: change`, `cannot-reproduce` is not a
verdict this skill may assign under any circumstance — there was nothing to
reproduce, so "it did not reproduce" says nothing about the ticket. The
admissible verdicts for a change ticket are `needs-fix`, `already-fixed`,
`needs-info`, and `too-large`, full stop.

This is the rule that keeps unimplemented work out of QA. Without it, the
mechanical path is: feature ticket → suite green → `cannot-reproduce` →
comment + transition to `jira.qaStatus` on a ticket nobody has implemented,
which no part of this pack can undo automatically.

The mirror rule holds too: a `defect` ticket whose reproduction runs green is
`cannot-reproduce` (or `already-fixed` when a closing commit explains the
green) — never `needs-fix` on the theory that the reporter must have been
right.

### 2. "Could not run the evidence step" is not the same verdict as "ran it and the behavior did not occur"

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

- The **class** (`defect` or `change`) and the signals it rests on — including
  a note when the ticket's `issuetype` pointed the other way.
- The verdict (exactly one of the five).
- The evidence bundle backing it: command(s) run, exit code(s), the trimmed
  output fragment, and — for `already-fixed` — the commit/test/symbol reference
  that closes the ticket.
- For `class: change` + `needs-fix`: the numbered **testable acceptance
  criteria** from Step 2b.3 and the **absence proof** (what was searched, where,
  what was found) — `jira-delivery` builds the failing test from exactly this
  list, so it is handed over verbatim rather than re-derived downstream.
- For `needs-info`: the specific step that could not run and why, or the
  criteria that could not be made testable, or the class ambiguity — phrased as
  concrete questions (this becomes `comment-needs-info.md`'s question list, not
  a vague "couldn't reproduce").
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

- [ ] A class (`defect`/`change`) was assigned in Step 1b **before** anything
      was executed, and it is stated in the output
- [ ] Step 2a (defect) or Step 2b (change) actually executed, unless the ticket
      was classified `too-large` via the early-exit note
- [ ] `cannot-reproduce` was not assigned to a `class: change` ticket — under
      any circumstance, for any reason
- [ ] `cannot-reproduce` is never assigned without an executed command + a
      captured output fragment
- [ ] `needs-fix` on a `change` ticket carries both the absence proof and at
      least one testable acceptance criterion — never an executor brief that
      leaves the specification to be invented downstream
- [ ] `needs-info` never carries a transition recommendation
- [ ] Prior comments were read before concluding `needs-fix`/`cannot-reproduce`
      (an already-fixed-in-`<commit>` comment would have changed the verdict)
- [ ] Exactly one verdict assigned — never two, never "leaning towards"

# Common mistakes to avoid

- **Classifying a feature ticket as `cannot-reproduce` because
  `jira.repro.test` came back green.** This is the single failure this skill's
  Step 1b exists to prevent: the suite is green because the feature was never
  built, and the verdict transitions unimplemented work to QA.
- Trusting `issuetype` over the ticket's text when the two disagree, in either
  direction — a `Bug`-typed feature request and a `Task`-typed crash report are
  both routine.
- Handing `jira-delivery` a `change` + `needs-fix` verdict with no testable
  acceptance criteria, expecting the executor to invent the specification —
  that is `needs-info`.
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

# How to use

## What it does

This skill turns a tracker ticket into one decision you can act on. It reads the ticket in full, decides whether the ticket describes a defect (behavior that exists and is wrong) or a change (behavior that does not exist yet), runs the evidence step that class demands, and assigns exactly one verdict out of five: `already-fixed`, `cannot-reproduce`, `needs-fix`, `needs-info`, or `too-large`. The class comes first on purpose — running a defect's reproduction against a feature ticket returns green, and reading that green as "did not reproduce" would send unimplemented work to QA.

## When to use it

- You are inside a ticket run and access preflight has already passed, so the ticket has been read and the tools are pinned.
- You have a ticket and need to know whether there is real work to do before spending an implementation budget on it.
- A feature ticket keeps coming back green from the test suite and you need the verdict to reflect "not built yet", not "not reproducible".
- You need an evidence bundle — commands, exit codes, output fragments — to attach to whatever decision gets posted.

## When not to use it

- To post the comment or move the ticket to QA — that is `jira-writeback`, which owns every write.
- To resolve the working repo, the reproduction commands, or the budget — that is `jira-config`, and it runs first.
- To verify tool access or read the ticket for the first time — that is `jira-access-preflight`.
- To actually implement the fix once the verdict is `needs-fix` — that is `jira-delivery`.

## How to invoke

```
Skill(skill: "jira-pack:jira-triage")
```

Call it after config, access preflight, and the board card are done. It takes no arguments — it works from the ticket and config the run already resolved.

## Inputs

- Ticket key or reference — the ticket already smoke-read by preflight — required.
- Working repo path — resolved by `jira-config` — required.
- `jira.repro.setup` / `jira.repro.test` — the commands the evidence step runs — setup optional, test required.
- `jira.budget.maxAttempts` — retry ceiling for environment failures only — optional, defaults to 3.

## What you get back

A report, not a Jira write. It contains the class and the signals behind it (including a note when the ticket's type pointed the other way), exactly one verdict, and the evidence bundle: the commands run, their exit codes, and a trimmed output fragment. For a change ticket headed to `needs-fix` you also get the absence proof and a numbered list of testable acceptance criteria, handed downstream verbatim. For `needs-info` you get concrete questions instead of a vague complaint.

## Worked example

```
Skill(skill: "jira-pack:jira-triage")

Ticket <PROJECT-KEY>-412, typed "Bug": "LAND control stays enabled while units
are airborne."

Step 1b   → class: change (imperative phrasing, acceptance criteria, no
            current-behavior complaint; issuetype disagreed — noted)
Step 2b.1 → baseline: `npm test` exit 0, suite green
Step 2b.2 → absence proof: grep for the LAND control's disabled state in
            apps/<mainApp> — control renders, no airborne guard
Step 2b.3 → criteria: 1) disabled while any unit reports airborne
                      2) enabled once every unit reports landed
Step 3    → needs-fix
```

You end up with a `needs-fix` verdict, the baseline run, the absence proof, and two testable criteria — not a `cannot-reproduce` on work nobody has built.

## Related

- `jira-config` — run it first; it resolves the repo and commands this skill consumes.
- `jira-access-preflight` — must pass fully before this skill's first read.
- `jira-writeback` — takes this skill's verdict and evidence and posts them.
- `jira-delivery` — picks up a `needs-fix` verdict and closes it.
