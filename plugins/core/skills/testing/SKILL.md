---
name: testing
description: "WRITE, RUN, or DEBUG tests -- pytest, Jest/RTL, Playwright E2E, deployment config. NOT for finding coverage gaps (use test-coverage) or CI pipeline config (use deployment)."
version: "1.0.0"
owner: "swarmery-core"
allowed-tools: Read, Write, Bash, Grep, Glob
color: teal
docs:
  status: reviewed
  source_sha: 86527cbef0ee
  updated: 2026-08-06
---

# Purpose

Write tests, run test suites, and debug test failures across the project's repositories (`.claude/project.json` → `repos`). Typical layout: a device/edge repo `<device>` (Python/pytest), the main app `apps/<mainApp>` (TypeScript/Jest + RTL, Playwright E2E), and an infrastructure repo (deployment config via template render + dry-run); placeholders come from `project.json → mainApp` / `device`.

# Rules (never violate)

1. Verify the source module exists (Glob/Read) before writing a test — never test a module you cannot locate.
2. Always run a test after writing it; never return a test that was not executed and passing.
3. Test behavior, not implementation; no vacuous assertions (`expect(true).toBe(true)`).
4. A single test file stays under 200 lines — split by feature area if larger.
5. Check prerequisites before integration/E2E runs: `make test-all` needs a database, `npm run test:e2e` needs a running app.
6. Never delete a flaky test or run `npm install`/`pip install` without user approval.

# Resources

- Read `resources/writing-test-patterns.md` when writing tests: philosophy (AAA, TDD), the testing pyramid, the framework/location/naming table, and code patterns for pytest, Jest, RTL, Playwright, and deployment config dry-runs.
- Read `resources/running-and-debugging.md` when running suites or debugging failures: per-repo commands, the 4-step debug procedure, self-check, common mistakes, escalation, and failure modes.

# How to use

## What it does

A hands-on test engineer: writes new tests, runs existing suites, and debugs failures across a multi-repo project — pytest, Jest with React Testing Library, Playwright browser flows, deployment config through template renders and dry-run installs. It picks the right framework, file location, and naming convention for the repo, then runs the test to prove it passes.

## When to use it

- Tests needed for a module you just implemented, or added to existing code.
- Running a suite for pass/fail; a failing or flaky test needing its root cause found and fixed.

Not for: finding what lacks tests (`test-coverage`), CI test jobs (the project's infra pack skills), lint/type checking (`code-standards`/`code-quality`), or live-app debugging (`troubleshooting`).

## How to invoke

```
Skill(skill: "core:testing")
```

State the goal (`write`, `run`, or `debug`) and the target (module path, test file, or test name), plus the repository if ambiguous.

## Worked example

```
Skill(skill: "core:testing")
"Write unit tests for the coordinate formatter in apps/<mainApp>."

→ Confirms the repo uses Jest, reads src/lib/utils/formatCoordinates.ts,
  writes src/lib/utils/__tests__/formatCoordinates.test.ts with AAA-style
  cases for the normal path and the zero-value edge case, then runs
  `npx jest src/lib/utils/__tests__/formatCoordinates.test.ts`.
```

You end up with a passing test file and the runner output showing 2 passed. For `debug`, you get the root cause plus a full-suite re-run to catch regressions.

## Related

`test-coverage` (what needs tests; owns the coverage targets), `code-standards` (naming and quality conventions), `troubleshooting` (the application misbehaving, not the test).
