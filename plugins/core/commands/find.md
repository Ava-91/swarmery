---
description: Find files by pattern across the project's repositories
allowed-tools:
  - code_file_search
  - WebFetch
color: red
docs:
  status: reviewed
  source_sha: d9eb863dfb47
  updated: 2026-08-06
---

# Find Files Command

Find files matching: $ARGUMENTS

## Instructions

Use `code_file_search` to find files by pattern.

### Usage

```javascript
code_file_search({
  pattern: "$ARGUMENTS",
  repo: "<repo>"  // one of the project's repos (project.json → repos); adjust based on context
})
```

### Examples

- `*Order*.tsx` - Find all files with "Order" in the name
- `*.spec.ts` - Find all test files
- `*.sql` - Find all SQL migration files
- `*Service*.ts` - Find all service files
- `values*.yaml` - Find deploy/values files

### Repositories

If the user doesn't specify a repo, pick the most likely one from the project's `.claude/project.json`
(`repos`, `mainApp`, `device`) based on the file pattern — e.g. web extensions (`.tsx`, `route.ts`)
map to the `mainApp`; `.py`/firmware patterns map to the `device` repo if one is defined; deploy
manifests map to the infrastructure repo. The layout varies per project, so read it — don't assume.

### Fallback

If MCP tools are not available:

```javascript
WebFetch({ url: "http://localhost:4001/search/files?pattern=PATTERN&repo=<codePath>/<repo>" })
```
(`codePath` from `project.json`.)

---

Now find files matching: $ARGUMENTS

# How to use

## What it does

Finds files by name pattern across every repository in your project. You give it a glob like `*Service*.ts`, and it searches the right repo for you — picking the most likely one from your project configuration when you don't name it. You get back a list of matching file paths instead of guessing where something lives.

## When to use it

- You know part of a filename but not which repo or directory holds it.
- You want every file of a kind at once — all test specs, all SQL migrations, all deploy value files.
- You are new to a project and need to see how files of a given type are laid out.
- You are about to edit a component and want to confirm there is only one file with that name.

## When not to use it

- You are searching for text *inside* files, not filenames — use `/search` instead.
- You want to know what breaks if you change a file — use `/impact` for that.
- You already know the exact path; just read the file directly.

## How to invoke

```
/find *Order*.tsx
```

Type the command followed by a filename pattern. Globs with `*` work as you would expect, and the pattern is matched against the file name.

## Inputs

- **pattern** — a filename glob such as `*.spec.ts` or `values*.yaml` — required.
- **repository** — optional. Name a repo in your request to narrow the search; otherwise the command infers the likeliest one from your project configuration based on the pattern.

## What you get back

A list of matching file paths. Nothing is written or changed — this is a read-only lookup. If the search tooling is unavailable, the command falls back to a local HTTP search endpoint using the code path from your project configuration and reports results the same way.

## Worked example

```
/find *.spec.ts

→ searches the main application repository for test specs
→ returns paths such as:
   apps/<mainApp>/src/orders/line-items.spec.ts
   apps/<mainApp>/src/orders/totals.spec.ts
```

You end up with the full set of spec files and can open the one you need, or hand the list to another command.

## Related

- `/search` — prefer it when you are matching file contents rather than file names.
- `/impact` — prefer it when you need the downstream effect of changing a file.
- `/code-quality` — prefer it when you want issues in files, not the files themselves.
