---
name: troubleshooting
description: "Debug a specific failure, investigate an incident, analyze error logs, or diagnose connectivity on the project's platform. NOT for proactive instrumentation (use monitoring/observability) or writing tests (use testing)."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: generated
  source_sha: 268250259d13
  updated: 2026-08-06
---

# Purpose

Diagnose and resolve operational issues on the project's platform (see the consumer project's `CLAUDE.md` and `.claude/project.json` → `domainTerms.product` for what the platform is). Covers device connectivity, telemetry streaming, database migrations, image pull failures, performance degradation, and CI/CD deploy-path failures. Provides structured incident response with severity classification and postmortem documentation.

**Placeholders used below:** `<mainApp>` = `project.json → mainApp`, `<device>` = `project.json → device` (the device/edge service, if the project has one), `<envAlias>` = `project.json → cloud.envAlias` (the staging environment alias used in slash-command names).

**Write tool scope:** Write is used only for creating postmortem documents and diagnostic reports. Never use Write to modify source code, deployment values, or manifests during an incident.

# When to use

- User reports an issue: "devices not connecting," "telemetry not showing," "deploy failed."
- User asks to "debug," "investigate," "diagnose," or "fix" an operational problem.
- CI pipeline has failed and user needs root cause analysis.
- User asks to "analyze logs" for any of the project's services.
- An incident is in progress and triage/resolution is needed.

**Disambiguation -- troubleshooting vs monitoring vs observability:** This skill is for reactive debugging of a specific failure. For proactive instrumentation (adding metrics, logging, tracing), use `monitoring` or `observability`. If the investigation requires reading Prometheus metrics, this skill consumes those outputs but does not create new metrics.

# When NOT to use

- **Proactive security hardening** -- use `security-audit`.
- **Writing tests to reproduce bugs** -- use `testing`.
- **CI pipeline configuration changes** (.gitlab-ci.yml edits) -- use `deployment`.
- **Deployment config value changes** -- use `deployment`.
- **Feature implementation** -- use the appropriate implementation skill.
- **Adding metrics, logging, or tracing** -- use `monitoring` or `observability`.

# Required environment (.claude/skills/troubleshooting/SKILL.md)

- Cluster access to the target environment (or SSH access to the environment's VM)
- Read access to service logs
- The cloud provider's CLI for cloud operations (secret management, firewall rules) -- see `project.json → cloud.provider`

## Environment variables

The following values are environment-specific. Replace with actual values for your target environment:

| Variable | Default (staging -- `project.json → cloud.envAlias`) | Description |
|----------|-------------------|-------------|
| `INGRESS_DOMAIN` | `d16.local` | Ingress hostname for health checks |
| `DEFAULT_NAMESPACE` | `default` | Default namespace |
| `REGISTRY_HOST` | `<region>-docker.pkg.dev` | Container registry host (region from `project.json → cloud.region`) |
| `DEVICE_SUBDOMAIN_PATTERN` | `d{N}.${INGRESS_DOMAIN}` | Per-device endpoint pattern |
| `FLEET_SIZE` | `3` | Number of devices checked by diagnose.sh |

# Inputs

| Input | Required | Description |
|-------|----------|-------------|
| Symptom | Yes | What is the user observing? (error message, unexpected behavior, missing data) |
| Environment | No | Which environment: localdev, staging (`<envAlias>`), production. Default: inferred from context |
| Severity | No | P0-P3. If not provided, triage step will determine it |

# Outputs

**Length budget:** Diagnosis should not exceed 80 lines. Postmortem should not exceed the template (approximately 30 lines). Resolution commands should be listed, not narrated.

Deliverables:
- **Diagnosis:** Root cause identification with evidence (log excerpts, command output). Mark inferred causes with `[SUSPECTED]` prefix.
- **Resolution:** Commands executed or recommended to fix the issue.
- **Postmortem:** (for P0/P1 incidents) Structured postmortem document using the template in Step 5.

# Procedure

## Step 1: Search known issues reference

Use Bash to grep `resources/common-issues.md` (bundled with this skill) for keywords from the symptom before loading the full file:

```bash
grep -i "<keyword-from-symptom>" resources/common-issues.md
```

If the grep returns a match, read only the matching section. If no match, proceed to Step 2 without loading the full file.

**Checkpoint:** Known issues checked. Either a matching pattern was found (apply its verified solution) or no match (proceed to fresh diagnosis).

## Step 2: Triage

Use the diagnostic decision tree to route to the correct workflow:

1. Something is wrong with the staging environment -> `/<envAlias>-health` (snapshot first)
2. CI pipeline failed -> `/ci-diagnose <pipeline-id>`
3. Need to SSH into the VM -> `/<envAlias>-ssh [cmd]`
4. Need sustained cluster access -> `/<envAlias>-kubectl`
5. Missing/stale bootstrap secret -> `/bootstrap-<envAlias>-secrets`
6. Cross-repo drift suspected -> `/check-versions`
7. Multi-step recovery needed -> document current state and escalate to the on-call engineer with a summary of findings and the specific recovery steps needed

**Severity levels:**

| Level | Definition | Response |
|-------|-----------|----------|
| P0 | Complete outage, data loss | Immediate resolution; all hands |
| P1 | Major functionality broken | Resolve within hours |
| P2 | Minor functionality broken | Resolve within days |
| P3 | Cosmetic issue | Resolve in next sprint |

**Checkpoint:** Severity assigned. Routing decision made. If P0, proceed immediately.

## Step 3: Diagnose

### Run platform diagnostics
```bash
# Bundled diagnostic script -- covers pods, events, ingress, health, resources
scripts/diagnose.sh [namespace]
# Default namespace: value of DEFAULT_NAMESPACE (see Environment Variables)
```

**`diagnose.sh` interface:**
- **Input:** Optional namespace argument (default: `${DEFAULT_NAMESPACE}`)
- **Output:** Pod status, recent events (last 10), ingress rules, services, health endpoint responses, resource usage
- **Known limitation:** Health check probes devices 1-`${FLEET_SIZE}` only; domain names use `${INGRESS_DOMAIN}`

### Common diagnostic patterns

#### Device not connecting
**Symptoms:** device shows as "disconnected," no telemetry, device heartbeat missing
```bash
kubectl logs -n <device-ns> deployment/<device> --tail=100
kubectl get pods -n <device-ns>
kubectl exec -n <device-ns> deployment/<device> -- \
  python -c "import <device_protocol_lib>  # verify the device protocol connection opens"
```

**Solutions:**
- Pod crashed: `kubectl rollout restart deployment/<device> -n <device-ns>` + check `--previous` logs
- Hardware: fix serial-device permissions (e.g., `sudo chmod 666 /dev/ttyAMA0`) on the device
- Wrong serial settings: edit the device configmap (`<device>-config`), set the baud-rate value, restart

#### Telemetry not appearing in UI
**Symptoms:** device connected but no data in dashboard
```bash
kubectl logs -n <app-ns> deployment/<mainApp> --tail=100
# Replace ${INGRESS_DOMAIN} with your environment's hostname
curl -N "http://${INGRESS_DOMAIN}/api/telemetry/stream?deviceId=d1"
kubectl exec -n <data-ns> deployment/postgresql -- \
  psql -U postgres -d backend -c "SELECT * FROM telemetry ORDER BY timestamp DESC LIMIT 10;"
```

**Solutions:**
- SSE not connected: Check browser Network -> EventStream tab
- Upstream WS not connecting: `kubectl exec -n <app-ns> deployment/<mainApp> -- wget -qO- http://d1.${INGRESS_DOMAIN}/health`
- Restart: `kubectl rollout restart deployment/<mainApp> -n <app-ns>`

#### High latency / slow performance
```bash
kubectl top pods -n <device-ns> && kubectl top pods -n <app-ns> && kubectl top nodes
```
- CPU/Memory saturation: Adjust resource limits; scale replicas.
- Slow DB queries: Add missing indexes (e.g., `CREATE INDEX idx_telemetry_device_timestamp ON telemetry(device_id, timestamp DESC)`).

#### Image pull errors (ImagePullBackOff)
```bash
# Expired cloud registry token -- replace ${REGISTRY_HOST} with your registry
gcloud auth login
gcloud auth configure-docker ${REGISTRY_HOST}
cd <infrastructure-repo> && . files/dockerSecret.sh
kubectl rollout restart deployment/<device> -n <device-ns>

# Wrong image tag
kubectl get deployment -n <device-ns> <device> -o jsonpath='{.spec.template.spec.containers[0].image}'
```

#### Database migration failures
```bash
kubectl exec -n <data-ns> deployment/postgresql -- \
  psql -U postgres -d backend -c "SELECT * FROM flyway_schema_history ORDER BY installed_rank DESC LIMIT 10;"
```
**Checkpoint:** Root cause identified with log evidence. If root cause is inferred rather than confirmed by log evidence, mark with `[SUSPECTED]` prefix.

## Step 4: Resolve

```bash
# Restart pod
kubectl rollout restart deployment/<device> -n <device-ns>

# Rollback deployment
kubectl rollout undo deployment/<mainApp> -n <app-ns>

# Scale up
kubectl scale deployment/<mainApp> --replicas=3 -n <app-ns>
```

**Before executing any destructive recovery command** (`helm rollback`, `UPDATE flyway_schema_history`, `kubectl scale --replicas=0`, `kubectl rollout undo`):
1. Run `/<envAlias>-health` to snapshot current state
2. Confirm the intended action with the operator
3. Verify the current cluster state matches expectations

**Checkpoint:** Recovery action executed (or deferred pending operator confirmation). Service health verified post-recovery.

## Step 5: Document (P0/P1 incidents)

```markdown
# Incident Postmortem

**Date:** [ISO date]
**Duration:** [minutes]
**Severity:** [P0/P1]

## Summary
[Brief description of what happened]

## Timeline
- [HH:MM] - Alert triggered / issue reported
- [HH:MM] - Diagnosis started
- [HH:MM] - Root cause identified
- [HH:MM] - Fix deployed
- [HH:MM] - Resolved / verified

## Root Cause
[What caused the issue and why. Prefix with [SUSPECTED] if not confirmed by direct evidence.]

## Action Items
- [ ] [Preventive measure 1]
- [ ] [Preventive measure 2]
- [ ] [Monitoring improvement]
```

Save postmortem to the project's incident documentation directory. Do not save to source code directories.

**Checkpoint:** Postmortem written and saved. Action items assigned.

# Self-check

- [ ] `resources/common-issues.md` was grep-searched for symptom keywords before fresh diagnosis.
- [ ] Severity level was assigned (P0-P3).
- [ ] Root cause was identified with evidence (log excerpts, command output).
- [ ] Inferred root causes are marked with `[SUSPECTED]` prefix.
- [ ] Recovery commands were confirmed with the operator before executing destructive actions.
- [ ] `/<envAlias>-health` snapshot was taken before any recovery action.
- [ ] For P0/P1: postmortem document was created using the Step 5 template.
- [ ] No source code, deployment values, or manifests were modified.
- [ ] Environment-specific values use variables from the Environment Variables section, not hardcoded strings.

# Common mistakes

- **Executing destructive commands without snapshotting state first** -- always run `/<envAlias>-health` before `helm rollback`, `kubectl rollout undo`, or `UPDATE flyway_schema_history`.
- **Modifying source code during incident response** -- fix the immediate issue with operational commands; code changes go through normal PR flow.
- **Loading all of common-issues.md without searching first** -- grep for symptom keywords first, then read only the matching section.
- **Using Write to modify deployment values or manifests** -- Write is scoped to postmortem documents only during troubleshooting.
- **Reporting inferred root causes as confirmed** -- always mark uncertain diagnoses with `[SUSPECTED]` so the operator knows the confidence level.

# Escalation

- **P0 incident with no clear root cause after 30 minutes:** Escalate to the on-call engineer; provide all diagnostic output collected so far.
- **Recovery requires cluster admin access not available:** Escalate to infrastructure team with the specific commands needed.
- **Issue spans multiple services with unclear ownership:** Document current state, list the specific recovery steps needed, and escalate to the on-call engineer.

# Examples

<example title="Device telemetry not appearing">
**Symptom:** "Device d1 is connected but I see no telemetry in the dashboard"
**Process:** Grep `resources/common-issues.md` for "telemetry" -> match "Telemetry Not Appearing" -> read that section -> check main-app logs -> verify SSE stream with curl -> find upstream WS not connecting -> restart the main app -> verify telemetry appears.
**Diagnosis:** WebSocket connection between the device service and the main app was refused due to a main-app pod restart. `kubectl rollout restart` resolved.
</example>

<example title="CI deploy pipeline failure">
**Symptom:** "deploy_<envAlias> job failed"
**Process:** Grep `resources/common-issues.md` for "deploy" -> check known CI failures (P-017 through P-026 in failure taxonomy below) -> match signal in CI logs -> apply documented recovery -> verify pipeline succeeds on retry.
</example>

<example title="Image pull error after registry token expiry">
**Symptom:** "Pod stuck in ImagePullBackOff"
**Process:** Grep `resources/common-issues.md` for "ImagePullBackOff" -> match -> re-authenticate with the cloud CLI -> regenerate docker secret -> restart deployment -> verify pod starts.
**Diagnosis:** Container registry token had expired. `gcloud auth configure-docker ${REGISTRY_HOST}` + secret regeneration resolved.
</example>

# Failure modes

| Failure | Recovery |
|---------|----------|
| Cannot connect to cluster (timeout) | Check VPN/SSH connection; try `/<envAlias>-ssh` first |
| Logs are empty or truncated | Check if pod restarted; use `--previous` flag for previous container logs |
| Root cause identified but fix requires code change | Document the root cause and recommended fix; do not modify code during incident response |
| Multiple cascading failures | Prioritize by severity; fix the blocking issue first (usually: database -> backend -> frontend) |
| Unknown failure pattern | Document symptoms, diagnostic output, and what was tried; escalate to the team |

# Related skills

- `<envAlias>-operations` -- daily-ops bundle for routine operations on the staging environment (from project root: `.claude/skills/<envAlias>-operations/SKILL.md`)
- `security-audit` -- proactive vulnerability scanning (not reactive debugging)
- `testing` -- writing tests to prevent recurrence of bugs found during troubleshooting
- `deployment` -- CI pipeline configuration (not debugging pipeline failures)
- `deployment` -- deployment config changes needed after diagnosis
- `monitoring` -- Prometheus metrics and alerts that may surface symptoms
- `observability` -- structured logging and tracing used during log-based diagnosis

---

## 2026-04 failure taxonomy (CI/CD deploy path)

| ID | Signal | Root cause | Recovery |
|----|--------|-----------|----------|
| P-026 | `[ERROR] Required secret '<mainApp>-*' not found in namespace '<data-ns>'` | App-secrets bootstrap script skipped after an infrastructure-repo merge | `/bootstrap-<envAlias>-secrets` |
| P-025 | `ERROR: remote payload exited 1` with no detail | `$(ssh ... bash -s)` swallows stderr (fixed post-2026-04-20) | SSH to VM, re-run failing step manually |
| P-024 | `can't get a valid version for dependency <mainApp>` | Subchart Chart.yaml bumped; umbrella Chart.yaml/Chart.lock not updated | `bash scripts/check-chart-sync.sh` -> update -> refresh chart dependencies -> commit + push |
| P-022 | `cluster / versions drift detected -- refusing to roll back` | Deploy landed but verify failed before promote ran | SSH to VM, `helm rollback <release> -n <app-ns>` -> rerun `rollback_<envAlias>` |
| P-021 | `FAILED_PRECONDITION: Secret Version [...] is in DESTROYED state` | Secret version destroyed without replacement | `bootstrap-sm-certs.sh` (in `general/scripts/`) |
| P-017 | `Permission denied (publickey). Too many authentication failures` | ssh-agent has >=6 keys; server MaxAuthTries=6 | Add `-o IdentitiesOnly=yes -o IdentityAgent=none` or use `/<envAlias>-ssh` |

## Bundled resources

- **`resources/common-issues.md`** -- 12+ known issue patterns with verified solutions. Grep for symptom keywords before loading.
- **`scripts/diagnose.sh`** -- Usage: `scripts/diagnose.sh [namespace]`. Default: `${DEFAULT_NAMESPACE}`.
- From project root: `.claude/skills/<envAlias>-operations/SKILL.md`, `.claude/commands/` (operational slash commands).

# How to use

## What it does

This skill walks you through a live operational failure on your project's platform — a device that stopped connecting, telemetry missing from the dashboard, a pod stuck pulling an image, a failed deploy job. It searches a bundled catalogue of known issues first, assigns a severity, gathers evidence from logs and cluster state, and proposes recovery commands. It fixes things with operational commands only; it never edits source code, manifests, or deployment values.

## When to use it

- A user reports a concrete symptom: "devices are not connecting", "no telemetry in the UI", "the deploy job failed".
- A CI pipeline has failed and you need the root cause, not a guess.
- You need to read service logs and cluster state to explain unexpected behaviour.
- An incident is live and you need triage, a severity level, and a documented recovery path.

## When not to use it

- Adding metrics, structured logs, or traces — use `monitoring` or `observability`.
- Writing a test that reproduces the bug — use `testing`.
- Changing pipeline config or deployment values — use `deployment`.
- Proactive vulnerability scanning with no failure in hand — use `security-audit`.

## How to invoke

```
Skill(skill: "core:troubleshooting")
```

Invoke it as soon as the symptom is stated. It runs its own procedure — known-issue search, triage, diagnosis, resolution — so you do not need to pre-plan the steps.

## Inputs

- **Symptom** — the error message, missing data, or unexpected behaviour you observe. Required.
- **Environment** — localdev, staging (`<envAlias>`), or production. Optional; inferred from context if omitted.
- **Severity** — P0 through P3. Optional; the triage step assigns one if you do not.

## What you get back

- A diagnosis under 80 lines, with log excerpts or command output as evidence. Causes that are inferred rather than proven carry a `[SUSPECTED]` prefix, so you can see the confidence level.
- The recovery commands, listed rather than narrated. Destructive ones (rollback, undo, scaling to zero) are held for your confirmation and preceded by a health snapshot.
- For P0 and P1 incidents, a postmortem document written to your incident documentation directory — summary, timeline, root cause, action items.

## Worked example

```
Skill(skill: "core:troubleshooting")

Request: "Device d1 shows as connected but no telemetry reaches the dashboard."

What happens: the bundled known-issues file is grepped for "telemetry" and
matches "Telemetry Not Appearing" -> main-app logs are read -> the SSE stream
is probed with curl -> the upstream WebSocket is found refused -> the main app
is restarted -> telemetry is confirmed flowing again.

You end up with: root cause (the WebSocket to the main app was refused after a
pod restart), the exact `kubectl rollout restart` that fixed it, and the
verification output showing telemetry back in the stream.
```

## Related

- `deployment` — prefer it for pipeline configuration and deployment value changes, including the fix that follows a diagnosis here.
- `monitoring` — prefer it when you want alerts and metrics that surface a symptom earlier, rather than debugging one now.
- `observability` — prefer it when the gap is missing logs or traces, not a specific broken service.
- `testing` — prefer it once the root cause is known and you want a test that prevents the bug returning.
