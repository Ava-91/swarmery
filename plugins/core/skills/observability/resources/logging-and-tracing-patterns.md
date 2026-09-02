# Logging and Tracing Instrumentation Patterns

**Placeholders:** `<mainApp>` = `project.json → mainApp`, `<device>` = `project.json → device` (the edge/device service, if any). Repos and layout come from `project.json → repos`.

## Required environment

- Access to the service repo(s) being instrumented -- `<mainApp>` (TypeScript) and/or `<device>` (Python 3.11+).
- OpenTelemetry SDK for the target language (if adding tracing). Check the project's deploy/charts repo for the deployed OTel collector version and match SDK version accordingly.

## Inputs

| Input | Required | Description |
|-------|----------|-------------|
| Target service | Yes | Which service to instrument (`<mainApp>`, `<device>`) |
| Instrumentation goal | Yes | Structured logging, distributed tracing, or correlation for debugging |
| Existing logging setup | No | Current logger configuration (if any) to extend rather than replace |

## Outputs

**Length budget:** Logger wrapper code should not exceed 60 lines. Tracing setup should not exceed 40 lines. Correlation guide should not exceed 20 lines.

Deliverables:
- Structured logger configuration or wrapper code.
- OpenTelemetry tracer setup and span instrumentation code.
- Correlation guide: how to find logs for a given trace ID.
- Updated code files with instrumentation added.

## Step 1: Define the structured log format

All the project's services emit JSON logs with these fields:

```json
{
  "timestamp": "2026-05-24T10:30:00.123Z",
  "level": "INFO",
  "service": "main-app",
  "trace_id": "abc123def456",
  "span_id": "789ghi",
  "device_id": "variable-from-runtime",
  "message": "Telemetry received from device",
  "latency_ms": 45
}
```

Required fields: `timestamp`, `level`, `service`, `message`.
Recommended fields: `trace_id`, `span_id` (for correlation), domain context fields (e.g. `device_id`, `job_id` -- see `project.json → domainTerms`).

**Checkpoint:** Log format reviewed. All four required fields present. No PII or secrets in proposed context fields.

## Step 2: Implement structured logging

**`<device>` (Python):**
```python
import logging
import json
from datetime import datetime, timezone
from typing import Literal

LogLevel = Literal["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]

class StructuredLogger:
    def __init__(self, service: str) -> None:
        self.service = service
        self.logger = logging.getLogger(service)

    def log(self, level: LogLevel, message: str, **context: str | int | float | bool) -> None:
        log_entry = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "level": level,
            "service": self.service,
            "message": message,
            **context,
        }
        self.logger.log(getattr(logging, level), json.dumps(log_entry))

    def info(self, message: str, **context: str | int | float | bool) -> None:
        self.log("INFO", message, **context)

    def error(self, message: str, **context: str | int | float | bool) -> None:
        self.log("ERROR", message, **context)

logger = StructuredLogger("device-service")

# Usage -- device_id is a runtime variable, never hardcoded
logger.info("Telemetry received", device_id=device_id, latency_ms=45)
```

**`<mainApp>` (TypeScript):**
```typescript
// src/lib/logger.ts
interface LogContext {
  traceId?: string;
  deviceId?: string;
  jobId?: string;
  latencyMs?: number;
  [key: string]: string | number | boolean | undefined;
}

type LogLevel = 'DEBUG' | 'INFO' | 'WARNING' | 'ERROR';

function log(level: LogLevel, message: string, context: LogContext = {}): void {
  const entry = {
    timestamp: new Date().toISOString(),
    level,
    service: 'main-app',
    message,
    ...context,
  };
  console.log(JSON.stringify(entry));
}

export const logger = {
  info: (message: string, context?: LogContext) => log('INFO', message, context),
  error: (message: string, context?: LogContext) => log('ERROR', message, context),
  warn: (message: string, context?: LogContext) => log('WARNING', message, context),
  debug: (message: string, context?: LogContext) => log('DEBUG', message, context),
};
```

**Checkpoint:** Logger code compiles/lints. No PII in context fields. High-frequency events (per-message at several Hz per device) use DEBUG level, not INFO.

## Step 3: Add distributed tracing with OpenTelemetry

**`<device>` (Python):**
```python
from opentelemetry import trace

tracer = trace.get_tracer("device-service")

async def process_device_message(device_id: str, message: bytes) -> None:
    with tracer.start_as_current_span("receive_message") as span:
        span.set_attribute("device.id", device_id)
        span.set_attribute("protocol.version", "2")
        telemetry = parse_message(message)

        with tracer.start_as_current_span("send_websocket"):
            await send_websocket(device_id, telemetry)
```

**Trace flow across services:**
```
Trace ID: {trace_id}

Span 1: device-service receives message (5ms)
  +-- Span 2: device-service sends WebSocket (2ms)
      +-- Span 3: main-app receives telemetry (10ms)
          +-- Span 4: main-app persists via the ORM -> PostgreSQL (15ms)
          +-- Span 5: main-app broadcasts to browser via SSE (3ms)

Total: 35ms
```

**Checkpoint:** Span names are meaningful (not generic). Span attributes use domain-specific keys (`device.id`, not `id`). No unbounded attributes (no request bodies or user input as span attributes).

## Step 4: Propagate trace context across service boundaries

When `<device>` sends a WebSocket message to `<mainApp>`, inject the trace context into the message headers or payload so `<mainApp>` can continue the same trace.

**Checkpoint:** Trace context injection code added at every service boundary (WebSocket, HTTP). Verified that `<mainApp>` extracts the context on the receiving side.

## Step 5: Correlate logs, traces, and metrics for debugging

When investigating a latency issue:
1. Start from the metric alert (e.g., processing latency p95 > 500ms) -- this comes from the `monitoring` skill's Prometheus alerts.
2. Find traces with duration > 500ms in the tracing backend.
3. Identify the slow span (e.g., database save took 400ms).
4. Search logs by the same `trace_id` to find error or context messages.
5. Root cause: e.g., database connection pool exhausted.

**Checkpoint:** Correlation path documented. User can follow trace_id from alert to logs.
