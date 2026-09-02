---
name: summary-templates
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when a task involves summarizing completed work, writing a feature summary, documenting what changed, or creating a completion report for a task, feature, bug fix, or refactoring. Don't use it for writing documentation, changelogs, or postmortems."
disable-model-invocation: true
color: teal
docs:
  status: reviewed
  source_sha: 0e2c64e63f23
  updated: 2026-08-06
---

# Purpose

Provide standardized templates for structured summaries of technical work across four work types — task, feature, bug fix, refactoring — each with quantified metrics, role-specific instructions, and actionable next steps. This skill formats data you supply; it measures nothing and never modifies source files. The canonical end-of-task `SUMMARY.md` (seven fixed sections at the task root) belongs to `session-closeout` — defer to it for workspace task closeout; this skill keeps the per-change-type templates.

# Rules (never violate)

1. Never invent metrics — missing data is `N/A -- measure post-deploy`, never a fabricated number.
2. Never include sensitive data (credentials, API keys, PII).
3. Fill every template section or mark it `N/A`; never silently omit one.
4. Produce summary documents only — never modify source files.
5. Stay within the length budget: 200 lines markdown, 300 lines HTML.
6. Document the template-selection reasoning, especially for ambiguous work types.

# Resources

- Read `resources/template-structures.md` when selecting or filling a template: the four structures, `.claude/templates/` references and fallback rule, HTML mapping, domain-terminology guidance, worked examples.
- Read `resources/authoring-procedure.md` when producing the summary: the 6-step procedure with checkpoints, self-check, mistakes, escalation, failure modes.

# How to use

## What it does

Turns finished work into a structured summary someone else can act on: one of four templates — task, feature, bug fix, refactoring — picked by the work's primary intent, filled with your real numbers, role-specific notes, and next steps that name an owner. Missing data is marked `N/A`, never guessed.

## When to use it

- Someone asks to summarize a task, write a feature summary, or document what changed.
- A feature, bug fix, or refactoring needs a per-change-type completion report; an agent finished implementation and needs a structured write-up.

Not for: API documentation, changelogs, release notes, outage postmortems (`troubleshooting`), workspace task closeout (`session-closeout`), or measuring metrics.

## How to invoke

```
Skill(skill: "core:summary-templates")
```

Hand it the work type (`task`, `feature`, `bug-fix`, `refactoring`), the work details (files changed, metrics measured), and optionally the format (`markdown` default; `html` for >3 sections or sharing outside the terminal).

## Worked example

```
Skill(skill: "core:summary-templates")
"Summarize the auth middleware work — I pulled the session check out of
14 route handlers into one withAuth() wrapper. No behavior change."
```

The skill reads that as restructuring without behavior change and selects the Refactoring Summary: Problems Solved (the duplicated check in 14 handlers), before/after architecture, metrics grounded in your 14 → 1 count, and a migration guide showing `export const GET = withAuth(handler)`. Unmeasured coverage stays `N/A -- measure post-deploy`.

## Related

`session-closeout` (canonical task-root SUMMARY.md), `troubleshooting` (incident postmortems), `testing` (tests the summary references), `code-standards` (review findings feeding recommendations).
