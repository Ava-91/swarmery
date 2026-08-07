---
description: Execute an existing plan (implementation-planner / task-planner output) — triage the phase DAG, dispatch isolated executor subagents, review each phase, keep durable progress
color: green
docs:
  status: reviewed
  source_sha: 80900278c81b
  updated: 2026-08-06
---

# Run Plan

Execute a finished plan directory end-to-end: parse its phase DAG, route sequential
phases through a per-phase implementer + review loop and parallelizable groups
through concurrent isolated dispatches, preserve ASK gates (commits/pushes/deploys
stay with the user), and track durable progress in the task ledger.

Follow the playbook in `skills/run-plan/SKILL.md` (auto-loaded skill `run-plan`).
Plan directory = $ARGUMENTS if provided, otherwise the newest
`working/**/{slug}/plan/` in the project workspace.

This command runs in the **main session** (the controller). It cannot be delegated
to an agent — subagents cannot spawn the executor subagents the playbook dispatches.

Related: `@implementation-planner` / `@task-planner` produce what this runs;
`@tech-lead` is the alternative when you want the full 9-phase workflow including
re-planning, pre-mortem, and the complete quality-gate panel rather than executing
an already-approved plan as written; `@implementation-agent` in Plan-execution mode
(`task_dir` input) is the single-agent alternative for a strictly-sequential
step-NN plan — the skill's triage section says when to hand off to it.

# How to use

## What it does

Takes a plan directory that a planner already produced and executes it end to end. It reads the phase dependency graph, runs sequential phases one at a time through an implement-then-review loop, dispatches parallelizable phases concurrently as isolated subagents, and records progress in the task ledger so an interrupted run can pick up where it stopped. Commits, pushes, and deploys stay behind ASK gates — you approve them, the command does not.

## When to use it

- You have an approved plan directory with `phase-N-*.md` docs and want it built without babysitting each phase.
- A previous run stopped partway and you want the remaining phases picked up from the ledger.
- The plan has phases that can run side by side and you want them dispatched concurrently instead of one after another.

## When not to use it

- The plan does not exist yet — run `@core:implementation-planner` or `@core:task-planner` first.
- You want re-planning, a pre-mortem, and the full quality-gate panel around the work — use `@core:tech-lead` instead.
- The plan is a strictly sequential `step-NN` plan — `@core:implementation-agent` in Plan-execution mode is the lighter single-agent path.

## How to invoke

```
/run-plan
/run-plan <path/to/working/2026/08/06/orders-line-items/plan>
```

Run it from your main session. It dispatches executor subagents itself, so it cannot be delegated to an agent — a subagent cannot spawn the subagents the playbook needs.

## Inputs

- Plan directory — path to a `.../{slug}/plan/` folder — optional. With no argument, the newest plan directory in the project workspace is used.

## What you get back

Each phase doc gets its acceptance-criteria checkboxes ticked and its `## Completion Report` section filled in as work lands. Source files change in the repository. The task ledger is updated after every phase, so progress survives a stopped session. You are asked before any commit, push, or deploy.

## Worked example

```
/run-plan ~/workspace/orders/working/2026/08/06/line-items-api/plan
```

The command parses the plan's phase table, sees phases 1 and 2 are independent and
dispatches both at once, then runs phase 3 after they pass review. Each finished phase
comes back reviewed, with its checkboxes ticked and a Completion Report written into
the phase doc. At the end you have the code changes on disk, uncommitted, and a prompt
asking whether to commit.

## Related

- `@core:implementation-planner` and `@core:task-planner` — produce the plan this command consumes.
- `@core:tech-lead` — prefer it when the work still needs planning and full quality gates, not just execution.
- `@core:implementation-agent` — the single-agent alternative for a strictly sequential plan.
