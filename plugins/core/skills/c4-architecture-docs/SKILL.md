---
name: c4-architecture-docs
version: "1.0.0"
owner: "swarmery-core"
description: "Document an epic/feature architecture with the C4 model -- system context/container/component/dynamic diagrams as Mermaid .mmd plus a narrative doc. NOT for rendering an existing .mmd (use mermaid-viewer)."
color: cyan
docs:
  status: reviewed
  source_sha: ff189543c257
  updated: 2026-08-06
---

# Purpose

Produce house-consistent **C4 architecture documentation** for a big issue: a small set of Mermaid C4 diagrams (`.mmd`) plus a short narrative doc, filed in the task dir and rendered for review. This skill owns the **HOW** — level selection, diagram grammar, labelling rules, and the file-and-promote workflow; it does not render (hand the `.mmd` to the project's Mermaid viewer) and does not replace an ADR. Project tiers and containers are never baked in — read them from `.claude/project.json` and the project's `CLAUDE.md`.

# Rules (never violate)

- Draw the **minimum** levels: L1+L2 always; one L3 only per touched container; Dynamic only for a non-obvious runtime flow; L4 almost never.
- Ground every box in the project's real inventory (`.claude/project.json`, `CLAUDE.md`) — never invent systems or protocols.
- Complexity gate: a view exceeding ~10 elements / ~12 relationships / ~10 dynamic steps must be split or authored as a styled `flowchart`, never Mermaid C4.
- Every element typed + described; every container/component technology-labelled; every relationship directed, labelled, protocol-labelled across containers.
- Render **and visually inspect a screenshot** of every diagram — console-clean is not the readability bar; crossing lines or overlapping labels fail review.
- File artifacts in the task dir (narrative ≤200 lines); record each boundary/technology decision as a linked ADR; promotion is deliberate and ASK-gated when contract-touching.

# Resources

- Read `resources/authoring-workflow.md` when executing — the 8-step procedure, inputs/outputs, checklists, escalation, examples, failure modes.
- Read `resources/c4-levels.md` when choosing abstraction levels.
- Read `resources/notation-and-review-checklist.md` before finalizing a diagram.
- Read `resources/mermaid-c4-syntax.md` when writing `.mmd` (syntax + gotchas).
- Copy starters from `templates/`; study the neutral idiom in `examples/`.

# How to use

## What it does

Turns a big issue — an epic, cross-tier feature, or incident review — into C4 documentation: minimal Mermaid diagrams plus a ≤200-line narrative, grounded in the project's real repos, rendered and visually reviewed, decisions captured as ADRs.

## When to use it

- Someone asks to "document the architecture" or "draw the system context / container diagram" for a feature or epic.
- A cross-tier change needs a durable structural picture, or a post-mortem needs a numbered dynamic flow.

NOT for viewing an existing `.mmd`, decision-only records (ADR), state machines/ERDs, or single-file changes.

## How to invoke

```
Skill(skill: "core:c4-architecture-docs")
```

Invoke once you know the issue scope and affected repos; the skill reads the rest from project configuration.

## Worked example

Epic "persist per-order event history for replay" touching the API server and DB: scope, pick L1+L2 plus one L3 for the API server, inventory real containers, fill the starter skeletons, write the narrative, render and inspect each diagram, and record the append-only-log vs snapshot-table choice as an ADR — yielding `reports/{architecture.md, c4-l1-context.mmd, c4-l2-container.mmd, c4-l3-api-server.mmd, adr-001-event-store.md}` plus rendered `.html`.
