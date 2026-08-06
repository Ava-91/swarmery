---
description: Check code quality issues (long functions, complexity, violations)
color: red
docs:
  status: reviewed
  source_sha: 2463d1f0a870
  updated: 2026-08-06
---

# Code Quality Check

Structural quality audit -- function length, cyclomatic complexity, nesting depth, duplication, and code smells -- for the project's TypeScript and Python source (apps and device repo per `project.json`), with a scored 0-100 report.

Follow the playbook in `skills/code-quality/SKILL.md` (auto-loaded skill `code-quality`); apply it to $ARGUMENTS if provided.

For conventions/naming/`any`-type checks use the `code-standards` skill instead.

# How to use

## What it does

This command audits the structural health of your project's TypeScript and Python source. It measures function length, cyclomatic complexity, nesting depth, duplication, and common code smells, then hands you a single 0–100 score with the specific spots that dragged it down. Use it when you want to know where the code is hard to change, not whether it is formatted correctly.

## When to use it

- You inherited a module and want a fast read on how tangled it is before touching it.
- A file keeps causing bugs and you suspect long functions or deep nesting are the reason.
- You are planning a refactor and need evidence for which units to split first.
- You want a repeatable score to compare against after a cleanup pass.

## When not to use it

- For naming, import order, or `any`-type checks — use the `code-standards` skill instead.
- For security vulnerabilities — reach for `/security-audit`.
- For missing test coverage — reach for `/test-coverage`.
- For a full refactor plan with impact analysis — reach for `/refactor-plan`.

## How to invoke

```
/code-quality
```

Run it with no arguments to audit the project's configured source repositories. Add a path, a glob, or a feature name after the command to narrow the audit to just that area.

## Inputs

- `arguments` — optional. A path, glob, or feature name that scopes the audit. Omit it to cover everything the project declares as source.

## What you get back

A scored report in the conversation: an overall 0–100 quality number, plus findings grouped by the dimension that produced them — oversized functions, high-complexity branches, deep nesting, duplicated blocks, and smells. Each finding names the file and the unit involved so you can jump straight to it. Nothing is written to disk and no code is changed.

## Worked example

```
/code-quality orders/line-items
```

The audit reads the source under that path, measures every function against the length, complexity, and nesting thresholds, and looks for duplicated blocks across the files. You end up with a score for that slice — say 62/100 — and a ranked list underneath it: the three longest functions with their line counts, the branches that push cyclomatic complexity past the threshold, and the duplicated block that appears in two of the files. That list is what you feed into your next refactor.

## Related

- `code-standards` — prefer it for conventions, naming, and type-safety review.
- `/refactor-plan` — prefer it once you know what is wrong and need a staged plan to fix it.
- `/test-coverage` — prefer it when the question is missing tests rather than tangled code.
