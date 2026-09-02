---
name: sre-operations
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill for SRE work — SLO definition, incident response, capacity planning, toil reduction — with human gates on every destructive operation. Don't use it for application debugging (troubleshooting skill) or metrics wiring (monitoring skill)."
disable-model-invocation: true
color: orange
docs:
  status: draft
  updated: 2026-09-01
---

# Purpose

Operate production responsibly: four SRE workflows with the same two
invariants — evidence before action, and a human gate before anything
destructive (rollbacks, restarts, scaling, config changes in shared envs).

# The four workflows

- **SLO definition** — user journeys → 3–5 SLIs (availability, latency
  p95/p99, throughput, correctness) → targets with error budgets (never
  100%) → instrumented metrics → alerts, each linked to a runbook (an alert
  without a runbook is incomplete).
- **Incident response** — classify severity; check the last 24h of deploys
  and the error logs in parallel; **human gate** before mitigation (fix or
  rollback); document the timeline; blameless post-mortem (5 Whys) within
  48h.
- **Capacity planning** — measure current utilization, analyze growth trends,
  forecast 3–12 months with confidence levels, recommend horizontal vs
  vertical with cost.
- **Toil reduction** — inventory operational tasks by frequency × duration ×
  automatability; automate the top items as scripts with safety checks and
  rollback (the `automation` skill carries the script standards).

Platform specifics (runtime CLI, env aliases) come from project.json →
`cloud`; never hard-code providers. Artifacts go to
`{task-dir}/sre/{action}-{target}.md` when a task dir is in play. Details per
workflow: `resources/workflows.md`.

# How to use

## What it does

Carries the SRE operating discipline: SLOs with error budgets and runbook-linked alerts, gated incident response with blameless post-mortems, evidence-based capacity forecasts, and prioritized toil automation — with humans approving every destructive step.

## When to use it

When the task is operating the system rather than changing its code: defining reliability targets, responding to an incident, planning capacity, or automating recurring ops chores.

## How to invoke

Load the skill and name the workflow and target: "define SLOs for the ingest service", "incident: API 5xx spike since 14:00", "capacity review for the DB". `@core:tech-lead` routes production-risk escalations here.

## Worked example

"Incident: dashboard 502s since the 10:20 deploy" — it classifies SEV-2, pulls the deploy diff and error logs in parallel, proposes rollback as mitigation and **stops for approval**, executes after the human confirms, then writes the timeline and a post-mortem draft with the 5-Whys chain to `sre/incident-dashboard-502.md`.
