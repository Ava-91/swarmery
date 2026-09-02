# Self-Check, Common Mistakes, and Failure Modes

## Self-check

- [ ] Log entries are JSON-structured with at minimum: `timestamp`, `level`, `service`, `message`.
- [ ] No PII, passwords, tokens, or secrets appear in log messages.
- [ ] No hardcoded `device_id` values -- all use runtime variables.
- [ ] `trace_id` is included in log entries when tracing is active.
- [ ] OpenTelemetry spans have meaningful names and domain-specific attributes.
- [ ] Trace context is propagated across service boundaries (WebSocket, HTTP).
- [ ] High-frequency events (per-message at several Hz per device) are logged at DEBUG level, not INFO.
- [ ] Logger type hints are specific (Literal type or enum, not bare `str` for level).
- [ ] Output stays within length budget (60 lines logger, 40 lines tracing, 20 lines correlation).

## Common mistakes

- **Logging PII or secrets.** Never log passwords, tokens, API keys, or personal data. Review log context fields before adding them.
- **Hardcoded device_id in log statements.** Always use a runtime variable. Copying `device_id="d1"` masks issues with other devices.
- **High-cardinality trace attributes.** Do not use request body content, user input, or unbounded IDs as span attributes -- these cause storage explosion in the tracing backend.
- **Missing trace context propagation.** If `<device>` sends telemetry to `<mainApp>` without injecting trace context, the trace breaks at the service boundary and correlation is lost.
- **Logging at INFO for every message.** At 5Hz per device with 10 devices, that is 50 log lines per second. Use DEBUG for per-message logging; use INFO for aggregated summaries or state changes.
- **Confusing observability with monitoring.** This skill handles logging and tracing instrumentation. For Prometheus metrics, dashboards, and alerts, use `monitoring`.

## Escalation

- **Unsure which log aggregation backend is deployed**: check the project's infrastructure repo for Filebeat, Loki, or Cloud Logging configuration. If unclear, ask the user.
- **OpenTelemetry collector not deployed**: tracing instrumentation requires a collector in the cluster. If the project's deploy/charts repo does not have an OTel collector chart, flag it as a prerequisite and stop.
- **Trace context propagation across WebSocket**: if the WebSocket protocol between `<device>` and `<mainApp>` does not support header injection, escalate for protocol design discussion.
- **OTel SDK version mismatch**: check the installed OTel SDK version in `requirements.txt` or `package.json` before writing instrumentation. If the version differs from the examples, adapt the API usage.

## Examples

<example title="Adding structured logging to a new API route in the main app">
```typescript
// src/app/api/orders/[id]/submit/route.ts
import { logger } from '@/lib/logger';
import { auth } from '@/lib/auth';
import { getDb } from '@/lib/db';
import { orders } from '@/lib/db/schema';
import { eq } from 'drizzle-orm';

export const dynamic = 'force-dynamic';

export async function POST(
  req: Request,
  { params }: { params: Promise<{ id: string }> }
): Promise<Response> {
  const session = await auth();
  if (!session) return Response.json({ error: 'Unauthorized' }, { status: 401 });

  const { id } = await params;
  logger.info('Order submission requested', { orderId: id, userId: session.user?.id });

  const [order] = await getDb()
    .select()
    .from(orders)
    .where(eq(orders.id, Number(id)));

  if (!order) {
    logger.warn('Order not found', { orderId: id });
    return Response.json({ error: 'Not found' }, { status: 404 });
  }

  // ... submit order logic

  logger.info('Order submitted', { orderId: id });
  return Response.json({ status: 'submitted' });
}
```
</example>

<example title="Correlating a slow processing trace">
1. Prometheus alert fires: processing latency p95 > 500ms.
2. Query tracing backend: find traces where `device.id={deviceId}` and duration > 500ms.
3. Examine spans: `receive_message` (5ms) -> `send_websocket` (2ms) -> `persist_telemetry` (480ms).
4. The `persist_telemetry` span is the bottleneck.
5. Search logs: `trace_id={traceId}` reveals "connection pool exhausted, waiting for available connection".
6. Root cause: PostgreSQL connection pool size too small for the write rate.
</example>

## Failure modes

| Failure | Symptom | Recovery |
|---------|---------|----------|
| Logs not JSON-structured | Log aggregation backend cannot parse fields | Update logger to emit JSON; check for `console.log(string)` calls |
| Trace breaks at service boundary | Spans appear as separate traces | Add trace context injection to WebSocket message headers/payload |
| PII in log output | Compliance violation | Audit log context fields; remove PII; add a lint rule if possible |
| Log volume too high | Storage costs spike, log search is slow | Reduce INFO-level logging; move per-message logs to DEBUG |
| OTel collector not deployed | Spans are generated but never exported | Deploy an OTel collector via the project's deploy/charts repo; or use stdout exporter for development |

## Related skills

- `monitoring` -- Prometheus metrics, Grafana dashboards, and alert rules. Use monitoring for dashboards; use observability for logging and tracing.
- `code-standards` -- coding standards including type safety for logger parameters.
- `api-integration` -- API route handler patterns where logging is typically added.
- `troubleshooting` -- diagnosing a live failure rather than instrumenting code ahead of time.
- The project's deployment workflow / infra-pack skills when enabled -- deploying OTel collectors and log shipping sidecars.
