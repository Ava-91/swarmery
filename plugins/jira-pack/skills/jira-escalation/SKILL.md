---
name: jira-escalation
description: "Convert a needs-fix attempt that has outgrown /jira-fix's autonomy budget (or a ticket triage already classified too-large) into a phased plan saved to the private workspace, post the plan's full text to Jira via comment-too-large.md, leave the ticket's status untouched, and remove any branch/worktree jira-delivery created before this fired. NOT for classifying the ticket (that's jira-triage) and NOT for the fix attempt itself (that's jira-delivery, which hands off here on budget exhaustion)."
version: "0.1.0"
owner: "swarmery-core"
---

# Purpose

Make "this is bigger than an autonomous `/jira-fix` run" a soft landing
instead of a dead end. When a ticket or a fix attempt outgrows
`jira.budget`, the work already done (evidence, diagnosis, partial fix
attempts) is not thrown away — it becomes a phased plan, saved where every
other plan in this project lives, with its full text posted back to the
ticket so a human can pick it up without needing local filesystem access.

# When to use

Called from two places:

1. **`jira-triage`**, before any repro attempt, when a ticket is
   self-evidently an epic or multi-stage feature (its own early-exit note).
   No branch exists yet in this path.
2. **`jira-delivery`**, mid-attempt, once one of the budget/verification
   triggers below fires. A branch and worktree may already exist in this
   path and must be cleaned up (Step 4).

# Triggers (any one of these five fires escalation)

1. `jira.budget.maxFiles` exceeded — the executor's diff touches more files
   than the budget allows, independent of whether verification passed
   (`jira-delivery`'s own second gate).
2. `jira.budget.maxAttempts` exceeded — that many delegate→verify cycles
   have run without a qualifying `PASS`.
3. `@debugger` returned an escalation under its own stop conditions (fix
   spiral, 3 failed attempts, ~5 files — `plugins/core/agents/debugger.md`)
   before `jira.budget.maxAttempts` was independently exhausted; treat this
   the same as trigger 2 rather than waiting for the outer counter to also
   run out.
4. `@verification-agent` returned `FAIL` and `jira.budget.maxAttempts` is
   already exhausted (a `FAIL` with attempts remaining is not itself a
   trigger — `jira-delivery` re-dispatches instead).
5. `jira-triage` classified the ticket `too-large` at triage time, before any
   reproduction attempt.

# Step 1 — choose the planner

- **`@task-planner`** (`plugins/core/agents/task-planner.md`) — scope under
  roughly a week, 3-5 phases. The default choice for a single defect or
  small feature that simply exceeded the *autonomous* budget, not the
  underlying complexity budget.
- **`@implementation-planner`** (`plugins/core/agents/implementation-planner.md`)
  — scope over a week, or the ticket is genuinely multi-phase (an epic,
  a cross-repo migration). Use this when triage's `too-large` reason names
  something bigger than "this needs a human to review a few extra files."

Either planner writes its own `README.md` + `phase-N-<slug>.md` tree; this
skill's job is choosing which one and handing it the ticket's context
(summary, description, the evidence bundle so far — including any partial
diagnosis from an escalated `@debugger` or `@verification-agent` run).

# Step 2 — the plan lives in the private workspace, never in a code repo

Per `/Users/andriytretiak/.claude/CLAUDE.md` §11 (Plans & Work Artifacts —
a hard rule that overrides any skill/plugin default):

```
<workspace>/<project>/workspace/working/{YYYY}/{MM}/{DD}/{slug}/plan/
```

- `{YYYY}/{MM}/{DD}` = the date this escalation fired (task start), not the
  original ticket's creation date.
- `{slug}` = the leaf folder, a lowercase kebab slug **without** a date
  prefix — the date lives only in the `YYYY/MM/DD` path segments; the
  canonical task-id `yyyy-mm-dd-slug` is derived from the two, never encoded
  into the folder name itself.
- The plan is never written under `docs/`, a code repo root, or any in-repo
  phase-history tree this repo happens to ship (that tree is a frozen
  historical record of already-shipped phases — see this repo's own
  `CLAUDE.md`). Both
  planner agents already enforce this themselves; this skill does not
  override their output location, only hands them the ticket context.

# Step 3 — the Jira comment carries the plan's full text, never a link

Render `comment-too-large.md` (`plugins/jira-pack/templates/comment-too-large.md`)
— **this skill posts it directly via `addCommentToJiraIssue`, not
`jira-writeback`**, since this verdict never carries a status transition and
`jira-writeback`'s own template table already marks `too-large` as owned
here. A workspace path is local and private to whichever machine ran
`/jira-fix`; it is useless to a teammate reading the ticket in a browser, so
the full plan text is what goes in the comment body, not a reference to it.

Format:

- **Objective** — one paragraph.
- **Phases** — one paragraph per phase: what it covers, depends-on,
  estimate.
- **Dependencies** — cross-phase dependencies and the critical path.
- **Estimate** — total.
- **Definition of done** — what "done" means for this body of work.

**Size rule — full plans over ~15,000 characters.** Do not paste a wall of
undifferentiated text. Instead post: the complete phase structure (every
phase's one-paragraph summary, in full), plus **one representative phase
reproduced in full** as a sample of the actual detail level, plus an
explicit line naming the workspace task the detailed phase docs live in
(so a human who does have filesystem/repo access can find the rest). This
is not a truncation — every phase still gets its summary paragraph; only the
full per-phase detail is capped to the one sample.

# Step 4 — status untouched, no branch left behind

- **The ticket's status is never changed.** No transition is attempted,
  successfully or otherwise — this verdict is silent on status by design, the
  same way `needs-info` is.
- **No branch survives this path.** Two cases:
  - Triggered from `jira-triage` (trigger 5) — no branch was ever created;
    nothing to clean up.
  - Triggered from `jira-delivery` (triggers 1-4) — a branch and worktree
    from `jira-delivery`'s Step 1 already exist. Remove both together, in
    this order:
    ```bash
    git worktree remove <worktree-root>/fix-<key>-<slug> --force   # --force: it may still carry the aborted attempt's uncommitted edits
    git branch -D fix/<KEY>-<slug>                                  # if the worktree removal didn't already take the local ref with it
    git push origin --delete fix/<KEY>-<slug>                       # only if jira-delivery's Step 1 empty-push already ran
    ```
    A half-done fix must not settle into git — leaving the branch around
    (even unmerged) is a worse failure mode than deleting a few empty or
    partial commits, since nothing downstream should ever build on an
    attempt this skill just declared abandoned.
- **The board card moves to `in_review`**, with its `prompt` gaining the
  stop reason (which trigger fired, and the specific numbers — e.g.
  "maxFiles: 8 > budget 5" or "maxAttempts: 3/3 exhausted, last verdict
  FAIL").

# Dry-run

Not explicitly specified upstream by the triggering skill's own dry-run
section (only `jira-delivery` documents one) — extended here for
consistency with the rest of the pack's `--dry-run` contract, since
`@jira-task-runner`'s own dry-run guarantee ("every write call... is kept
out of the run") has to hold for this skill too:

- The planner invocation itself is **not** run in `--dry-run` — the same
  budget rationale as `jira-delivery` skipping delegation: `@task-planner`/
  `@implementation-planner` are real multi-turn agent calls, and the mode
  exists to check the contour, not spend that budget. Print instead:
  ```
  DRY-RUN plan <task-planner|implementation-planner> for <KEY>
  ```
- The Jira comment and the board-card move are suppressed exactly like
  `jira-writeback`'s own dry-run pattern:
  ```
  DRY-RUN jira comment <KEY>
  <full rendered comment-too-large.md text, or the size-capped form>
  DRY-RUN board PATCH /api/board/tasks/{id} { "boardColumn": "in_review" }
  ```
- Any branch/worktree cleanup in Step 4 is a `git worktree remove`/`git
  branch -D`/`git push --delete` — real destructive git operations. In
  `--dry-run` these print as `DRY-RUN git worktree remove <path>` /
  `DRY-RUN git branch -D <name>` / `DRY-RUN git push origin --delete <name>`
  rather than executing, matching `jira-delivery`'s own dry-run treatment of
  git operations.

# Self-check before returning

- [ ] Exactly one of the five triggers is cited as the reason for escalating
- [ ] The planner choice (`@task-planner` vs `@implementation-planner`) is
      justified against scope, not defaulted blindly
- [ ] The plan was written under
      `<workspace>/<project>/workspace/working/{YYYY}/{MM}/{DD}/{slug}/plan/`
      — never under `docs/`, a repo root, or any in-repo phase-history tree
- [ ] The Jira comment contains the plan's **full text** — no bare workspace
      path or link substituted for it
- [ ] Plans over ~15,000 characters used the capped form (full phase
      structure + one full sample phase + workspace task name), not a raw
      dump or a silent truncation
- [ ] The ticket's status was not changed
- [ ] If a branch/worktree existed (the `jira-delivery` path), both were
      removed together
- [ ] The board card landed at `in_review` with the specific stop reason in
      its `prompt`

# Common mistakes to avoid

- Posting a workspace file path instead of the plan's actual text — the
  whole point of this step is that the ticket reader has no access to that
  path.
- Silently truncating a long plan without the "detailed phase docs live in
  workspace task `<slug>`" line — a human reading a truncated plan with no
  pointer has no way to find the rest.
- Leaving a branch or its worktree behind after a `jira-delivery`-triggered
  escalation "in case it's useful later" — it isn't; a half-verified attempt
  must not be mistaken for reviewable work.
- Attempting any transition on this verdict "just to move it out of the
  current column" — status is explicitly untouched; only the board card
  (a local mirror, not the ticket itself) moves.
- Writing the plan into this repository (a `docs/` folder, a repo root, or
  any other in-tree location) — an in-repo phase-history tree, if this repo
  ships one, is a frozen record of already-shipped phases, never a
  destination for a new plan.

# Related

- `plugins/jira-pack/skills/jira-triage/SKILL.md` — source of the
  `too-large` verdict (trigger 5) and the evidence bundle handed to the
  chosen planner.
- `plugins/jira-pack/skills/jira-delivery/SKILL.md` — source of triggers
  1-4, and the branch/worktree this skill cleans up when it hands off
  mid-attempt.
- `plugins/core/agents/task-planner.md` / `plugins/core/agents/implementation-planner.md`
  — the two planners Step 1 chooses between.
- `plugins/jira-pack/templates/comment-too-large.md` — the template this
  skill renders and posts directly (not via `jira-writeback`).
- `plugins/jira-pack/skills/swarmery-board-card/SKILL.md` — moves the card
  to `in_review` with the stop reason; this skill never calls the board API
  itself beyond handing that skill the reason string.
- `/Users/andriytretiak/.claude/CLAUDE.md` §11 — the hard rule governing
  where every plan in this project is stored.
