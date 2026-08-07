---
description: Branch-from-fresh-main boilerplate — checkout main, pull, create branch with a meaningful name, push empty so it can be protected
allowed-tools:
  - Bash
docs:
  status: reviewed
  source_sha: b80f82ce0995
  updated: 2026-08-06
---

# /new-feature-branch — start work on fresh main

Automates the per-issue branch workflow that every operator task in this
project starts with, so the four-step recipe becomes one command.

## Usage

```
/new-feature-branch <slug-describing-the-work>
```

Examples:

- `/new-feature-branch fix/rollback-drift-check`
- `/new-feature-branch feat/maps-api-key-secret`
- `/new-feature-branch docs/runtime-maps-env-usage`
- `/new-feature-branch chore/check-chart-sync-helper`

## What it does

1. `git checkout main` — switch to the main branch.
2. `git pull origin main` — fast-forward local main.
3. `git checkout -b <slug>` — create feature branch.
4. `git push -u origin <slug>` — push empty branch so it can be
   protected in GitHub before substantive commits land.

Total: four commands replaced by one invocation.

## Branch naming convention

Prefix describes intent:

| Prefix | Use for |
|---|---|
| `feat/` | New functionality |
| `fix/` | Bug fix |
| `docs/` | Docs-only changes |
| `chore/` | Tooling / dev-experience / maintenance |
| `refactor/` | Code structure changes, no behaviour change |
| `test/` | Test-only changes |

Slug after prefix: dash-separated, lowercase, short but meaningful.

## Implementation

```bash
#!/usr/bin/env bash
set -Eeuo pipefail
slug="${1:?usage: new-feature-branch <prefix/slug>}"
git checkout main
git pull --ff-only origin main
git checkout -b "$slug"
git push -u origin "$slug"
echo "✓ Branch '$slug' created at main HEAD and pushed. Protect it in GitHub before substantive commits if desired."
```

## Prerequisites

- You are inside the target git repo's working tree.
- Working tree is clean (no uncommitted changes) OR your changes are stashed.
- `origin` remote is configured.

## Related

- The staging health command (if the project ships one, e.g. `/<envAlias>-health`) — first diagnostic after starting any deploy-related work
- Commit-message convention: see `skills/git-commit/`

# How to use

## What it does

Starting a branch the right way takes four commands, and skipping one leaves you branching off a stale main or with an unpushed branch that nobody can protect. This command does all four for you: it switches to main, fast-forwards it, cuts your new branch, and pushes it empty so branch protection can be applied before any real commit lands.

## When to use it

- You are about to start work on a new issue and want your branch cut from an up-to-date main.
- Your team protects branches in the hosting UI, and the branch must exist remotely before the first substantive commit.
- You keep forgetting the `git pull` step and end up rebasing later.
- You want branch names to follow one convention across everyone's work.

## When not to use it

- You already have a branch and just want to commit — use your normal commit flow instead.
- You have uncommitted changes you want to carry over — stash or commit them first, then run this.
- You need a branch cut from something other than main — do that by hand with `git checkout -b <slug> <base>`.

## How to invoke

```
/new-feature-branch <prefix/slug>
```

Type `/new-feature-branch` followed by the branch name you want. The prefix tells readers what kind of change it is: `feat/`, `fix/`, `docs/`, `chore/`, `refactor/`, or `test/`. After the prefix, use a short lowercase dash-separated slug.

## Inputs

- `<prefix/slug>` — the full branch name, prefix included — **required**. Without it the command stops and prints its usage line.

## What you get back

A new local branch created at the current main HEAD, checked out and ready for work, plus a matching remote branch on `origin` with upstream tracking already set. You get a one-line confirmation naming the branch and reminding you to protect it before substantive commits. Nothing is committed and no files change.

## Worked example

```
/new-feature-branch fix/rollback-drift-check
```

Main is checked out and fast-forwarded, `fix/rollback-drift-check` is created at that commit and pushed to `origin` with tracking set. You end up on the new branch with a clean tree, and the confirmation line reads:

```
✓ Branch 'fix/rollback-drift-check' created at main HEAD and pushed. Protect it in GitHub before substantive commits if desired.
```

## Related

- The project's staging health command, if one ships (for example `/<envAlias>-health`) — run it first when the work touches a deploy.
- The `git-commit` skill — use it once you have commits to write, for the message convention that pairs with these branch prefixes.
