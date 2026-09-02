---
name: pr-generator
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when creating a pull request — generate the title, structured description, and review checklist from the branch's actual commits and diff. Don't use it for commit messages (git-commit skill) or changelogs."
disable-model-invocation: true
color: cyan
docs:
  status: draft
  updated: 2026-09-01
---

# Purpose

A PR description assembled from the real diff and commit history — not from
memory of what you meant to do.

# Method

1. Gather evidence: `git log <base>..HEAD --oneline`, `git diff <base>...HEAD --stat`,
   and read the diff for anything the summary will claim.
2. Title: conventional-commit style, ≤72 chars, states the change not the
   activity ("feat(orders): bulk line-item editing", not "worked on orders").
3. Description sections: **What** (the change in 2-4 sentences), **Why**
   (the problem/ticket), **How** (notable decisions, anything a reviewer
   would ask about), **Testing** (what was actually run — never claim
   untested checks), **Breaking changes / rollout** (or "None").
4. Review checklist: 3-7 items specific to THIS diff (the risky hunks, the
   contract edges, the migration), not generic boilerplate.
5. Respect the project's PR template when the repo has one
   (`.github/PULL_REQUEST_TEMPLATE.md` wins over this structure).

Never include Claude/AI attribution lines. Never invent test results.

# How to use

## What it does

Generates a PR title, a structured description grounded in the branch's real commits and diff, and a diff-specific review checklist — following the repo's own PR template when one exists.

## When to use it

After a branch is ready to push: the work is done, checks ran, and the PR needs a description a reviewer can trust.

## How to invoke

Load the skill and name the branch and base, e.g. "generate the PR for feature/bulk-edit against main" — or let an orchestrator load it at delivery time.

## Worked example

For a 4-commit branch touching an API route and a migration, it produces: title `feat(orders): bulk line-item editing`, a What/Why/How description citing the expand-migrate design, Testing listing the actual `npm test` output, and a checklist item "verify migration backfill on a copy of staging data — new NOT NULL column".
