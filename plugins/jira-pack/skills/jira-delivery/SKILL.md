---
name: jira-delivery
description: "Close the needs-fix branch for both ticket classes: isolated git worktree, a red test BEFORE any implementation on change tickets, delegation to a core executor, every publish action (push / PR / comment / QA transition) gated behind a green @verification-agent verdict, commit + push + PR, then writeback via jira-writeback. NOT for classifying the ticket (that's jira-triage) and NOT for turning an over-budget attempt into a plan (that's jira-escalation, which this skill hands off to when a trigger fires)."
version: "0.2.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: 96adf6ea9003
  updated: 2026-08-06
---

# Purpose

Turn a `needs-fix` verdict into real, verified, published work without the
orchestrator (`@jira-task-runner`) ever editing code itself. This skill owns
exactly six things: the isolated branch, the red-test-first step on change
tickets, delegation to the right executor, the verification gate, the commit +
PR, and the writeback. Anything that outgrows `jira.budget` mid-flight is not
this skill's problem to solve — it hands off to `jira-escalation` (see
[Related](#related)) and stops.

# When to use

Called by `@jira-task-runner`'s Fork (step 5), `needs-fix` row, strictly
after `jira-triage` has produced the evidence bundle for **either** class:

- **`class: defect`** — reproduction command, exit code, output fragment,
  extracted repro steps, for a ticket whose reproduction ran **red**.
- **`class: change`** — the green baseline run, the absence proof (what was
  searched and what was found), and the numbered **testable acceptance
  criteria**. There is no red command yet: producing one is this skill's Step
  2a, and it is a precondition for any implementation work.

Never invoked directly on a `too-large` verdict — that never reaches this
skill at all.

# The class decides the shape of the run

| | `class: defect` | `class: change` |
|---|---|---|
| Red evidence | Already in hand (`jira-triage`'s failing reproduction) | **Produced here, in Step 2a**, as a failing acceptance test |
| Branch | `fix/<KEY>-<slug>` | `feat/<KEY>-<slug>` |
| Worktree dir | `<worktree-root>/fix-<key>-<slug>` | `<worktree-root>/feat-<key>-<slug>` |
| Commit type | `fix(<scope>): … [<KEY>]` | `feat(<scope>): … [<KEY>]` |
| Writeback template | `comment-fix-summary.md` | `comment-change-summary.md` |

Everywhere below, `<branch>` and `<worktree>` mean whichever of the two rows
above the run's class selected. Nothing else in the flow differs: the same
verification gate, the same budget ceilings, the same escalation triggers, the
same comment-then-transition ordering.

# Step 1 — branch, in an isolated git worktree

**Isolation is a requirement, not advice.** The working root `jira-config`
resolved (default `${CLAUDE_PROJECT_DIR}` or `--repo`) is a **shared
checkout** — other sessions (interactive users, other `/jira-fix` runs, other
agents) may be reading or writing it concurrently. Running `git checkout -b`
directly in that checkout would move its shared `HEAD`/index out from under
every one of them mid-run. A dedicated worktree gives this run its own
working directory and its own index, entirely disjoint from the shared one,
so nothing it does can be observed as a state change by a parallel session.

Adapting `plugins/core/commands/new-feature-branch.md`'s four-step recipe
(checkout main → pull → checkout -b → push empty) to a worktree — step 1 has
no shared `HEAD` to check out, so it is replaced with a fetch:

```bash
git -C <working-root> fetch origin main                        # read-only, safe against the shared checkout
git worktree add <worktree-root>/<prefix>-<key>-<slug> \
  -b <prefix>/<KEY>-<slug> origin/main                          # branch + isolated worktree in one step, from the fresh remote tip
git -C <worktree-root>/<prefix>-<key>-<slug> push -u origin <prefix>/<KEY>-<slug>   # empty push so it can be protected before real commits land
```

- `<prefix>` — `fix` for `class: defect`, `feat` for `class: change` (the
  table above). A change ticket landing on a `fix/…` branch mislabels the work
  for every reviewer and every changelog downstream.
- `<KEY>` — the ticket key.
- `<slug>` — kebab-case of the ticket's `summary`, truncated to ~40 characters.
- `<worktree-root>` — a path outside the shared checkout (e.g. a sibling
  directory), never a subdirectory `git worktree` would refuse to nest inside
  the checkout it's isolating from.

Everything from here on — the executor's edits, the commit, the push, the PR
branch — happens inside `<worktree>`, never in `<working-root>`.

# Step 2a — `class: change` only: the failing test comes FIRST

**A change ticket enters this skill without red evidence, and it does not get
to leave without having had some.** Before any implementation work is
dispatched, `@test-writer` writes a test from `jira-triage`'s numbered
acceptance criteria, and that test is **run and observed failing**.

1. Dispatch `@test-writer` with: the ticket key and title, the numbered
   testable acceptance criteria (verbatim from `jira-triage` — not
   paraphrased), the absence proof naming the files the behavior belongs in,
   `<worktree>` as the working root, and the instruction that the test must
   assert the ticket's target behavior and **must fail against the current
   code**.
2. Run it. Capture the command, exit code, and the failing assertion — this is
   the change class's red evidence, and it plays exactly the role the
   reproduction plays for a defect.
3. **If the new test passes on the first run**, stop and do not implement
   anything. A green test against untouched code means one of two things, and
   both are findings rather than work: the behavior already exists (→ hand back
   `already-fixed` with this test as the evidence; `jira-triage` misread the
   absence proof) or the test does not actually assert the criterion (→ one
   re-dispatch of `@test-writer` to tighten it, counted against
   `jira.budget.maxAttempts`; still green after that → `needs-info`, with the
   criterion that could not be expressed as the question).

Only with a captured red run does the flow continue to Step 2b. Skipping this
step and implementing straight from the criteria is what produces a PR whose
"verification" only ever proves that the suite still runs.

For `class: defect` this step does not apply — `jira-triage`'s failing
reproduction already **is** the red evidence, and re-deriving it here would
spend budget to learn what the evidence bundle already states.

# Step 2b — delegate; the orchestrator never edits code itself

| Type of work | Executor | Why |
|---|---|---|
| A reproduced defect (the `needs-fix` evidence bundle *is* a repro) | `@debugger` | Has its own stop conditions (3 failed attempts, fix spiral, ~5 files — `plugins/core/agents/debugger.md`) that are deliberately sized to match `jira.budget`, and requires a regression test as part of its own completion criteria. |
| A `class: change` ticket, after Step 2a's red test exists | `@implementation-agent` | Leaf-mode executor for scoped, step-by-step code changes; the failing test is the acceptance criterion it drives to green. |
| A defect fix that landed without a regression test (`@debugger` didn't add one — e.g. a documented `TODO: P0-REGRESSION` case) | `@test-writer` | Adds the test the fix still needs before verification is treated as complete. |
| A `class: change` ticket whose red test turns out to need a debugger's diagnosis (the implementation attempt fails in existing code the ticket did not name) | `@debugger` | Once the failure is in code that already exists, it is a defect-shaped problem regardless of the ticket's class — use the executor built for diagnosis rather than re-briefing the implementer. |

The prompt handed to whichever executor is chosen carries, at minimum:

- the ticket key and title;
- **for `class: defect`** — the reproduction steps extracted by `jira-triage`
  (Step 1's read of the ticket's "Steps to reproduce" or equivalent) and the
  reproduction run's evidence: the exact command and a trimmed output fragment,
  from `jira-triage`'s evidence bundle (never re-derived or re-worded);
- **for `class: change`** — the numbered acceptance criteria, the absence
  proof, the path of Step 2a's new test, and that test's failing run (command,
  exit code, assertion). The executor's completion condition is *this test goes
  green without the rest of the suite going red* — stated in the prompt, not
  left implicit;
- the working root — the **worktree path** from Step 1, not the shared
  checkout;
- the branch name (`<prefix>/<KEY>-<slug>`);
- the budget — `jira.budget.maxFiles` and `jira.budget.maxAttempts` — so the
  executor's own internal stop conditions and this skill's external gate
  never disagree about how much room there is.

**`@debugger` and `@test-writer` take this directly as a plain-brief
prompt.** `@debugger`'s own Inputs are a bug description plus an *optional*
`Reference:` step file path (`plugins/core/agents/debugger.md`);
`@test-writer`'s are `target`/`type`/`coverage_gaps` (optional)
(`plugins/core/agents/test-writer.md`). Neither documents a
`step_file`-vs-`task_dir` mode-selection contract, so the list above, handed
straight over as a prompt, is exactly the input shape both already expect —
nothing to reshape for either.

**`@implementation-agent` needs a materialized step file instead.** Its own
mode table (`plugins/core/agents/implementation-agent.md`) recognizes
exactly two input shapes — `step_file` (Leaf mode) or `task_dir`
(Plan-execution mode, a direct-user-invocation entry point whose own
anti-nesting guard refuses a `task_dir` arriving from an orchestrator). A
plain ticket brief is neither shape, and the agent's behavior when given
neither is undocumented — it may guess Leaf mode, or it may stall. On an
`autonomy: auto` run with nobody present to unblock a stall, that
undocumented fallback is not something this skill can rely on. So before
dispatching `@implementation-agent`, write the list above into a minimal
step-file-shaped doc and dispatch against that file instead of the bare
brief:

- **Where**: `<workspace>/<project>/workspace/working/{YYYY}/{MM}/{DD}/<prefix>-<KEY>-<slug>/step-file.md`
  — the run's workspace task dir, never the shared checkout or the worktree
  (the same workspace-only placement `jira-escalation` uses for its own plan
  tree, minus the `plan/` subfolder since this is a single step doc, not a
  phased plan). `{YYYY}/{MM}/{DD}` is this run's date; `<prefix>-<KEY>-<slug>`
  reuses Step 1's branch/worktree slug so the step file, branch, and
  worktree all trace back to the same run.
- **What it contains**: objective, the ticket key and title, the working root
  (Step 1's worktree path) and branch, the budget
  (`jira.budget.maxFiles`/`maxAttempts`), and measurable acceptance criteria.
  The rest is class-dependent:
  - `class: defect` — the extracted reproduction steps and the repro run's
    command and output; acceptance criteria are *the regression is resolved,
    the repro command now exits green, and the existing test suite still
    passes*.
  - `class: change` — the numbered acceptance criteria from `jira-triage`, the
    absence proof, and Step 2a's test path plus its failing run; acceptance
    criteria are *Step 2a's test goes green, and the existing test suite still
    passes*. The step file **never** invites the executor to write the test
    itself — it already exists and is red; re-writing it is how an executor
    accidentally weakens the assertion until it passes.
- **Dispatch passes `step_file: <that path>`, never `task_dir`** —
  `jira-delivery` is itself an orchestrator dispatching
  `@implementation-agent`, and the agent's anti-nesting guard refuses a
  `task_dir` arriving from an orchestrator on sight.
- **In dry-run, the step file is not written and this dispatch does not run
  at all** — Steps 2a and 2b are skipped entirely in dry-run (see Dry-run
  below), so nothing ever reaches the materialization step.

# Step 3 — `@verification-agent` is the sole publish gate

**No code path in this skill reaches `push`, PR, the fix comment, or the QA
transition without a `PASS` verdict from `@verification-agent` first.** Stated
once per action, because a reader skimming straight to any one of the four
must hit the gate there, not just at the top of this section:

- **`git push`** does not happen without `PASS`.
- **`gh pr create`** does not happen without `PASS`.
- **The writeback comment** (`comment-fix-summary.md` / `comment-change-summary.md`)
  is not composed or posted without `PASS`.
- **The `jira.qaStatus` transition** is not attempted without `PASS`.

Run `@verification-agent` (scope: the worktree's diff against `origin/main`)
after the executor returns, before touching git or Jira at all.

**On `class: change`, `PASS` is necessary but does not by itself prove the
ticket was delivered.** `@verification-agent` grades build/typecheck/lint/test
on the diff; a change whose implementation quietly never satisfied the
criterion can still come back green if Step 2a's test was weakened or
deleted along the way. So on the change path, additionally confirm — from the
verification run's own output, not from the executor's summary — that **Step
2a's specific test now passes and still exists in the diff**. A `PASS` whose
test list no longer contains that test is a `FAIL` for this skill's purposes,
and it routes exactly like one.

**A second, independent gate — `jira.budget.maxFiles`.** `PASS` from
verification is necessary but not sufficient. Compute the diff's file count
(`git -C <worktree> diff --stat origin/main...HEAD`) and compare it against
`jira.budget.maxFiles`. A diff that passes verification but touches more
files than the budget allows **still does not publish** — it routes to
`jira-escalation` exactly like a verification `FAIL` would (see below).
Without this check, `jira-escalation`'s own `maxFiles`-exceeded trigger would
never actually fire from this skill's side, since a `PASS` alone would
otherwise wave a too-large diff straight through.

**On `FAIL` or `PARTIAL`:** this counts as one attempt against
`jira.budget.maxAttempts`. If attempts remain, re-dispatch the same executor
with the verification failure folded into the prompt as feedback, and re-run
Step 3 from the top. Once `jira.budget.maxAttempts` is exhausted without a
qualifying `PASS` (or the executor itself reports a `@debugger`-style
escalation — fix spiral, its own 3-attempt/5-file ceiling), stop delegating
and hand off to `jira-escalation` (see [Related](#related)) with: the ticket
key, the evidence bundle, the diagnosis (which trigger fired and the
specific numbers), and the worktree/branch path so escalation can clean it
up. **The ticket stays exactly where it is** — no comment, no transition, no
PR — until escalation's own writeback lands.

# Step 4 — commit and PR

Only after a qualifying `PASS` (verification green **and** within
`jira.budget.maxFiles`):

```
fix(<scope>): <description>  [<KEY>]     # class: defect
feat(<scope>): <description>  [<KEY>]    # class: change
```

The conventional-commit type follows the ticket's class, not the shape of the
diff: a change ticket that happened to be implemented by deleting code is still
`feat`, and a defect fixed by adding a component is still `fix`.

`<scope>` is taken from `commitScopes` in `.claude/project.json` when that
array is present and a matching scope exists; otherwise omit the parenthesized
scope rather than inventing one.

Push the branch (`git -C <worktree> push`) — the empty push already happened
in Step 1, so this is a fast-forward of real commits onto the same ref.

Open the PR through **`@pr-generator`** for the title/description/review
checklist text, then **`gh pr create`** for the actual open —
`plugins/core/agents/pr-generator.md` runs with `permissionMode: plan` and
`disallowedTools: [Edit, Write, NotebookEdit]`; it generates PR text and
literally cannot open a PR itself. The orchestrator (this skill, via `gh`) is
the one that performs the write. The PR body includes a link to the ticket.

# Step 5 — back to Jira

Through `jira-writeback` (`plugins/jira-pack/skills/jira-writeback/SKILL.md`),
exactly as it already does for the other four verdicts — passing the class
along so it renders the matching template:

- `class: defect` → `comment-fix-summary.md` (root cause, what changed, the PR
  link, how to verify for QA, risks);
- `class: change` → `comment-change-summary.md` (what was implemented, the
  acceptance criteria and the test that now asserts each, the PR link, how to
  verify for QA, what was deliberately left out).

Then attempt the transition to `jira.qaStatus`. `swarmery-board-card` moves the
card to `in_review`, with its `prompt` gaining the PR link.

# Dry-run

**No `git branch`, `git commit`, `git push`, or `gh pr create` call fires at
all.** In place of the four real actions, print exactly:

```
DRY-RUN git branch <prefix>/<KEY>-<slug> from <base>
DRY-RUN git commit "<fix|feat>(<scope>): <description>  [<KEY>]"  (files: N)
DRY-RUN git push origin <prefix>/<KEY>-<slug>
DRY-RUN gh pr create --title "<title>" --body "<body>"
```

**Delegation to an executor does not run at all in dry-run** — Steps 2a and 2b
are skipped entirely, not run-and-discarded. On the change path that means no
test is written and no red run is produced either; print instead:

```
DRY-RUN @test-writer red test for <KEY> from <N> acceptance criteria
```

That also covers the `@implementation-agent` step-file materialization: no
step-file doc is written to the workspace task dir, and no dispatch fires. The
mode exists to check the contour (class, branch naming, commit format, PR
shape, the writeback path) without spending real budget on a
`@debugger`/`@implementation-agent`/`@test-writer` invocation. Because
Steps 2a/2b never ran, Step 3's verification gate and Step 4's commit/PR are
necessarily also skipped — the `DRY-RUN` lines above are printed from
the *planned* branch name, scope, and file count estimate, not from a real
diff.

# Self-check before returning

- [ ] The branch prefix matched the class (`fix/` for defect, `feat/` for
      change), and so did the commit type and the writeback template
- [ ] The branch was created via `git worktree add`, never `git checkout -b`
      in the shared working root
- [ ] **`class: change` only:** a test was written and **observed failing**
      before any implementation was dispatched; a first-run pass was treated
      as `already-fixed`/`needs-info`, never implemented over
- [ ] **`class: change` only:** the green verification run still contains that
      same test — a `PASS` whose test list lost it was routed as a `FAIL`
- [ ] The executor's prompt carried key, title, worktree path, branch, both
      budget numbers, and the class's own evidence (repro steps + command
      output for defect; acceptance criteria + absence proof + the red test's
      path and failing run for change) — directly for
      `@debugger`/`@test-writer`, or via the materialized step file for
      `@implementation-agent`
- [ ] `push`, PR, the comment, and the transition each individually did
      not fire without a `PASS` verdict
- [ ] The diff's file count was checked against `jira.budget.maxFiles`
      independently of the verification verdict
- [ ] A `FAIL`/`PARTIAL` verdict was counted against `jira.budget.maxAttempts`
      before any re-dispatch
- [ ] Budget exhausted (attempts, files, or a `@debugger`-style internal
      escalation) handed off to `jira-escalation` with the worktree/branch path
- [ ] `gh pr create` was never attempted directly by `@pr-generator` (it only
      generated text)
- [ ] Dry-run fired zero git/gh calls and skipped delegation entirely

# Common mistakes to avoid

- **Implementing a `change` ticket before a red test exists** — then the only
  thing the verification gate can prove is that the suite still runs, which is
  what it proved before the run started too.
- **Letting the executor rewrite or delete Step 2a's test to get to green.**
  The test is the acceptance criterion in executable form; a green obtained by
  weakening it is the one failure mode this whole flow is built to catch.
- Running `git checkout -b` in the shared working root "just this once" —
  even a single write to the shared checkout's index can race a parallel
  session.
- Treating a `PASS` verdict alone as sufficient to publish — the file-count
  budget is a second, independent gate.
- Asking `@pr-generator` to open the PR — it cannot; it has no `Edit`/`Write`
  and `permissionMode: plan`. The orchestrator calls `gh` itself.
- Re-dispatching the executor past `jira.budget.maxAttempts` "because the
  last failure looked close to passing" — the budget is a hard ceiling, not
  a judgment call.
- Running delegation in `--dry-run` "to preview what the executor would do" —
  the mode exists specifically to avoid spending that budget.

# Related

- `plugins/core/commands/new-feature-branch.md` — the branch-from-fresh-main
  recipe this skill adapts for an isolated worktree.
- `plugins/core/agents/debugger.md` — source of the 3-attempt/~5-file stop
  conditions `jira.budget` is sized to match, and the source of the
  fix-spiral escalation this skill treats as budget-exhausted.
- `plugins/core/agents/verification-agent.md` — the `PASS`/`FAIL`/`PARTIAL`
  verdict format this skill gates every publish action on.
- `plugins/core/agents/pr-generator.md` — generates PR text only
  (`permissionMode: plan`, no `Edit`/`Write`); this skill's `gh pr create`
  call is the actual open.
- `plugins/jira-pack/skills/jira-triage/SKILL.md` — source of the `needs-fix`
  verdict and the evidence bundle this skill's executor prompt is built from.
- `plugins/jira-pack/skills/jira-writeback/SKILL.md` — owns the
  `comment-fix-summary.md` post and the QA transition once `PASS` is in hand.
- `plugins/jira-pack/skills/jira-escalation/SKILL.md` — where a budget-
  exhausted or over-scope attempt goes instead of publishing.
- `plugins/jira-pack/templates/comment-fix-summary.md` — the template
  `jira-writeback` renders in Step 5 for `class: defect`.
- `plugins/jira-pack/templates/comment-change-summary.md` — the same, for
  `class: change`.
- `plugins/core/agents/test-writer.md` — the executor Step 2a dispatches to
  produce the red test a change ticket arrives without.

# How to use

## What it does

This skill takes a ticket that triage marked `needs-fix` and turns it into published, verified work. It creates an isolated git worktree so nothing races the shared checkout, makes sure red evidence exists before any code is written, hands the actual editing to a core executor agent, and refuses to push, open a PR, comment, or move the ticket until a verification agent returns `PASS` and the diff stays inside the file budget.

## When to use it

- A ticket has been triaged as `class: defect` with a reproduction that ran red, and you now want the fix built and shipped.
- A ticket has been triaged as `class: change` with numbered acceptance criteria and an absence proof, and you want a failing test written first, then the implementation.
- You want the whole publish path — push, PR, ticket comment, QA transition — held behind a single verification gate.
- You want to preview the branch name, commit format, and PR shape without spending executor budget (dry-run).

## When not to use it

- The ticket has not been classified yet — run `jira-triage` first; it produces the evidence bundle this skill builds its executor prompt from.
- The attempt has blown its attempt or file budget — hand off to `jira-escalation`, which turns it into a phased plan instead.
- You only need the verdict comment posted or the QA transition attempted — that is `jira-writeback`.
- The verdict is `too-large`; that never reaches this skill.

## How to invoke

```
Skill(skill: "jira-pack:jira-delivery")
```

Normally invoked by the ticket-runner agent at the `needs-fix` fork, not typed by hand.

## Inputs

- Ticket key, title, and class (`defect` or `change`) — required.
- The triage evidence bundle — required. For a defect: repro steps, command, exit code, output fragment. For a change: green baseline, absence proof, numbered acceptance criteria.
- Budget values (`maxFiles`, `maxAttempts`) and the QA status name, read from the project's config block — required.
- Working root and a worktree root outside the shared checkout — optional; defaults come from config.
- Dry-run flag — optional.

## What you get back

A branch `fix/<KEY>-<slug>` or `feat/<KEY>-<slug>` living in its own worktree, real commits with the class-matching conventional type and the ticket key in the subject, an open PR linking the ticket, a summary comment on the ticket, a QA-status transition attempt on the ticket itself, and the board card moved to `in_review`. On budget exhaustion you instead get a handoff to escalation with the branch path and the trigger that fired — and the ticket left untouched.

## Worked example

```
Skill(skill: "jira-pack:jira-delivery")
# class: change, criteria: "line items must reject a zero quantity"
```

A worktree is created on `feat/<KEY>-reject-zero-quantity`. A test writer produces a
test against the criterion; it is run and observed failing. Only then does the
implementation executor run against that red test. Verification comes back `PASS`,
the diff touches 3 files against a budget of 8, and the same test is still in the
green run — so the commit `feat(orders/line-items): reject zero quantity  [<KEY>]`
is pushed, a PR is opened, and the change-summary comment lands on the ticket.

## Related

- `jira-triage` — run it first; it decides the class and produces the evidence.
- `jira-escalation` — where an over-budget or over-scope attempt goes instead.
- `jira-writeback` — owns the comment template and the QA transition this skill calls into.
