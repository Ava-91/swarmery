# Comment template — `too-large`

**Purpose**: Jira comment body for the `too-large` verdict — the ticket's
scope obviously exceeds `jira.budget` before any fix work starts (an epic, a
multi-stage feature). Unlike the other four templates, this one is **not**
rendered or posted by `jira-writeback` — Phase 7's `jira-escalation` skill
owns it, since this verdict never carries a status transition and the plan it
references is saved to the private workspace, never to this repo (hard-rule
§11). This template ships in Phase 6 so Phase 7 has it ready to render into.

**Required blocks** (every one must be filled in with real content):
1. diagnosis (why this doesn't fit as a direct `/jira-fix` run)
2. why it doesn't fit the budget, specifically
3. the **full text of the plan**, phase by phase — never a link; the
   workspace path is local and private, and useless to a team reading the
   ticket
4. what happens next

**Language**: the same language the ticket (`summary`/`description`) is
written in; English if that is ambiguous.

**Size note**: if the full plan text exceeds roughly 15,000 characters, this
template still gets the complete phase structure (objective, one paragraph
per phase, dependencies, estimate, definition of done) plus one representative
phase shown in full, plus an explicit line naming the workspace task the
detailed phase docs live in — not a wall of undifferentiated text.

---

## Template

```markdown
This ticket is out of scope for an autonomous `/jira-fix` run — it needs a
plan, not a single delegated fix.

**Diagnosis**: `<why this ticket is epic-shaped or multi-stage, in one or two
sentences>`

**Why it doesn't fit the budget**: `<specific comparison against
jira.budget.maxFiles / jira.budget.maxAttempts, or "classified as an epic
before any repro attempt — a single reproduction command has no meaningful
target here">`

**Plan**

*Objective*: `<one paragraph>`

*Phases*:
1. `<phase name>` — `<one paragraph: what it covers, depends on, estimate>`
2. `<phase name>` — `<one paragraph>`
3. `<...as many phases as the plan actually has>`

*Dependencies*: `<cross-phase dependencies, critical path>`

*Estimate*: `<total>`

*Definition of done*: `<what "done" means for this body of work>`

**What happens next**: this ticket's status has not been changed. The full
phase-by-phase plan lives in the workspace task `<workspace task slug>`;
picking this up is a planning/scheduling decision, not something this run
makes on its own.

<!-- swarmery:jira-task-runner run=<external_id or run tag> verdict=too-large -->
```
