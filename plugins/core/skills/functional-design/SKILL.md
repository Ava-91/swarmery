---
name: functional-design
description: "EXECUTES refactors of TypeScript business logic toward pure functions, immutability, and pipelines -- edits files via Edit. NOT for planning-only refactors (use refactor-plan) or I/O-heavy handlers / Python paths."
version: "1.0.0"
owner: "swarmery-core"
allowed-tools: Read, Edit, Grep
color: teal
docs:
  status: reviewed
  source_sha: 0de9c6a109eb
  updated: 2026-08-06
---

# Purpose

Rewrite TypeScript business logic so it stops mutating its inputs: mutating
functions become functions that return new values, hand-written loops become
`.filter()`/`.map()`/`.reduce()` chains over named predicates, and interface
properties gain `readonly`. Targets the main app's `src/lib/` services. This
skill **edits files** and shows a before/after snippet per change.

Done means: every refactored function is pure, immutable shapes carry `readonly`,
and `npm run typecheck` passes.

# Rules (never violate)

1. TypeScript only — never apply this to Python.
2. Edit only under `src/lib/`; a route handler gets at most one import-only Edit,
   never a refactor of its I/O.
3. Never wrap async I/O in a synchronous composition chain — `await`, then pipe
   the result through pure code.
4. Never introduce a new dependency (immer, ramda, lodash-fp) or an undeclared
   `pipe()`; chain methods or use sequential `const` assignments.
5. Verify every type by reading it — never guess a parameter shape.
6. Maximum 3 Edits per invocation, one per function, each with a shown diff.

# Resources

- Read `resources/refactor-procedure.md` before editing: the 7-step procedure,
  the route-handler boundary rule, the self-check, common mistakes, escalation
  triggers, two worked examples, and the failure-mode table.

# How to use

## What it does

Turns mutating, imperative business logic into pure functions over immutable
data, applies the edits itself, and reports each change with the principle behind
it and a short before/after snippet.

## When to use it

- A `src/lib/` service function mutates the object handed to it.
- A `for` loop with nested conditionals would read better as named predicates.
- A route handler tangles calculation with I/O and the calculation should move out.
- A class holds mutable state that could be plain data plus functions.

Not for: a plan without edits (`refactor-plan`); Python, naming, or framework
conventions (`code-standards`); a handler's own I/O, an ORM fluent builder, or
React `useState`/`useReducer` — each has its own contract; or hot per-tick
telemetry paths, where spread copies cost more than they buy.

## How to invoke

```
Skill(skill: "core:functional-design")
→ "Refactor calculateOrderCost in src/lib/orders/pricing.ts"
```

`file_path` is required; `function_name` is optional — omit it to scan the file.

## Worked example

`calculateOrderCost` used to assign `baseCost`, `shippingCost`, `totalCost`, and
`status` straight onto the `Order` it was handed. Afterwards it takes `readonly
OrderItem[]` and returns a new `OrderCostResult` whose three fields are all
`readonly`. The `status = 'PRICED'` line is gone — a status transition is a side
effect, and the summary tells you to move it into the caller.

You get the edits, a summary under 30 lines naming each principle applied, the
functions skipped and why, and `[VERIFY]` flags on anything needing manual
testing. It stops and asks first when a function mixes calculation with I/O, has
no tests but a changing return type, or has more than three call sites.

## Related

- `refactor-plan` — when the plan should be reviewed before any file changes.
- `code-standards` — general quality, naming, and Python refactoring.
- `code-quality` — broader review beyond functional patterns.
