---
name: refactor-plan
description: "PRODUCES a refactoring plan ONLY -- impact analysis, step ordering, risk, rollback -- NO code changes. NOT for executing refactors (use functional-design) or dependency upgrades (use deps-check)."
version: "1.0.0"
owner: "swarmery-core"
allowed-tools: Read, Grep, Glob
disable-model-invocation: true
docs:
  status: reviewed
  source_sha: 5d88f40704d6
  updated: 2026-08-06
---

# Purpose

Generate a structured refactoring plan for a proposed code change: current state, cross-repo impact, step-by-step migration order, risks, and rollback. The output is a markdown document reviewed before execution — this skill plans only, never edits code. Success: every cited file was found via Grep/Glob, all affected repos in every stack language are covered, and the rollback is executable without interpretation.

# Rules (never violate)

1. Read-only: Read/Grep/Glob only — no file is ever modified.
2. Every file cited in the plan is a real Grep/Glob result, never a guess.
3. Blast radius over 50 files → stop and escalate to the user before continuing.
4. Save the plan to the workspace task dir (`.../plan/README.md`), never inside a code repo.
5. Rollback must be specific — commit references, migration file names, revert order.
6. Schema changes go through migrations (landing before dependent code), never manual DDL.

# Resources

- Read `resources/planning-procedure.md` when building the plan: the 7-step procedure with checkpoints, self-check, mistakes, escalation triggers, and what to surface.
- Read `resources/plan-template.md` when writing the output: the template, save-location contract, length budget, a full worked rename example, and failure modes.

# How to use

## What it does

Writes a refactoring plan for a change you are considering — and nothing else. It greps the codebase for every real reference, maps which repos and files are hit, orders steps so nothing lands before its dependency, names the risks, and spells out how to undo each step, all for review before anyone touches code.

## When to use it

- Renaming a type, table, or module and needing to know how far the change reaches.
- Extracting shared logic or splitting a module across repo boundaries.
- Changing a streamed (WebSocket/SSE) message format between producer and consumer; migrating one pattern to another across many files.

Not for: executing refactors (`functional-design`), dependency bumps (`deps-check`), one-line fixes, or release/environment rollback (the project's infra pack skills).

## How to invoke

```
Skill(skill: "core:refactor-plan")
```

State the refactoring goal, the repos in scope (defaults to all `project.json → repos`), and any constraints (freeze windows, untouchable areas).

## Worked example

```
Skill(skill: "core:refactor-plan")
"Rename the legacy_entity table and every reference to device,
 across apps/<mainApp> and the edge repo."
```

The skill greps both repos and finds 47 references in 23 files plus 8 in the edge service. The plan orders the migration first, then the ORM schema, the API route directory, imports and tests; flags that renaming the public route breaks external callers, proposes an alias, and names the exact revert migration in the rollback.

## Related

`monorepo-coordination` (cross-repo merge ordering), `functional-design` (execute instead of plan), `migration-check` (verify an introduced migration), `code-standards` (conventions while executing).
