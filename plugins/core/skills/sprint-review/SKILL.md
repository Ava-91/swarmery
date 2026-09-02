---
name: sprint-review
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill for an end-of-sprint read-only audit — scope recent changes across repos, fan out review agents, run check-only suites, and produce a PASS/FAIL report. Don't use it for reviewing a single PR or diff."
disable-model-invocation: true
color: purple
docs:
  status: draft
  updated: 2026-09-01
---

# Purpose

A periodic, read-only health pass over everything that changed in the window —
finding what slipped through per-change review, without modifying anything.

# Method

1. **Scope first** (always): per repo, `git log --since="${SINCE:-14 days ago}"`,
   `git diff --stat` against the window start, `git shortlog -sn`. Skip repos
   with zero commits; the changed-file list scopes every later step.
2. **Fan out reviews** (when you can spawn agents): @code-reviewer briefed
   per dimension (quality, silent failures, contracts) and @security-auditor —
   each with the scoped file list and "findings as severity + file:line,
   change nothing". When you cannot spawn, run the passes yourself and note it.
3. **Check-only suites**: typecheck/lint/tests per stack, never with fixes
   applied; distinguish new failures from known pre-existing ones.
4. **Report** with verdict `## Verdict: PASS | FAIL | PASS WITH BLOCKERS`,
   per-repo activity table, findings by severity, and per-subagent status
   (`COMPLETE | FAILED | SKIPPED`). Write it to
   `<workspace>/<project>/workspace/working/{YYYY}/{MM}/sprint-review-{YYYY-MM-DD}.md`.
   FAIL and PASS WITH BLOCKERS are human gates — list the blockers first.

Full phase detail and report template: `resources/audit-process.md`.

# How to use

## What it does

Runs the end-of-sprint audit: scopes the window's changes across all project repos, fans out read-only reviewers, runs check-only suites, and writes a dated PASS/FAIL report to the workspace with blockers ranked first.

## When to use it

At sprint or milestone boundaries, or before a release cut, when you want a fleet-level look at everything that landed rather than per-PR review.

## How to invoke

Load the skill and state the window: "sprint review for the last 14 days" (the default) or an explicit `SINCE` date. An orchestrator can run it as a standalone task.

## Worked example

"Sprint review since 2026-08-18": scoping finds 2 active repos (23 + 7 commits); @code-reviewer and @security-auditor sweep the 61 changed files; suites pass except one known-flaky E2E (noted as pre-existing); report lands at `workspace/working/2026/09/sprint-review-2026-09-01.md` with `## Verdict: PASS WITH BLOCKERS` — one P1 silent failure in a new webhook handler, listed first.
