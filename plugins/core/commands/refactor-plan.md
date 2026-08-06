---
description: Generate comprehensive refactoring plan with impact analysis
color: red
docs:
  status: reviewed
  source_sha: 61862657360c
  updated: 2026-08-06
---

# Refactoring Plan

Produce a structured, plan-only refactoring document -- current state analysis, cross-repo impact, step-by-step migration order, risk assessment, effort estimate, and rollback plan. No code changes are made.

Follow the playbook in `skills/refactor-plan/SKILL.md` (auto-loaded skill `refactor-plan`); apply it to $ARGUMENTS if provided.

For the **cross-repo impact** section, use Graphify rather than grep: `graphify affected "<symbol>"`
(blast radius), `graphify path "<A>" "<B>"` to prove a specific dependency, and
`graphify explain "<symbol>"` for the node's neighborhood — each repo has its own graph at
`<repo>/graphify-out/graph.json`, so run per repo (or pass `--graph` explicitly).
If the staleness hook reports the graph is behind HEAD, run `graphify update .` first.
For anything not in the graph (e.g. `devops/*` if unindexed) — use `rg` there.

To execute pure-function refactors directly, use the `functional-design` skill instead.

# How to use

## What it does

Turns a refactoring idea into a written plan before anyone touches code. You get the current state analysed, the cross-repo blast radius mapped, a migration order you can follow step by step, a risk assessment, an effort estimate, and a rollback plan. Nothing is edited — the output is a document you review and then decide on.

## When to use it

- You want to restructure a module or rename a widely used symbol and need to know what breaks first.
- The change spans more than one repository and you need a merge order rather than a diff.
- A reviewer or lead asked for a written plan and risk assessment before the work is approved.
- You suspect a refactor is bigger than it looks and want the effort estimate before committing to it.

## When not to use it

- You want the refactor performed, not planned — use the `functional-design` skill for pure-function refactors it can execute directly.
- You only need the blast radius of one symbol, not a full plan — run `/impact` instead.
- The work is a multi-week program rather than a refactor — reach for an implementation planner that produces phase docs.

## How to invoke

```
/refactor-plan
```

Type it on its own for a plan scoped to the current context, or add the target after it — a path, a module, or a symbol name.

## Inputs

- **target** — the module, path, or symbol to refactor, passed as an argument — optional. Without it, the plan is scoped from the current conversation context.

## What you get back

A structured plan document covering current state, cross-repo impact, ordered migration steps, risks, effort, and rollback. The impact section is built from the knowledge graph — `graphify affected`, `graphify path`, and `graphify explain` per repository — and falls back to `rg` for anything the graph does not index. No files in your codebase are modified.

## Worked example

```
/refactor-plan orders/line-items pricing calculation
```

The pricing symbols in `orders/line-items` are located, their callers traced across each repository's graph, and the results assembled into a plan: what the code looks like today, which consumers break, the order to migrate them in, what could go wrong at each step, roughly how long it takes, and how to back it out. You end up with a document to review — the code is untouched.

## Related

- `/impact` — prefer it when you only want the list of affected code, with no plan around it.
- `functional-design` — prefer it when you want the refactor applied rather than described.
- `/code-quality` — prefer it when you are still deciding whether a refactor is warranted.
