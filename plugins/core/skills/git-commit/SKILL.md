---
name: git-commit
description: "Generate conventional commit messages when staging files for commit in any of the project's repos. Do not use for git tag messages, merge commit messages, changelog entries, or automated version bump commits."
version: "1.0.0"
owner: "swarmery-core"
disable-model-invocation: true
color: teal
docs:
  status: reviewed
  source_sha: 2125a5dd2def
  updated: 2026-08-06
---

# Purpose

Generate conventional commit messages for staged changes: a `<type>(<scope>): <subject>` subject line, optional bullet body, optional `BREAKING CHANGE:` / `Closes #N` footer. Message generation only — no git operations are run. Scopes come from the project's `.claude/project.json` -> `commitScopes`.

# Rules

- NEVER generate a message when staged files may contain secrets (`.env`, `*.populated.yaml`, `credentials.json`, `*.key`, `*.pem`, `*secret*`). Refuse and instruct the user to unstage them — no exceptions, no confirmation prompt.
- Subject: imperative mood, lowercase first word, no trailing period, max 72 characters, describing the user-visible change — never "update file X".
- One commit message per repo. Never combine scopes like `feat(app,infra)`.
- Never use a deprecated scope — map it to its documented replacement from the project's `CLAUDE.md`.
- Add a `BREAKING CHANGE:` footer whenever the change breaks existing APIs, CLI flags, or port assignments.

# Resources

- Read `resources/procedure.md` when generating a message — the step-by-step procedure, inputs/outputs, security gate, self-check, and escalation rules.
- Read `resources/format-reference.md` when picking type or scope — type/scope tables, subject rules, multi-scope handling, examples, failure modes.
- Read `examples/commit-examples.md` for additional worked examples (it may contain deprecated legacy scopes such as `be`/`fe`/`helm` — always use current `commitScopes`).

# How to use

## What it does

Turns a staged diff into a conventional commit message: type chosen from what actually changed, scope mapped from the touched files to the project's own scope list, subject kept under 72 characters in imperative mood. It refuses outright when staged files look like they carry secrets.

## When to use it

- You finished an implementation task, staged the files, and need the commit message.
- You squashed several commits and need one summary message.
- You want an existing message checked against project conventions.
- A change spans two repos and needs one correctly scoped message per repo.

Not for tag messages, merge commits, or changelog entries.

## How to invoke

```
Skill(skill: "core:git-commit")
```

Invoke once changes are staged. Inputs: `diff` (staged diff or change description) and `repo` (target repo, which decides the scope). You get back the message plus one sentence of type/scope reasoning; nothing is written to disk and no git command runs.

## Worked example

```
Staged: apps/<mainApp>/src/orders/line-items.tsx, line-items.test.tsx

feat(app): show per-line-item totals on the orders page

- Add LineItemTotals component with currency formatting
- Cover empty and single-item states in tests
```

Type/scope reasoning: a new user-visible capability in the main app, so `feat(app)`.
