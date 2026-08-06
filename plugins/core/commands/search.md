---
description: Fast code search across the project's repositories using ripgrep
allowed-tools:
  - code_text_search
  - code_file_search
  - code_batch_search
  - WebFetch
color: red
docs:
  status: reviewed
  source_sha: 354c7f4bad5c
  updated: 2026-08-06
---

# Code Search Command

Search for: $ARGUMENTS

## Instructions

You are a code search assistant. Use the code search tools to find the requested information quickly.

### Available Tools

1. **code_text_search** - Search for text in code
   ```javascript
   code_text_search({
     query: "search term",
     repo: "<repo>",            // one of the project's repos (see project.json → repos)
     filePattern: "*.ts",       // optional filter
     wholeWord: true             // optional exact match
   })
   ```

2. **code_file_search** - Find files by pattern
   ```javascript
   code_file_search({
     pattern: "*Order*.tsx",
     repo: "<repo>"
   })
   ```

3. **code_batch_search** - Search all repos at once
   ```javascript
   code_batch_search({
     query: "OrderStatus"
     // searches all repos by default
   })
   ```

### Fallback (if MCP tools not available)

Use WebFetch to call the REST API:

```javascript
WebFetch({ url: "http://localhost:4001/search/text?q=QUERY&repo=<codePath>/<repo>" })
```
(`codePath` and the repo list come from the project's `.claude/project.json`.)

### Repositories

Read the searchable repos from the project's `.claude/project.json` (`repos`, plus `mainApp` and
`device` when present). Do not assume a fixed repo layout — it varies per project.

### Search guidance

- Default to the project's `mainApp` (from `project.json`) for web app + API work.
- Keep the `device`/edge repo in scope for telemetry, edge-runtime, or firmware issues, if the project defines one.
- Keep infrastructure/deployment repos in scope for Cloud Run / cluster deployment work.

### Response Format

Provide results in this format:

```markdown
## Search Results for "[query]"

Found X matches in Y repositories:

### [repo_name] (Z matches)

📄 path/to/file.ts:123
   > matching line content

📄 path/to/another.ts:456
   > matching line content
```

---

Now search for: $ARGUMENTS

# How to use

## What it does

Finds code across every repository in your project without you having to remember where anything lives. You give it a term — a symbol, a string, a filename fragment — and it searches all the repos listed in your project config at once, then reports the matches grouped by repository with file paths, line numbers, and the matching line.

## When to use it

- You know a symbol or string name but not which repository or file it lives in.
- You are about to change something and want every call site across the workspace first.
- You want to find files by name pattern, such as every component matching `*Order*.tsx`.
- You are tracing a behaviour that crosses a web app, an edge service, and deployment config.

## When not to use it

- You want relationship-aware answers ("what breaks if I change this?") — use `/impact`.
- You only need to locate files by name in one place — `/find` is narrower and faster.
- You want an architectural overview rather than literal matches — use the architecture map.

## How to invoke

```
/search OrderStatus
```

Type the command followed by whatever you want to find. Everything after the command is the search term, so quoting is unnecessary for single words.

## Inputs

- **search term** — the text, symbol, or file pattern to look for — required.
- **`.claude/project.json`** — supplies the repository list, plus the main app and device repos when the project defines them — required, read automatically.

## What you get back

A single markdown report: a heading with your query, a count of matches and repositories, then one section per repository. Each hit is one line with the file path and line number followed by the matching source line, so you can jump straight to it. Nothing is written to disk and no files are modified.

## Worked example

```
/search OrderStatus
```

It reads the repo list from your project config, searches them all in one pass, and returns something like:

```
## Search Results for "OrderStatus"

Found 7 matches in 2 repositories:

### apps/<mainApp> (5 matches)

📄 src/orders/line-items/status.ts:42
   > export type OrderStatus = 'pending' | 'shipped'
```

You end up with every definition and usage in one list, ready to open or hand to a refactor.

## Related

- `/find` — locates files by name pattern only, when content does not matter.
- `/impact` — traces downstream effects of a change rather than listing literal matches.
- `/code-quality` — inspects the code you found for complexity and convention issues.
