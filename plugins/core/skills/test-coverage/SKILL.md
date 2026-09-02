---
name: test-coverage
description: "FIND test coverage gaps (read-only) -- map source to test files, flag untested modules by risk. NOT for writing/running/debugging tests (use testing) or measuring coverage percentages (run --coverage directly)."
version: "1.0.0"
owner: "swarmery-core"
disable-model-invocation: true
allowed-tools: Read, Grep, Glob, Bash
color: teal
docs:
  status: reviewed
  source_sha: 41a89174d34a
  updated: 2026-08-06
---

# Purpose

Analyze test coverage gaps across the project's repositories (`project.json → repos`) by mapping source files to test files, identifying untested modules and public functions, classifying risk, and suggesting what to test. This skill identifies *what* to test and owns the authoritative coverage-target table; the `testing` skill provides *how* — run this first, then hand off.

# Rules (never violate)

1. Read-only: never create or edit test files during gap analysis.
2. Exclude generated files, type declarations, build outputs, and test fixtures from the "untested" count.
3. Every untested module gets a risk classification (High/Medium/Low) with rationale.
4. Label the gap number an estimate — file/function mapping, not tool-measured line coverage.
5. Never run the full suite (`make test-all`) — gap analysis needs no execution.
6. Report stays within 200 lines; over 50 gaps → group by directory, detail the top 10 by risk.

# Resources

- Read `resources/gap-analysis-procedure.md` when running the analysis: the 6-step procedure (scope, inventory, mapping, gaps, suggested cases, target comparison) with checkpoints, the report template, the authoritative coverage-target table, self-check, mistakes, escalation, and failure modes.

# How to use

## What it does

Finds the holes in your test suite without touching a single file: it walks a repository or module, matches every source file to its expected test file, and reports what has no coverage — plus which exports inside tested files still lack cases. Each gap carries a risk level, a suggested test file path following your conventions, and 3–5 cases worth writing.

## When to use it

- "What's untested?" needs an answer grounded in the actual file tree.
- Before a release, confirming high-risk paths (auth, streaming, mutating routes) have tests.
- After new modules land, checking whether tests landed too; prioritizing limited testing time.

Not for: writing/running/debugging tests (`testing`), real line/branch percentages (run `--coverage` directly), code complexity (`code-quality`/`code-standards`), or CI test jobs (the project's infra pack skills).

## How to invoke

```
Skill(skill: "core:test-coverage")
```

Give it the scope (repo or module path, required) and optionally a priority filter: `critical`, `api`, or `all` (default). Without a scope it asks before globbing.

## Worked example

```
Skill(skill: "core:test-coverage")
> Analyze apps/<mainApp>/src/lib, focus on critical modules.
```

The skill confirms scope and exclusions, globs source files, maps each to its expected test path, and classifies what's missing. You get a table showing `orders/line-items/` at 4 source files and 1 test file, then a detail block for the untested reducer: its two exports, a suggested `orders/line-items/__tests__/totals.test.ts`, and cases for the happy path, a rounding edge case, and malformed input — plus the target comparison and excluded-files list.

## Related

`testing` (write the tests this report asks for), `security-audit` (modules deserving higher test priority), `code-quality` (complexity, a separate concern).
