---
description: Cross-repo impact analysis — graph-aware (Graphify) with a live ripgrep fallback
color: red
docs:
  status: generated
  source_sha: fc326bb79534
  updated: 2026-08-06
---

# Impact Analysis Command

Find everything affected by a change to: $ARGUMENTS

## Primary path — Graphify (graph-aware, most accurate)

Graphify builds a per-repo knowledge graph at `<repo>/graphify-out/graph.json`. Each repo from
`project.json → repos` has its own graph — **run the commands from inside the repo you are
analyzing** (or pass `--graph <repo>/graphify-out/graph.json` explicitly).

1. **Blast radius** — `graphify affected "$ARGUMENTS" --depth 3` — reverse traversal listing
   everything that depends on the symbol (run per repo: the main app from `project.json → mainApp`,
   then the device/edge repo from `project.json → device` if the project has one).
2. **Connection trace** — `graphify query "what depends on $ARGUMENTS?"` for a BFS answer, or
   `graphify path "$ARGUMENTS" "<other symbol>"` to prove/disprove a specific dependency.
3. **Symbol context** — `graphify explain "$ARGUMENTS"` — the node, its neighbors, and its
   community, with file:line citations.
4. Weigh edge confidence: `EXTRACTED` edges come straight from the AST (trust them);
   `INFERRED` edges were model-resolved (verify before calling them WILL BREAK).

> If the staleness hook warns the graph is behind HEAD, run `graphify update .` first
> (add `--force` after refactors that deleted files) — otherwise the blast radius may
> omit new callers (a false "safe to change").

## Fallback path — ripgrep (always live; covers anything the graph misses)

Use ripgrep whenever the graph is stale or absent, and to double-check infra config
(deploy manifests/YAML) if it is not in the graph. Run from the workspace root, listing
the repos from `project.json → repos`:

```bash
rg -n --no-heading "$ARGUMENTS" \
  apps/<mainApp> <device-repo> \
  <infrastructure-repo>
```

## What to report

1. Total occurrences + per-repo breakdown.
2. File paths with line numbers and surrounding context.
3. For graph results: depth (`d=1` WILL BREAK / `d=2` LIKELY / `d=3` MAYBE) and the
   edge confidence tag (`EXTRACTED` vs `INFERRED`).
4. Recommended update order if the symbol changes (interfaces → impls → callers → tests).
5. **Cross-tier flag:** if the symbol crosses the main app ↔ the device/edge repo, call out the
   manual contract (no shared schema) and the coordinated-merge requirement.

## Response format

```markdown
## Impact Analysis for "$ARGUMENTS"

### Summary
- Total occurrences: X · Repositories affected: Y · Source: Graphify graph / ripgrep

### <mainApp> (Z)
- d=1 (WILL BREAK): src/app/api/things/route.ts:45 — POST handler [calls, EXTRACTED]

### <device repo> (N)
- src/send_data.py:78 — payload["status"] = order_status

### Recommendations
- [update order + tests to run]
```

Now analyze impact of: $ARGUMENTS

# How to use

## What it does

Finds everything that would break if you change a symbol, across every repository in your project. It leads with the Graphify knowledge graph for a real dependency traversal, then falls back to a live ripgrep sweep so nothing is missed when the graph is stale or absent. You get one report with per-repo hits, a break-likelihood rating, and the order to update things in.

## When to use it

- You are about to rename or change the signature of a function, type, or field and need the full caller list first.
- A change touches a shared contract between the main app and a device or edge repo, and you need to know whether both sides move together.
- You want to prove or disprove that one symbol actually reaches another before assuming a dependency exists.
- A reviewer asks "what else does this affect?" and grep alone keeps missing indirect callers.

## When not to use it

- You just want to locate a string or file — use `/search` or `/find`, which are faster and do not build a report.
- You need a full architecture overview rather than one symbol's blast radius — use `/architecture-map`.
- You already have the affected list and want a change sequence written up — use `/refactor-plan`.

## How to invoke

```
/impact createOrder
```

Type the command followed by the symbol you are changing. Run it from inside the repo you want analyzed so the per-repo graph at `graphify-out/graph.json` resolves, or let the ripgrep fallback sweep the repos listed in your project config.

## Inputs

- **symbol** — the function, type, field, or identifier you are about to change — required. Anything you can name in code works; the more specific the name, the tighter the result.

## What you get back

A single markdown report in the chat. It opens with a summary line (total occurrences, repositories affected, and whether the graph or ripgrep produced the result), then a section per repository listing file paths with line numbers and context. Graph-sourced hits carry a depth rating — `d=1` WILL BREAK, `d=2` LIKELY, `d=3` MAYBE — and an edge-confidence tag (`EXTRACTED` from the syntax tree, `INFERRED` by a model and worth verifying). The report closes with a recommended update order: interfaces, then implementations, then callers, then tests. Nothing is written to disk and no files are edited.

## Worked example

```
/impact OrderStatus

→ ## Impact Analysis for "OrderStatus"
  ### Summary
  - Total occurrences: 14 · Repositories affected: 2 · Source: Graphify graph
  ### apps/<mainApp> (11)
  - d=1 (WILL BREAK): src/orders/line-items/route.ts:45 — POST handler [calls, EXTRACTED]
  ### <device repo> (3)
  - src/send_data.py:78 — payload["status"] = order_status
  ### Recommendations
  - Update the shared enum, then the route handler, then the contract tests.
  - Cross-tier: the device repo has no shared schema — coordinate the merge.
```

You end up knowing which eleven call sites break immediately, which three live behind a hand-maintained contract in another repo, and that the two changes have to land together.

## Related

- `/search` — plain ripgrep across repos when you want matches, not analysis.
- `/refactor-plan` — turns a known blast radius into a sequenced refactor plan.
- `/graphify` — build or refresh the knowledge graph this command reads from.
