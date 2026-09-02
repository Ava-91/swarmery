# context-optimization — procedure, delegation rule, and plan format

## Inputs

- `task_description` — the user's request or the delegated task summary.
- `repos_involved` — the repos the task may touch (e.g. `["apps/<mainApp>",
  "<device>"]`); inferred from `.claude/project.json` → `repos` when omitted.

## Procedure

1. **Identify repos** — from the task description, work out which repos are in
   play and map each to its language and search glob:

   | Repo (example) | Language | Search glob |
   |---|---|---|
   | `apps/<mainApp>` | TypeScript | `*.ts`, `*.tsx` |
   | `<device>` (device/edge repo) | Python | `*.py` |
   | infrastructure repo | YAML / service config | `*.yaml`, `*.yml`, `*.tpl` |
   | CI/CD config | YAML / Shell | `*.yaml`, `*.sh` |
   | versions/config repo | YAML / JSON | `*.yaml`, `*.json` |

   *Checkpoint: at least one repo identified.*

2. **Discovery first** — run `codebase-retrieval` with a focused query about the
   symbols, patterns, or data flow the task turns on. Do not read full files yet.

   **Confidence gate:** if the results reference none of the repos or symbols in
   `task_description`, rate confidence LOW and stop: "codebase-retrieval returned
   results that do not appear relevant to [task]. Narrow the scope or name the
   repo to search." *Checkpoint: at least one relevant result at HIGH/MEDIUM.*

3. **Targeted reads** — read only the relevant section of each file with
   offset/limit; at most 200 lines per file unless a single function is longer.
   Prefer imports plus the one function over the whole module.
   *Checkpoint: no full-file reads.*

4. **Incremental loading** — load only what the current phase needs. When a phase
   ends and its files are no longer needed, suggest `/clear` before the next.
   *Checkpoint: phase boundary identified.*

5. **Cross-repo boundary** — switching repos releases the previous repo's context:
   suggest `/clear` with the reason. *Checkpoint: suggestion made.*

6. **Track the budget** — keep a running count of files loaded versus files
   edited; target no more than 3 loaded per 1 edited.
   *Checkpoint: ratio recorded in the plan.*

7. **Isolate heavy reads behind a subagent summary** — see the decision rule below.

## Step 7 — the delegation decision rule

When a phase must load a large slice (a whole module tree) only to produce a
verdict or a short list, delegate that read to a **leaf** subagent — `@researcher`
for search-and-summarize, `@code-reviewer` for review-and-score. The leaf burns
its own window; `main` receives only the summary.

| `main` window usage | Output is a digest (summary / verdict / list)? | Action |
|---|---|---|
| < 40% | any | Read inline with offset/limit (steps 3–4). No delegation. |
| ≥ 40% | **yes** | **Delegate to a leaf** — `main` must not absorb the raw read. |
| ≥ 40% | no (you need the code in `main` to edit it) | Load-then-`/clear` (steps 4–5); delegation would not help. |
| ≥ 60% | any | Stop and ask (see Escalation) before loading anything further. |

The 40% line is the same trigger that activates this skill: once `main` crosses
it, an isolatable read is delegated by default. "Isolatable" means the read's
product is a digest, not source you must edit.

**Depth constraint:** one level only (orchestrator → leaf). Never chain leaf →
leaf: the fleet is depth-1, and a leaf that needs more work escalates back to its
orchestrator rather than spawning helpers. A leaf that invokes this skill skips
step 7 entirely — its context is already isolated.

*Checkpoint: at ≥40%, every isolatable read was delegated; `main` holds summaries.*

## Output template

At most 30 lines — a lightweight index, never a narrative.

```markdown
## Context Plan

**Task:** {one-line summary}
**Repos:** {repo list}
**Estimated files to edit:** {N}

### Phase 1 -- {description}
| # | Repo | File | Offset | Limit | Reason |
|---|------|------|--------|-------|--------|
| 1 | apps/<mainApp> | src/lib/telemetry/ws-client.ts | 12 | 35 | WebSocket reconnect logic |
| 2 | apps/<mainApp> | src/app/api/telemetry/stream/route.ts | 1 | 35 | SSE endpoint handler |

/clear before Phase 2: {yes|no} -- {reason}

### Phase 2 -- {description}
| # | Repo | File | Offset | Limit | Reason |
|---|------|------|--------|-------|--------|
| 3 | <device> | src/telemetry/sender.py | 20 | 40 | Telemetry sender |

### Budget
Files loaded: {N} / Files to edit: {M} (ratio: {N}:{M})
Confidence: {HIGH|MEDIUM|LOW} -- {rationale}
```

## Self-check before returning

- [ ] `codebase-retrieval` ran before any full-file read.
- [ ] No file loaded whole when one function was enough (offset/limit used).
- [ ] Files loaded ≤ 3× files actually edited.
- [ ] `/clear` suggested at every repo switch.
- [ ] Window usage stayed under 50% of the model's limit throughout.
- [ ] The plan follows the template — not free-form reasoning.
- [ ] The confidence gate was applied after discovery.
- [ ] At ≥40%, every isolatable read was delegated to a leaf at depth-1.

## Common mistakes to avoid

- Do not read 4+ full files before understanding the task's structure.
- Do not keep the previous repo's files loaded after switching repos.
- Do not `/clear` mid-task before saving the plan or partial progress.
- Do not assume a 200K window — check the model's real limit.
- Do not load test files alongside implementation files unless tests are the task.

## What to surface to the user

- The structured context plan, with a reason per row.
- The `/clear` suggestion and its rationale at each boundary or past 40%.
- A subagent recommendation when the task spans 3+ repos with independent work.
- A leaf-isolation recommendation when `main` is ≥40% and a phase needs a large
  read whose product is only a summary (step 7; depth-1).
- The confidence level from discovery.

## Escalation

Stop and ask when: the scope is ambiguous and the repos are unclear; discovery
returns nothing relevant (the task may be mis-scoped); the window passes 60% with
files still to load; or confidence is LOW — never hand over an unreliable plan.

## Failure modes

- **Discovery returns irrelevant results** — symptom: loaded files do not relate
  to the task. Fix: refine the query with specific symbols or paths; rate LOW.
- **`/clear` at the wrong moment** — symptom: the agent cannot recall what it
  found. Fix: write a short findings summary before clearing.
- **Budget exceeded** — symptom: earlier context is being truncated or confused.
  Fix: `/clear` and reload only what the remaining work needs.

## Worked example — telemetry latency across two repos

```markdown
## Context Plan

**Task:** Fix telemetry latency between the device/edge repo and the main app
**Repos:** apps/<mainApp>, <device>
**Estimated files to edit:** 1

### Phase 1 -- Understand the data flow (main-app side)
| # | Repo | File | Offset | Limit | Reason |
|---|------|------|--------|-------|--------|
| 1 | apps/<mainApp> | src/lib/telemetry/ws-client.ts | 12 | 35 | WebSocket reconnect logic |
| 2 | apps/<mainApp> | src/app/api/telemetry/stream/route.ts | 1 | 35 | SSE endpoint handler |

/clear before Phase 2: yes -- switching from the main app to the device/edge repo

### Phase 2 -- Check the device firmware side
| # | Repo | File | Offset | Limit | Reason |
|---|------|------|--------|-------|--------|
| 3 | <device> | src/telemetry/sender.py | 20 | 40 | Telemetry sender |

### Budget
Files loaded: 3 / Files to edit: 1 (ratio: 3:1)
Confidence: HIGH -- codebase-retrieval returned exact telemetry files
```

Result: 3 files loaded, 1 edited — within budget.
