---
name: monitoring
description: "Prometheus metrics, Grafana dashboards, alert rules, ServiceMonitor wiring, and endpoint instrumentation. NOT for logs/traces (belong to observability) and NOT for Helm health probes."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: 8d2277869557
  updated: 2026-08-06
---

# Purpose

Define, instrument, and verify Prometheus metrics, Grafana dashboards, and alert rules across the project's platform. Produce metric definitions, dashboard JSON, alert rule YAML, and ServiceMonitor resources that connect instrumentation to Prometheus scraping. Placeholders `<mainApp>`, `<device>`, and `<project>` (metric prefix) resolve from `project.json`.

# Rules

- Metric names follow `<project>_{subsystem}_{unit}_{suffix}` (e.g. `myproject_telemetry_latency_seconds`).
- Never hardcode label values like `device_id` — labels use runtime variables only.
- Every alert rule MUST carry a `for:` duration, or it fires on single-sample spikes.
- Label cardinality stays bounded — never request paths, user IDs, or trace IDs as labels.
- Metrics without ServiceMonitor/scrape wiring are never collected — always wire the scrape path and run `helm lint` on chart changes.
- Metrics, dashboards, and alerts only: "add logging/tracing" belongs to `observability`; Helm health probes belong to the project's infra pack skills.

# Resources

- Read `resources/instrumentation-procedure.md` when instrumenting a service — environment, inputs/outputs, and the six-step procedure with TypeScript/Python code, ServiceMonitor YAML, alert rules, and verification checkpoints.
- Read `resources/promql-and-alerts-reference.md` when writing panels or alerts, or when something misbehaves — golden-signals PromQL, self-check, common mistakes, escalation, examples, failure modes.

# How to use

## What it does

Covers Prometheus metrics work end to end: picking metric type and name, instrumenting service code in TypeScript or Python, wiring the scrape path so Prometheus actually collects the series, writing alert rules that survive transient spikes, building Grafana panels, and verifying the metric exists before calling the work done.

## When to use it

- Adding a counter, histogram, or gauge and wanting naming and label rules right the first time.
- A metric is defined in code but never shows up in Prometheus — suspect scrape wiring.
- New alert rules with consistent thresholds, `for:` durations, and severities; or golden-signal PromQL for a dashboard.
- Investigation starting from a Prometheus alert (log/trace-first investigations start with `observability`).

## How to invoke

```
Skill(skill: "core:monitoring")
```

Invoke before writing the first metric line; follow the six steps in order, clearing each checkpoint. Required inputs: target service and metric intent. Optional: alert thresholds (SLOs) and an existing dashboard path to extend.

## Worked example

```
Skill(skill: "core:monitoring")

"Add a histogram for order line-item processing latency in orders/line-items,
alert when p95 goes above 500 ms."
```

You get a `Histogram` with explicit buckets and bounded labels, a ServiceMonitor pointing at the metrics path, an alert with `expr: histogram_quantile(0.95, ...) > 0.5` and a `for: 5m` guard, one Grafana latency panel, plus verification evidence: a `helm lint` pass and curl output showing the new `_bucket` series.
