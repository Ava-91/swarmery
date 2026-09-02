---
name: context-optimization
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when a task spans 3+ files or crosses repo boundaries and context usage must be managed. Don't use it for single-file edits or read-only queries."
disable-model-invocation: true
allowed-tools: Read, Grep, Glob
color: teal
docs:
  status: reviewed
  source_sha: 9d0a19d62c88
  updated: 2026-08-06
---

# Purpose

Plan what to read before reading it. On a task spanning several files or repos,
turn "open everything and hope" into a short ordered context plan: which files,
which line ranges, in which phase, and where to `/clear` between them. It changes
no code — it governs loading so the skills that do the work keep room to think.

# Rules (never violate)

1. Run `codebase-retrieval` before any full-file read; discovery precedes loading.
2. Read with offset/limit — never whole files when one function is the target.
3. Keep files loaded ≤ 3× files actually edited; record the ratio in the plan.
4. Suggest `/clear` at every repo switch and at every phase boundary that frees files.
5. At ≥40% window usage, delegate any isolatable read (one whose product is a
   digest) to a **leaf** subagent at depth-1 — never leaf → leaf.
6. Confidence LOW after discovery means stop and ask, not a plan you half-trust.

# Resources

- Read `resources/planning-procedure.md` before planning: the 7-step procedure,
  the repo/glob map, the step-7 delegation decision table (40% / 60% thresholds),
  the plan template, self-check, escalation triggers, failure modes, and a worked
  two-phase example.

# How to use

## What it does

Produces a context plan: targeted reads in phases, `/clear` decisions with
reasons, and a loaded-versus-edited budget with a confidence rating. It runs
discovery first, so the plan names real files rather than guesses.

## When to use it

- The task touches 3 or more files, in one repo or across several.
- The task crosses a repo boundary — `apps/<mainApp>` plus `<device>`, say.
- Your window is already past 40% and more reads are coming.
- You are about to load a large module tree just to extract a verdict or a list.

Not for: single-file edits (just make them); a read-only question about one
function (`code-search`); or work already running inside a scoped subagent — that
isolation is what this skill would have bought you.

## How to invoke

```
Skill(skill: "core:context-optimization")
Task: fix telemetry latency between the device/edge repo and the main app
Repos: apps/<mainApp>, <device>
```

Invoke it at the start, before any full-file reads. `task_description` is
required; `repos_involved` is inferred from the task and `.claude/project.json`
when omitted.

## Worked example

The invocation above returns a two-phase plan of at most 30 lines. Phase 1 reads
two main-app files by line range — the WebSocket reconnect logic and the SSE
endpoint handler — then marks `/clear before Phase 2: yes`, because the work
switches repos. Phase 2 reads one range in the device sender. Budget: 3 files
loaded, 1 edited, ratio 3:1, confidence HIGH. Had confidence come back LOW, it
would have asked you to narrow the scope instead.

## Related

- `code-search` — when you already know the identifier and want every reference.
- `code-quality` — run it after this skill has narrowed the review set.
- `api-integration` — compose when the task turns on API flows across repos.
