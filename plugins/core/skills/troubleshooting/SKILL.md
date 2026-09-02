---
name: troubleshooting
description: "Debug a specific failure, investigate an incident, analyze error logs, or diagnose connectivity on the project's platform. NOT for proactive instrumentation (use monitoring/observability) or writing tests (use testing)."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: 268250259d13
  updated: 2026-08-06
---

# Purpose

Diagnose and resolve operational issues on the project's platform (`project.json → domainTerms.product`): device connectivity, telemetry streaming, migrations, image pulls, performance, CI/CD deploy-path failures. Structured incident response: known-issue search, triage with severity (P0-P3), evidence-based diagnosis, recovery, postmortem. Reactive debugging only.

# Rules (never violate)

1. Write is scoped to postmortems and diagnostic reports only — never modify source code, deployment values, or manifests during an incident.
2. Before any destructive recovery (rollback, rollout undo, scale-to-zero, schema-history edits): snapshot via `/<envAlias>-health`, then confirm with the operator.
3. Grep `resources/common-issues.md` for symptom keywords before fresh diagnosis; read only the matching section.
4. Mark inferred (not log-evidenced) root causes `[SUSPECTED]`.
5. Every P0/P1 incident gets a postmortem in the incident docs directory, never in source trees.
6. Use the environment variables (`INGRESS_DOMAIN`, `REGISTRY_HOST`, …), never hardcoded environment strings.

# Resources

- Read `resources/diagnostic-procedures.md` when working an incident: the 5-step procedure, diagnostic patterns, environment variables, severity table, postmortem template, self-check, escalation, the CI/CD failure taxonomy (P-017…P-026), and failure modes.
- Grep `resources/common-issues.md` when matching a symptom — 12+ known patterns with verified solutions; never load the whole file.
- Run `scripts/diagnose.sh [namespace]` for a cluster snapshot — pods, events, ingress, health, resources.

# How to use

## What it does

Walks you through a live operational failure — a device that stopped connecting, missing telemetry, a pod stuck pulling an image, a failed deploy job. It searches a bundled known-issues catalogue first, assigns a severity, gathers evidence from logs and cluster state, and proposes recovery commands — operational commands only, never source edits.

## When to use it

- A concrete symptom is reported: "devices not connecting", "no telemetry", "deploy failed".
- A CI pipeline failed and you need the root cause; an incident is live and needs triage.

Not for: adding metrics/logs/traces (`monitoring`/`observability`), repro tests (`testing`), pipeline or deploy config changes (the project's infra pack skills), or proactive scanning (`security-audit`).

## How to invoke

```
Skill(skill: "core:troubleshooting")
```

State the symptom (required), the environment (localdev / `<envAlias>` / production, inferred if omitted), and a severity if known — triage assigns one otherwise. You get a diagnosis under 80 lines with evidence, listed recovery commands (destructive ones held for confirmation), and a postmortem for P0/P1.

## Worked example

```
Skill(skill: "core:troubleshooting")
"Device d1 shows as connected but no telemetry reaches the dashboard."
```

Known issues are grepped for "telemetry", matching "Telemetry Not Appearing" → main-app logs read → SSE stream probed with curl → upstream WebSocket found refused → main app restarted → telemetry confirmed flowing. You get the root cause, the exact `kubectl rollout restart` that fixed it, and verification output.

## Related

The project's infra pack skills (config fixes after diagnosis), `monitoring`/`observability` (proactive instrumentation), `testing` (prevent recurrence), `<envAlias>-operations` (routine staging ops).
