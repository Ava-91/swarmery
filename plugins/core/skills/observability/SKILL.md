---
name: observability
description: "Structured logging and OpenTelemetry distributed tracing, incl. log-trace-metric correlation. NOT for metrics/dashboards/alerts (belong to monitoring) and NOT for Helm health probes."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: 3c7676ebd024
  updated: 2026-08-06
---

# Purpose

Instrument the project's services with structured logging and OpenTelemetry distributed tracing, and provide the patterns for correlating logs, traces, and metrics when diagnosing issues. Code-level instrumentation only — what to log, how to propagate trace context, how to correlate signals. Placeholders `<mainApp>` and `<device>` resolve from `project.json`.

# Rules

- Logs are JSON with required fields `timestamp`, `level`, `service`, `message` — plus `trace_id`/`span_id` when tracing is active.
- NEVER log PII, passwords, tokens, API keys, or secrets; review every context field.
- High-frequency per-message events (several Hz per device) log at DEBUG, never INFO; INFO is for summaries and state changes.
- Span names are meaningful with domain-specific attribute keys (`device.id`, not `id`); no unbounded span attributes (request bodies, user input).
- Propagate trace context at every service boundary (WebSocket, HTTP) — a missing injection breaks the trace and loses correlation.
- Logging and tracing only: metrics, dashboards, and alerts belong to `monitoring`; Helm health probes and log routing belong to the project's infra pack skills.

# Resources

- Read `resources/logging-and-tracing-patterns.md` when instrumenting — the log format, Python/TypeScript logger wrappers, OTel span code, context propagation, the five-step correlation path, inputs/outputs and length budgets.
- Read `resources/checks-and-failure-modes.md` before returning or when something misbehaves — self-check list, common mistakes, escalation triggers, worked examples, failure modes, related skills.

# How to use

## What it does

Adds structured logging and OpenTelemetry tracing to a service and shows how to follow one request across services: which fields every log line carries, how to name spans, how to pass trace context over a boundary, and how to jump from a slow trace to the log lines explaining it.

## When to use it

- Adding logging to a service and wanting a JSON format a log backend can parse.
- Instrumenting with OTel spans, or a trace breaks mid-flow and needs context propagation.
- Going from a trace ID to the log lines for that same request.
- Reviewing log statements for leaked personal data, tokens, or secrets. (Metric/dashboard/alert work starts with `monitoring` instead.)

## How to invoke

```
Skill(skill: "core:observability")
```

Invoke before writing instrumentation code. Required inputs: target service and instrumentation goal (logging, tracing, or correlation). Optional: your existing logger setup, so it is extended rather than replaced.

## Worked example

```
Skill(skill: "core:observability")
Add structured logging to the order submit route in apps/<mainApp>.
```

The skill defines the JSON log shape (`timestamp`, `level`, `service`, `message`, plus `trace_id` and domain fields), writes a typed `logger` wrapper, and adds `logger.info` / `logger.warn` calls to the `orders/line-items` route — request received, not found, submitted. You end up with parseable logs carrying an order ID and no personal data, plus a trace ID you can paste into log search.
