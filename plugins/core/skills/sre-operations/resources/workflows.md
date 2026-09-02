# SRE workflows — full detail

## SLO definition

1. Identify the user journeys for the target service.
2. Define 3–5 SLIs: availability, latency p95/p99, throughput, correctness.
3. Set SLO targets with error budgets — never aim for 100%; the budget is
   what makes change velocity negotiable.
4. Instrument the SLIs as metrics (the `monitoring` skill owns
   metric/dashboard/alert mechanics).
5. Create alerts, each linked to a runbook. An alert without a runbook is
   incomplete by definition.

## Incident response

1. Acknowledge and classify severity (SEV-1 user-facing outage → SEV-4
   cosmetic).
2. Gather evidence in parallel: deployments in the last 24h, error logs,
   relevant dashboards. Recent deploys are the leading suspect but not the
   verdict — confirm the mechanism before acting on it.
3. **Human gate**: mitigation (fix forward or rollback) requires explicit
   user confirmation. Present the option, the blast radius, and the rollback
   of the rollback.
4. Document the timeline as it happens (`T+mm:ss — event`).
5. Blameless post-mortem within 48h: 5-Whys root cause, contributing factors,
   action items with owners.

Context hygiene: after gathering logs/metrics, compact findings into a short
timeline before starting root-cause analysis.

## Capacity planning

1. Measure current CPU, memory, disk, network utilization.
2. Analyze growth trends from metrics history.
3. Forecast 3–12 month needs with confidence levels (HIGH/MEDIUM/LOW and the
   basis).
4. Recommend scaling strategy — horizontal vs vertical — with cost deltas.

## Toil reduction

1. Inventory operational tasks: frequency, duration, automatable (yes/no).
2. Prioritize by impact = time saved × frequency.
3. Automate top items as parameterized, idempotent scripts with safety gates
   and rollback — the `automation` skill carries the script standards.

## Invariants

- Destructive operations (rollbacks, restarts, scaling, shared-env config)
  always pause for human confirmation — also inside autonomous runs.
- Evidence before action: no mitigation on an unconfirmed hypothesis.
- Platform CLI and env names from project.json → `cloud` (`cloud.runtime`,
  `cloud.envAlias`); never assume a provider.
- Artifacts: `{task-dir}/sre/{action}-{target}.md` — assessment, decisions,
  timeline, follow-ups.
