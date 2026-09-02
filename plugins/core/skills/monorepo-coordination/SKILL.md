---
name: monorepo-coordination
description: "Coordinate changes spanning 2+ repos of a multi-repo workspace or 2+ apps/packages of a monorepo -- merge-order plans, MR/PR templates, post-merge checklists. NOT for single-repo changes, even large ones."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: d9709fababf4
  updated: 2026-08-06
---

# Purpose

Plan and sequence changes that span multiple repositories (or multiple monorepo apps/packages) so they land in the correct order, with CI-probe-enforced operator gates and post-merge validation of the whole system. Done when every repo is phased, each MR/PR carries dependency arrows and failure modes, and every operator step has a probe.

# Rules

- Read the repo shape from `${CLAUDE_PROJECT_DIR}/.claude/project.json` (`repos`, `monorepo`) before planning — the phase model is identical for both shapes; only merge mechanics differ.
- Merge order follows the phase model: 1 Foundation → 2 Operator action → 3 Wire consumers → 4 Consume. Never wire before foundation.
- Every operator step gets a CI probe that fails if skipped — MR prose alone is never a gate.
- When a contracted cross-service boundary changes, read the living contract document FIRST (see `api-contract`) and place its update no later than the emitter change.
- Each MR is individually revert-safe; its description carries Depends on, Blocks, Operator steps, and Failure mode if merged out of order.
- Use durable identifiers (file paths), never transient MR numbers; immutable digests, never mutable tags, in cross-repo handoffs.

# Resources

- Read `resources/phase-model-and-procedure.md` when building the plan — repo-shape detection, when (not) to use, inputs/outputs, the output template, and the six-step procedure with MR and CI-probe boilerplate.
- Read `resources/checks-and-examples.md` before returning — self-check, common mistakes, escalation triggers, worked scenarios, failure modes, related skills.

# How to use

## What it does

Plans the landing order for a change touching multiple repositories — or multiple independently deployed monorepo apps: a phased merge order, MR/PR descriptions spelling out dependencies, CI probes that fail when a manual step was skipped, and a post-merge checklist.

## When to use it

- A change spans two or more repos, or monorepo packages that deploy independently.
- A runtime env var seeded in one place, wired in another, read by the app.
- A message-format change crosses a contracted device↔app boundary.
- Not for single-repo changes (`refactor-plan`), hotfixes, dependency bumps, or image promotion.

## How to invoke

```
Skill(skill: "core:monorepo-coordination")
```

Provide the change description and affected repos/packages (required); operator steps and existing MR links optional. Output is a plan capped at ~200 lines — nothing is merged or pushed.

## Worked example

```
Change: add MAPS_API_KEY as a runtime env var. Repos: infrastructure,
deploy charts, main app. Operator step: run the secret bootstrap script.
```

The plan: seed the secret in the infrastructure repo, run the bootstrap script, wire the value into the chart with a placeholder guard, then read it in the app. Phase 2 gets a CI probe failing while the cluster secret contains `CHANGE_ME`; validation closes with a browser check that the value reaches the client.
