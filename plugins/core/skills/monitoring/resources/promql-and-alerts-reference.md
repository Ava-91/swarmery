# PromQL Reference, Checks, and Failure Modes

## Golden signals PromQL reference

| Signal | PromQL |
|--------|--------|
| Latency (p95) | `histogram_quantile(0.95, rate(http_server_requests_seconds_bucket[5m]))` |
| Traffic | `rate(http_server_requests_seconds_count[5m])` |
| Errors | `rate(http_server_requests_seconds_count{status=~"5.."}[5m]) / rate(http_server_requests_seconds_count[5m])` |
| Saturation (CPU) | `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)` |

## Self-check

- [ ] Metric names follow `<project>_{subsystem}_{unit}_{suffix}` convention.
- [ ] No hardcoded `device_id` values in metric labels -- all use runtime variables.
- [ ] Every alert rule includes a `for:` duration.
- [ ] Label cardinality is bounded -- no unbounded label values (e.g., request path, user ID).
- [ ] ServiceMonitor or scrape config connects the metric endpoint to Prometheus.
- [ ] `helm lint` passes for any modified chart.
- [ ] Dashboard JSON or panel definitions are syntactically valid.
- [ ] Combined code output does not exceed 150 lines per service.

## Common mistakes

- **Hardcoded device_id in examples or code.** Always use a runtime variable. Copying `device_id="d1"` into production creates metrics for only one device.
- **Alert rules without `for:` duration.** Omitting `for:` causes alerts to fire on single-sample spikes.
- **Unbounded label cardinality.** Using request path, user ID, or trace ID as a metric label explodes Prometheus storage.
- **Eager metric registration at import time in `<mainApp>`.** Register metrics in a function, not at module scope, if the registry depends on runtime config.
- **Forgetting ServiceMonitor wiring.** Defining metrics in code without a ServiceMonitor means Prometheus never scrapes them.
- **Mixing monitoring and observability concerns.** This skill handles metrics and dashboards. For structured logging and tracing, use `observability`.

## Escalation

- **Prometheus not scraping the endpoint**: verify ServiceMonitor selector labels match the Service; if they do not match and the chart structure is unclear, escalate to the user.
- **Unsure about SLO thresholds**: surface the proposed values and ask the user to confirm before writing alert rules.
- **prom-client version mismatch**: if `package.json` shows a version whose API differs from the examples, flag it and adapt.
- **Cross-repo wiring unclear**: if the scrape config lives in the project's infrastructure repo and the structure is unfamiliar, escalate rather than guessing.

## Examples

<example title="Instrument telemetry latency in the device service">
Input: "Add a histogram for telemetry processing latency in the device service."

```python
from prometheus_client import Histogram

telemetry_latency = Histogram(
    'myproject_telemetry_latency_seconds',
    'Time to process a single telemetry message',
    ['device_id'],
    buckets=[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0]
)

async def process_telemetry(device_id: str, message: bytes) -> None:
    with telemetry_latency.labels(device_id=device_id).time():
        parsed = parse_message(message)
        await store_telemetry(parsed)
```
</example>

<example title="Alert when no devices are connected">
```yaml
- alert: NoDevicesConnected
  expr: myproject_devices_connected{status="active"} == 0
  for: 3m
  labels:
    severity: critical
  annotations:
    summary: "No active devices connected for 3 minutes"
    description: "Check the device-service pods and upstream connectivity."
```
</example>

## Failure modes

| Failure | Symptom | Recovery |
|---------|---------|----------|
| Metric not appearing in Prometheus | `/api/metrics` returns the metric but Prometheus targets page shows "down" | Check ServiceMonitor selector labels match the Kubernetes Service labels |
| Cardinality explosion | Prometheus OOM or slow queries | Audit labels; remove unbounded values; use `le` buckets for histograms |
| Alert firing on transient spike | Alert resolves within seconds | Add or increase `for:` duration |
| prom-client v14/v15 API mismatch | TypeScript compilation errors on registry methods | Check `package.json` version; adjust import pattern accordingly |
| Dashboard panel shows "No data" | Panel renders but is empty | Verify PromQL label matchers against actual metric labels; check time range |

## Related skills

- `observability` -- structured logging, distributed tracing, log correlation. Use observability for instrumentation that produces logs or traces; use monitoring for metrics, dashboards, and alerts.
- `monorepo-coordination` -- when monitoring changes span the app repo + deploy repo + infrastructure repo.
- The project's deployment workflow / infra-pack skills when enabled -- Helm chart patterns (health probes, resource templates) and verifying monitoring coverage before promoting to production.
