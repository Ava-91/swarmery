# Metrics Instrumentation Procedure

**Placeholders:** `<mainApp>` = `project.json → mainApp`, `<device>` = `project.json → device` (the edge/device service, if any), `<project>` = `project.json → name` snake_cased (the metric-name prefix). Repos and layout come from `project.json → repos`.

## Required environment

- Access to the application repo(s) listed in `project.json → repos` (e.g., `<mainApp>` using `prom-client` for TypeScript, `<device>` using `prometheus_client` for Python).
- Access to the project's deploy/charts repo (Helm charts, ServiceMonitor/PrometheusRule CRDs), if one exists.
- Access to the project's infrastructure repo (Prometheus scrape configs, Grafana provisioning), if one exists.

## Inputs

| Input | Required | Description |
|-------|----------|-------------|
| Target service | Yes | Which service to instrument (`<mainApp>`, `<device>`, or new) |
| Metric intent | Yes | What to measure (latency, throughput, error rate, saturation, domain-specific) |
| Alert thresholds | No | Operator-defined SLOs or severity levels (if creating alerts) |
| Existing dashboards | No | Path to current Grafana JSON to extend |

## Outputs

**Length budget:** Combined code output must not exceed 150 lines per instrumented service. Dashboard JSON panels are excluded from this limit.

Deliverables:
- Metric definitions in application code (TypeScript or Python).
- Prometheus alert rule YAML (PrometheusRule CRD or standalone rule file).
- Grafana dashboard JSON (or panel additions to existing dashboard).
- ServiceMonitor/PodMonitor YAML for the deploy/charts repo.
- Verification evidence: `helm lint` pass for chart changes, metric endpoint curl output.

## Step 1: Identify metric type and naming

Choose Counter, Histogram, or Gauge. Follow the naming convention `<project>_{subsystem}_{unit}_{suffix}` (e.g., `myproject_telemetry_messages_total`, `myproject_telemetry_latency_seconds`).

**Checkpoint:** Metric name confirmed to follow `<project>_{subsystem}_{unit}_{suffix}` convention.

## Step 2: Instrument the application code

**`<device>` (Python):**
```python
from prometheus_client import Counter, Histogram, Gauge

telemetry_messages = Counter(
    'myproject_telemetry_messages_total',
    'Total telemetry messages received',
    ['device_id', 'type']
)
telemetry_latency = Histogram(
    'myproject_telemetry_latency_seconds',
    'Telemetry processing latency',
    ['device_id']
)
devices_connected = Gauge(
    'myproject_devices_connected',
    'Number of connected devices',
    ['status']
)

# Usage -- device_id comes from runtime, never hardcode
telemetry_messages.labels(device_id=device_id, type='POSITION_UPDATE').inc()
telemetry_latency.labels(device_id=device_id).observe(latency_s)
devices_connected.labels(status='active').set(active_count)
```

**`<mainApp>` (TypeScript):**
```typescript
// src/lib/metrics.ts
import { Counter, Histogram, Registry } from 'prom-client';

export const registry = new Registry();

export const telemetryMessages = new Counter({
  name: 'myproject_telemetry_messages_total',
  help: 'Total telemetry messages received',
  labelNames: ['device_id', 'type'] as const,
  registers: [registry],
});

// Usage -- device_id is a runtime variable
telemetryMessages.labels({ device_id: deviceId, type: 'POSITION_UPDATE' }).inc();
```

Expose via an API route (e.g. `src/app/api/metrics/route.ts`) guarded by a scrape-only auth check. Check `package.json` for the actual `prom-client` version -- the v14 and v15 APIs differ in registry handling.

**Checkpoint:** Code compiles/lints. Metric labels use only runtime variables, no hardcoded values.

## Step 3: Wire Prometheus scraping via ServiceMonitor (deploy/charts repo)

```yaml
# <deploy-repo>/charts/<mainApp>/templates/servicemonitor.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "<mainApp>.fullname" . }}
  labels:
    {{- include "<mainApp>.labels" . | nindent 6 }}
spec:
  selector:
    matchLabels:
      {{- include "<mainApp>.selectorLabels" . | nindent 8 }}
  endpoints:
    - port: http
      path: /api/metrics
      interval: 15s
```

Run `helm lint` after adding or modifying any chart template.

**Checkpoint:** `helm lint` passes. ServiceMonitor selector labels match the Kubernetes Service labels.

## Step 4: Define alert rules

```yaml
groups:
  - name: project-alerts
    rules:
      - alert: HighErrorRate
        expr: >
          rate(http_server_requests_seconds_count{status=~"5.."}[5m])
          / rate(http_server_requests_seconds_count[5m]) > 0.05
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Error rate above 5% for {{ $labels.service }}"

      - alert: DeviceDisconnected
        expr: myproject_devices_connected{status="active"} < 1
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "No active devices connected"
```

Every alert rule MUST include a `for:` duration to avoid firing on transient spikes.

**Checkpoint:** Every alert has a `for:` duration. Alert thresholds confirmed with user (or flagged for confirmation).

## Step 5: Build Grafana dashboard panels

See the golden-signals PromQL table in `promql-and-alerts-reference.md`.

Domain-specific panel examples:
- Message rate per device: `rate(myproject_telemetry_messages_total[5m])`
- Latency percentiles: `histogram_quantile(0.95, rate(myproject_telemetry_latency_seconds_bucket[5m]))`
- Connected device count: `myproject_devices_connected{status="active"}`

**Checkpoint:** Dashboard JSON is syntactically valid. Panel PromQL references only metrics that exist.

## Step 6: Verify the instrumentation

- Curl the metrics endpoint and confirm new metrics appear.
- Run `helm lint` on any modified chart.
- Verify alert rule syntax: `promtool check rules <file>` (if available).

**Checkpoint:** Verification evidence collected. Ready to return results.
