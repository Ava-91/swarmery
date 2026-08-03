---
name: jira-delivery
description: "Close the needs-fix branch: create an isolated git worktree, delegate the actual code change to a core executor, gate every publish action (push / PR / fix comment / QA transition) behind a green @verification-agent verdict, commit + push + PR, then write back to Jira via jira-writeback. NOT for classifying the ticket (that's jira-triage) and NOT for turning an over-budget attempt into a plan (that's jira-escalation, which this skill hands off to when a trigger fires)."
version: "0.1.0"
owner: "swarmery-core"
---

# Purpose

Turn a `needs-fix` verdict into a real, verified, published fix without the
orchestrator (`@jira-task-runner`) ever editing code itself. This skill owns
exactly five things: the isolated branch, delegation to the right executor,
the verification gate, the commit + PR, and the writeback. Anything that
outgrows `jira.budget` mid-flight is not this skill's problem to solve —
it hands off to `jira-escalation` (see [Related](#related)) and stops.

# When to use

Called by `@jira-task-runner`'s Fork (step 5), `needs-fix` row, strictly
after `jira-triage` has produced the evidence bundle (reproduction command,
exit code, output fragment, extracted repro steps) for a ticket whose
reproduction ran **red**. Never invoked directly on a `too-large` verdict —
that never reaches this skill at all.

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
git worktree add <worktree-root>/fix-<key>-<slug> \
  -b fix/<KEY>-<slug> origin/main                               # branch + isolated worktree in one step, from the fresh remote tip
git -C <worktree-root>/fix-<key>-<slug> push -u origin fix/<KEY>-<slug>   # empty push so it can be protected before real commits land
```

- `<KEY>` — the ticket key.
- `<slug>` — kebab-case of the ticket's `summary`, truncated to ~40 characters.
- `<worktree-root>` — a path outside the shared checkout (e.g. a sibling
  directory), never a subdirectory `git worktree` would refuse to nest inside
  the checkout it's isolating from.

Everything from here on — the executor's edits, the commit, the push, the PR
branch — happens inside `<worktree-root>/fix-<key>-<slug>`, never in
`<working-root>`.

# Step 2 — delegate; the orchestrator never edits code itself

| Type of work | Executor | Why |
|---|---|---|
| A reproduced defect (the `needs-fix` evidence bundle *is* a repro) | `@debugger` | Has its own stop conditions (3 failed attempts, fix spiral, ~5 files — `plugins/core/agents/debugger.md`) that are deliberately sized to match `jira.budget`, and requires a regression test as part of its own completion criteria. |
| A behavior change or small feature (ticket describes desired new behavior, not a crash) | `@implementation-agent` | Leaf-mode executor for scoped, step-by-step code changes. |
| A regression test is still missing after the fix (`@debugger` didn't add one — e.g. a documented `TODO: P0-REGRESSION` case) | `@test-writer` | Adds the test the fix still needs before verification is treated as complete. |

The prompt handed to whichever executor is chosen carries, at minimum:

- the ticket key and title;
- the reproduction steps extracted by `jira-triage` (Step 1's read of the
  ticket's "Steps to reproduce" or equivalent);
- the reproduction run's evidence — the exact command and a trimmed output
  fragment, from `jira-triage`'s evidence bundle (never re-derived or
  re-worded);
- the working root — the **worktree path** from Step 1, not the shared
  checkout;
- the branch name (`fix/<KEY>-<slug>`);
- the budget — `jira.budget.maxFiles` and `jira.budget.maxAttempts` — so the
  executor's own internal stop conditions and this skill's external gate
  never disagree about how much room there is.

**[VERIFY] naming note**: neither `@debugger` nor `@implementation-agent` is
documented with an input shape for "a ticket-driven task description with no
plan phase doc and no step file" — `@implementation-agent`'s own mode
selection table only recognizes `step_file` (Leaf mode) or `task_dir`
(Plan-execution mode) as inputs, and refuses `task_dir` unless the caller is
the user directly. A `/jira-fix` run is neither; it hands the executor a
plain task description (ticket context, above) with no phase doc backing it.
This works in practice — the executor still behaves leaf-like (single scoped
change, no subagent spawning, verified via codebase-retrieval) — but it is a
gap in `@implementation-agent`'s own mode-selection contract, not something
this skill can paper over. Flagged for `@implementation-agent`'s own doc to
tighten, not silently resolved here.

# Step 3 — `@verification-agent` is the sole publish gate

**No code path in this skill reaches `push`, PR, the fix comment, or the QA
transition without a `PASS` verdict from `@verification-agent` first.** Stated
once per action, because a reader skimming straight to any one of the four
must hit the gate there, not just at the top of this section:

- **`git push`** does not happen without `PASS`.
- **`gh pr create`** does not happen without `PASS`.
- **The `comment-fix-summary.md` writeback** is not composed or posted without
  `PASS`.
- **The `jira.qaStatus` transition** is not attempted without `PASS`.

Run `@verification-agent` (scope: the worktree's diff against `origin/main`)
after the executor returns, before touching git or Jira at all.

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
fix(<scope>): <description>  [<KEY>]
```

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
exactly as it already does for the other four verdicts: post the
`comment-fix-summary.md`-rendered comment (root cause, what changed, the PR
link, how to verify for QA, risks), then attempt the transition to
`jira.qaStatus`. `swarmery-board-card` moves the card to `in_review`, with its
`prompt` gaining the PR link.

# Dry-run

**No `git branch`, `git commit`, `git push`, or `gh pr create` call fires at
all.** In place of the four real actions, print exactly:

```
DRY-RUN git branch fix/<KEY>-<slug> from <base>
DRY-RUN git commit "fix(<scope>): <description>  [<KEY>]"  (files: N)
DRY-RUN git push origin fix/<KEY>-<slug>
DRY-RUN gh pr create --title "<title>" --body "<body>"
```

**Delegation to an executor does not run at all in dry-run** — Step 2 is
skipped entirely, not run-and-discarded. The mode exists to check the contour
(branch naming, commit format, PR shape, the writeback path) without spending
real fix-attempt budget on a `@debugger`/`@implementation-agent`/`@test-writer`
invocation. Because Step 2 never ran, Step 3's verification gate and Step 4's
commit/PR are necessarily also skipped — the four `DRY-RUN` lines above are
printed from the *planned* branch name, scope, and file count estimate, not
from a real diff.

# Self-check before returning

- [ ] The branch was created via `git worktree add`, never `git checkout -b`
      in the shared working root
- [ ] The executor's prompt carried key, title, repro steps, repro
      command+output, worktree path, branch, and both budget numbers
- [ ] `push`, PR, the fix comment, and the transition each individually did
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
  `jira-writeback` renders in Step 5.
