---
name: code-quality
description: "QUANTITATIVE structural metrics on TS/Python -- function length, cyclomatic complexity, nesting, duplication, code smells -- scored report. NOT for conventions/`any` checks (use code-standards) or API alignment (use api-contract)."
version: "1.0.0"
owner: "swarmery-core"
allowed-tools: Read, Grep, Glob, Bash
disable-model-invocation: true
docs:
  status: reviewed
  source_sha: 52e55cb2efc6
  updated: 2026-08-06
---

# Purpose

Score TypeScript or Python source on **structural** quality: function length,
nesting depth, cyclomatic complexity, duplication, code smells, and
project-specific anti-patterns. Every finding cites `file:line`; every category
gets a 0–100 score. Audit only — this skill never edits code.

"Does this follow our conventions?" → `code-standards`, which owns `any`-type
detection, naming, and imports. "Is this too long or too deeply nested?" → here.

# Rules (never violate)

1. Never edit source — findings and scores only; fixes go to `@implementation-agent`.
2. Every finding carries a `file:line` citation and a severity (Error / Warning).
3. Apply one threshold set per file — TypeScript thresholds never judge Python.
4. Never grep for `any` or missing types; that is `code-standards`' scope.
5. Skip generated, migration, and vendored files; blank and comment-only lines
   never count toward function length.
6. Emit the machine-readable `QUALITY-SCORE:` header as the report's first line.

# Resources

- Read `resources/audit-procedure.md` before auditing: the threshold table, the
  9-step procedure, the project-specific check list, the scoring formula, the
  self-check, escalation triggers, and failure modes.
- Read `resources/report-format.md` when writing the report: the output template
  plus two worked examples (a TypeScript route handler, a Python device module).

# How to use

## What it does

Audits TypeScript or Python source for structural problems — functions that grew
too long, blocks nested too deep, duplicated code, project anti-patterns — and
returns a 0–100 score per category with every finding cited and a suggested fix.
"This file feels messy" becomes a list you can act on.

## When to use it

- A file or module needs checking for function length, complexity, or nesting.
- A pull request needs a structural quality assessment before review.
- A periodic quality audit of a repo or module is due.
- You just refactored and want proof the metrics improved.

Not for: conventions, `any` types, or import order (`code-standards`); schema ↔
handler alignment (`api-contract`); deployment manifests (`deployment`, infra
pack); or just locating files (`code-search`).

## How to invoke

```
Skill(skill: "core:code-quality")
scope: apps/<mainApp>/src/app/api/orders/route.ts
repo_type: typescript
```

`scope` (file, directory, or repo path) and `repo_type` (`typescript` |
`python`) are both required. The skill reads the files itself.

## Worked example

The invocation above reads the route handler, applies the TypeScript thresholds,
and returns:

```
QUALITY-SCORE: 68/100 | ERRORS: 2 | WARNINGS: 2

### Error-Level Issues (2)
1. `route.ts:15` -- GET() is 62 lines; extract query logic to the service layer
2. `route.ts:1` -- missing `export const dynamic = 'force-dynamic'`

### Action Plan
1. [High effort] Extract GET() query logic into a service module
2. [Low effort] Add the `force-dynamic` export -- one-line fix
```

A single-file audit stays under 200 lines, a directory audit under 500.

## Related

- `code-standards` — composes with this skill as a depth-1 fan-out under
  `@code-reviewer` over the same scope.
- `api-contract` — when a handler may disagree with its schema.
- `deployment` (infra pack) — deployment config and manifests.
- `code-search` — run first when the scope is still undetermined.
