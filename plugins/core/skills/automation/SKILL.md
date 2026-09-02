---
name: automation
description: "Convert an operational runbook (pod restarts, cache flushes, scaling) into a parameterized idempotent script with safety gates, or design a chaos experiment. NOT for CI/CD or deployment manifests (use deployment)."
version: "1.0.0"
owner: "swarmery-core"
allowed-tools: Read, Write, Bash
docs:
  status: reviewed
  source_sha: d77d5330fbd0
  updated: 2026-08-06
---

# Purpose

Convert manual runbooks into parameterized, idempotent scripts with safety gates, optionally graduating them to self-healing controllers, and design chaos experiments against the project's cloud infrastructure (`.claude/project.json` -> `cloud.*`) with mandatory environment guards. Output is executable shell or Python automation with dry-run, confirmation, and rollback. Examples use Kubernetes/kubectl — adapt to `cloud.runtime`.

# Rules (never violate)

- Zero hardcoded namespaces, deployment names, hostnames, or credentials — everything is a parameter or environment variable.
- Every script: `set -euo pipefail` (or equivalent), a `--dry-run` flag skipping all destructive operations, timestamped logging, and a rollback per destructive step.
- Chaos experiments require `ALLOW_CHAOS=true` and reject any namespace containing "prod"; never against production without explicit written confirmation.
- Scripts go to `devops/scripts/<runbook-name>.sh` (or `.py`) via the Write tool — never echoed through Bash — and are PR-reviewed before cluster execution.
- STOP and ask on ambiguous runbooks, PVC/StatefulSet data deletion, or restart-loop risk; refuse credential rotation without a secrets manager.

# Resources

- Read `resources/script-requirements.md` when producing a script — procedure, safety rules, self-check, common mistakes, escalation, failure modes, toil measurement.
- Read `resources/example-scripts.md` for full reference scripts — a gated restart runbook and a guarded chaos pod-kill.

# How to use

## What it does

Turns a manual operations runbook — restart a deployment, flush a cache, scale a service — into a parameterized, idempotent script reviewable in a pull request before anyone runs it against a cluster. It can also produce a guarded chaos experiment. Every script takes its targets as parameters, supports `--dry-run`, logs with timestamps, and carries a rollback.

## When to use it

- A manual runbook runs more than twice a week with stable steps.
- The task restarts pods, flushes caches, scales deployments, or rotates secrets.
- You want a guarded chaos experiment, or a toil-reduction analysis of an operational area.

Not for CI/CD pipelines or deployment manifests (the project's infra pack skills), live incident debugging (`troubleshooting`), or alert rules and dashboards (`monitoring`).

## How to invoke

```
Skill(skill: "core:automation")
```

Include the runbook description plus `runbook_name`, `target_namespace`, `target_deployment`, and `automation_level` (`script`, `self-healing`, or `chaos`). Ambiguous steps make the skill stop and ask before writing.

## Worked example

```
Skill(skill: "core:automation")
"Automate our restart runbook: check gateway logs, restart the
 deployment, verify it comes back. Namespace orders, deployment
 line-items-api, automation_level: script."
```

You get `devops/scripts/restart-line-items-api.sh`: required `--namespace`/`--deployment` flags, a `--dry-run` flag exiting before any change, a client-side dry run and `y/N` prompt ahead of the restart, rollout status with a timeout, and `kubectl rollout undo` documented as rollback — plus the dry-run transcript.

## Related

- `code-standards` — script style; `code-quality` — maintainability; `api-integration` — runbook steps calling the app's API.
