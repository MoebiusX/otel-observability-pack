# ObservabilityPack — OTel Binding for Prometheus / Elasticsearch / Grafana

**Binding ID:** `otel-elastic-prometheus-grafana`
**Spec version:** `1.3`
**Pack apiVersion:** `observability.platform/v1`
**Status:** Draft for review
**Owner:** KrystalineX Platform Engineering — Observability Practice

---

## 1. Purpose

The ObservabilityPack spec defines the **abstract** standard: ten dimensions, five layers, conformance rubric. It deliberately stays tool-agnostic so the same model can land on different stacks without rewriting the schema.

This binding document is the platform's current default realisation, pinning the **concrete** stack:

- **Instrumentation:** OpenTelemetry SDK + OTel Collector
- **Metrics:** Prometheus (with optional Mimir / Thanos for long-term retention)
- **Logs & Traces:** Elasticsearch + Elastic APM
- **Visualisation:** Grafana
- **Conventions:** OpenTelemetry Semantic Conventions

When a pack declares `metadata.binding: otel-elastic-prometheus-grafana`, every section of the manifest must satisfy the constraints documented here, and the operator generates the exact artefact formats listed in §5.

A future `otel-grafanalabs` binding (Mimir + Loki + Tempo + Grafana) or `otel-aws-managed` binding (AMP + OpenSearch + Managed Grafana) would replace this document — pack semantics would stay identical, only the wiring would change.

---

## 2. Stack versions

| Component | Tracked version | Floor version | Rationale |
|---|---|---|---|
| OpenTelemetry Semantic Conventions | `1.27.0` | `1.26.0` | HTTP, RPC, DB, messaging are stable in 1.27 |
| OpenTelemetry SDK | latest stable per language | n−1 of latest | Auto-instrumentation maturity |
| OpenTelemetry Collector | `0.110+` | `0.105+` | Required for stable elasticsearchexporter |
| Prometheus | `2.55` | `2.53` (LTS) | Native histograms GA, OTLP receiver |
| Mimir / Thanos (optional) | latest | latest−1 | Required only if retention > 60 days |
| Elasticsearch | `8.15` | `8.13` | OTLP ingest improvements; data streams native |
| Grafana | `11.3` | `11.0` | Scenes-based dashboard schema (v39+) |
| Alertmanager | `0.27` | `0.26` | UTF-8 label names |

The binding moves in lock-step with these. A "latest−1" floor is non-negotiable: services lagging more than one minor version are flagged as non-conformant by the conformance scanner.

---

## 3. The OTel-native pack shape

Two sections of the pack manifest are binding-specific: a top-level `spec.otel` block (instrumentation contract) and a `spec.pipelines` block whose shape mirrors OTel Collector configuration. Every other section is shared with the abstract spec.

### 3.1 Required `spec.otel` block

```yaml
otel:
  semconv: "1.27.0"
  resource_attributes:
    required:
      - service.name
      - service.namespace
      - service.version
      - service.instance.id
      - deployment.environment
    custom:                              # service-specific, optional
      - tenant.id
      - business.domain
  sdk:
    languages: [java, node, python]      # which auto-instrumentation packs are mandated
    sampling:
      policy: parentbased_traceidratio
      ratio: 0.1                         # head-based; tail sampling done in Collector
    propagators: [tracecontext, baggage]
    log_correlation: true                # inject trace_id/span_id into logs
```

Every pack MUST declare this block. The operator validates that emitted telemetry actually carries these resource attributes; missing attributes flip the pack to `Degraded`.

### 3.2 `spec.pipelines`

The shape mirrors OTel Collector configuration deliberately, so the operator can render it directly:

```yaml
pipelines:
  receivers:
    - name: otlp
      protocols: [grpc, http]
      endpoint: 0.0.0.0:4317

    - name: prometheus                   # for legacy /metrics endpoints
      scrape_configs:
        - job_name: payment-api
          scrape_interval: 15s
          static_configs:
            - targets: [payment-api:9090]

  processors:
    - name: memory_limiter
      limit_percentage: 80
    - name: batch
      timeout: 10s
    - name: resource
      attributes:
        - { key: deployment.environment, value: ${ENV}, action: upsert }
    - name: transform                    # metric/log/span enrichment
      log_statements: []
    - name: tail_sampling                # traces only
      policies:
        - { name: errors,        type: status_code, status_code: { status_codes: [ERROR] } }
        - { name: slow,          type: latency,     latency: { threshold_ms: 500 } }
        - { name: probabilistic, type: probabilistic, probabilistic: { sampling_percentage: 5 } }

  exporters:
    metrics:
      kind: prometheusremotewrite
      endpoint: https://prometheus.internal/api/v1/write
      headers: { X-Scope-OrgID: payments }
    logs:
      kind: elasticsearch
      endpoints: [https://es.internal:9200]
      logs_index: logs-payments-default
    traces:
      kind: elasticsearch
      endpoints: [https://es.internal:9200]
      traces_index: traces-apm-default
```

Three logical pipelines emerge (metrics, logs, traces), each composed of receivers → applicable processors → its exporter. The operator splits the unified block into three Collector pipelines at render time.

### 3.3 SemConv-aligned SLI references

SLIs reference metrics by their canonical OTel SemConv name; the materialised PromQL is the form after prometheusexporter translation. Example:

```yaml
slis:
  - id: api_latency_p99
    type: threshold
    description: 99th-percentile API latency (server-side).
    semconv_metric: http.server.request.duration
    query: |
      histogram_quantile(
        0.99,
        sum by (le)(rate(http_server_request_duration_seconds_bucket{service_name="payment-service"}[5m]))
      )
    threshold: 0.5
    unit: seconds
```

The `semconv_metric` field is the canonical OTel name. The `query` field is the materialised PromQL after the prometheusexporter has translated the OTel metric into its Prom equivalent (dots become underscores, units appended). The operator validates that the two are consistent.

`threshold` is the bound, in `unit`; `good_when` (spec 1.3, default `below`) says which side of it is good. When the operator renders a `threshold` or `distribution` SLI into Prometheus recording and alerting rules (§6.2) or into a Grafana threshold step, the bad-sample comparison follows `good_when`: bad = samples above the bound when `below`, bad = samples under the bound when `above`; the bound itself is good either way. A 1.2 pack cannot say `good_when` and renders exactly as before.

---

## 4. Backend pinning

### 4.1 Metrics — Prometheus

| Concern | Value |
|---|---|
| Receive | `prometheusremotewrite` from OTel Collector, OR direct scrape via Collector's `prometheusreceiver` |
| Storage | Native Prometheus TSDB up to 30 days; remote_write to Mimir/Thanos for ≥ 60 days |
| Rule evaluation | Prometheus ruler (or Mimir ruler if remote_write tier-1) |
| Recording rule format | Prometheus rule group YAML (`groups: [...]`) |
| Alerting rule format | Same file format, with `alert:` instead of `record:` |
| Service discovery | Kubernetes service-monitor CRDs are out of scope — all scrape configs live in the pack |

Long-term retention pattern: every tier-1 pack writes a `remote_write` block targeting Mimir; metrics older than 14 days are queried from Mimir, recent from local Prom. This is invisible to dashboards behind the Prometheus query frontend.

### 4.2 Logs and traces — Elasticsearch

| Concern | Value |
|---|---|
| Ingest | OTel Collector's `elasticsearchexporter` (data streams API) |
| Logs data stream | `logs-<service.namespace>-<deployment.environment>` |
| Traces data stream | `traces-apm-default` (Elastic APM-compatible) |
| Index template | Per-service template applied at pack-apply time |
| Lifecycle | ILM policy referenced by name; platform ships `std-logs-ilm-90d`, `std-traces-ilm-14d`, etc. |
| Search/alerting | Kibana-side alerts + Elastic Watcher; Grafana with ES data source for visual cross-pollination |
| Tail sampling | Done in the Collector before traces hit ES; reduces ES index volume significantly |

### 4.3 Dashboards — Grafana

| Concern | Value |
|---|---|
| Dashboard JSON format | Grafana 11.x schema, `schemaVersion >= 39` (Scenes-based) |
| Data sources | Pack declares logical references (`ref:platform/ds-prometheus`); operator resolves UIDs at apply time |
| Provisioning | Grafana provisioning API; one Grafana folder per pack, named after the service |
| Templating | Pack-level variables: `$env`, `$tenant`, `$cluster` injected by the operator from `metadata.bindings` |
| Auth | Operator uses a dedicated service account with folder-scoped Editor role |

---

## 5. Section-by-section artefact mapping

Each pack section maps to exactly one or two native artefacts the operator generates. CI lints both the pack and the rendered artefacts.

| Pack section | Generated artefact | File format | Lives in (operator-owned) |
|---|---|---|---|
| `otel.sdk` | Service env vars + instrumentation config | env vars, Helm values, sidecar `OpenTelemetryCollector` CR | Per service / per pod |
| `otel.resource_attributes` | OTLP attribute assertions | Validator in the gateway Collector | OTel Gateway |
| `pipelines.receivers` | Agent + gateway Collector configs | OTel Collector YAML | `otel-collector-agent`, `otel-collector-gateway` |
| `pipelines.processors` | Same | Same | Same |
| `pipelines.exporters.metrics` | `prometheusremotewrite` block | OTel Collector YAML | Gateway |
| `pipelines.exporters.logs` | `elasticsearch` block | OTel Collector YAML | Gateway |
| `pipelines.exporters.traces` | `elasticsearch` block | OTel Collector YAML | Gateway |
| `storage.metrics` | Prometheus `--storage.tsdb.retention.time`, `remote_write` | CLI + prometheus.yaml | Prometheus |
| `storage.logs` | ES index template + ILM policy | JSON via ES `_index_template`, `_ilm/policy` | Elasticsearch |
| `storage.traces` | Same | Same | Elasticsearch |
| `queries.recording_rules` | Prom rule group | YAML | Prometheus / Mimir ruler |
| `policy.burn_rate_alerts` | Prom alerting rules | YAML | Prometheus / Mimir ruler |
| `policy.forecasts` | Recording rule + alerting rule | YAML | Ruler |
| `dashboards.*` | Grafana dashboard JSON (schemaVersion 39+) | JSON via provisioning API | Grafana |
| `alerting.routes` | Alertmanager route tree + receivers | YAML | Alertmanager |
| `alerting.suppress` | Silences + maintenance windows | YAML / API | Alertmanager |
| `remediation` | Argo Events Sensor + Workflow Template | CRDs | Argo Workflows |
| `baselines` | Derived metrics from incident-mgmt + alert times | Prom recording rules | Platform observability service |
| `validation.chaos` | Chaos Mesh Workflow / Schedule | CRDs | Chaos Mesh |
| `validation.synthetic` | Blackbox probe config OR Elastic Synthetics monitor | YAML / Elastic API | blackbox-exporter / Elastic Synthetics |

---

## 6. Worked artefact examples

### 6.1 OTel Collector gateway config (generated from `pipelines:`)

```yaml
receivers:
  otlp: { protocols: { grpc: { endpoint: 0.0.0.0:4317 }, http: { endpoint: 0.0.0.0:4318 } } }
  prometheus:
    config:
      scrape_configs:
        - job_name: payment-api
          scrape_interval: 15s
          static_configs: [{ targets: [payment-api:9090] }]

processors:
  memory_limiter: { check_interval: 1s, limit_percentage: 80 }
  batch: { timeout: 10s }
  resource:
    attributes:
      - { key: deployment.environment, value: prod, action: upsert }
  tail_sampling:
    decision_wait: 10s
    policies:
      - { name: errors,        type: status_code,   status_code: { status_codes: [ERROR] } }
      - { name: slow,          type: latency,       latency: { threshold_ms: 500 } }
      - { name: probabilistic, type: probabilistic, probabilistic: { sampling_percentage: 5 } }

exporters:
  prometheusremotewrite:
    endpoint: https://prometheus.internal/api/v1/write
    external_labels: { service: payment-service, env: prod }
  elasticsearch/logs:
    endpoints: [https://es.internal:9200]
    logs_index: logs-payments-default
  elasticsearch/traces:
    endpoints: [https://es.internal:9200]
    traces_index: traces-apm-default

service:
  pipelines:
    metrics: { receivers: [otlp, prometheus], processors: [memory_limiter, batch, resource], exporters: [prometheusremotewrite] }
    logs:    { receivers: [otlp],             processors: [memory_limiter, batch, resource], exporters: [elasticsearch/logs] }
    traces:  { receivers: [otlp],             processors: [memory_limiter, batch, resource, tail_sampling], exporters: [elasticsearch/traces] }
```

### 6.2 Prometheus recording + burn-rate alert rules

```yaml
groups:
  - name: payment-service.slo
    interval: 30s
    rules:
      # recording: ratio over 5m
      - record: payment:api_availability:ratio_5m
        expr: |
          sum(rate(http_server_request_duration_seconds_count{service_name="payment-service",http_response_status_code!~"5.."}[5m]))
          /
          sum(rate(http_server_request_duration_seconds_count{service_name="payment-service"}[5m]))

      # recording: burn rate over 1h
      - record: payment:api_errorbudget:burn_1h
        expr: (1 - payment:api_availability:ratio_5m) / (1 - 0.999)

      # alert: fast burn (5m short, 1h long, 14x factor)
      - alert: PaymentAPIBurnFast
        expr: |
          (1 - avg_over_time(payment:api_availability:ratio_5m[5m]))  > 14 * (1 - 0.999)
          and
          (1 - avg_over_time(payment:api_availability:ratio_5m[1h])) > 14 * (1 - 0.999)
        for: 2m
        labels: { severity: SEV1, slo: api_availability_99_9, service: payment-service }
        annotations:
          summary: "Payment API SLO fast-burning"
          runbook_url: "https://runbooks.example.internal/payments/api-burn"

      # alert: slow burn (30m short, 6h long, 6x factor)
      - alert: PaymentAPIBurnSlow
        expr: |
          (1 - avg_over_time(payment:api_availability:ratio_5m[30m])) > 6 * (1 - 0.999)
          and
          (1 - avg_over_time(payment:api_availability:ratio_5m[6h]))  > 6 * (1 - 0.999)
        for: 15m
        labels: { severity: SEV2, slo: api_availability_99_9, service: payment-service }
```

### 6.3 Elasticsearch ILM policy (logs, 90-day retention)

```json
{
  "policy": {
    "phases": {
      "hot":    { "actions": { "rollover": { "max_size": "50gb", "max_age": "1d" }, "set_priority": { "priority": 100 } } },
      "warm":   { "min_age": "2d",  "actions": { "shrink": { "number_of_shards": 1 }, "forcemerge": { "max_num_segments": 1 } } },
      "cold":   { "min_age": "14d", "actions": { "set_priority": { "priority": 0 } } },
      "delete": { "min_age": "90d", "actions": { "delete": {} } }
    }
  }
}
```

### 6.4 Grafana dashboard binding (provisioned)

```yaml
dashboards:
  - id: payment-overview
    provider: { kind: grafana, version: "11.3", schemaVersion: 39 }
    folder: payment-service
    source: file://dashboards/payment-overview.json
    datasources:
      metrics: ref:platform/ds-prometheus
      logs:    ref:platform/ds-elasticsearch-logs
      traces:  ref:platform/ds-elasticsearch-apm
    panel_bindings:
      - { panel: sli-availability,  binds_to: slis.api_availability  }
      - { panel: sli-latency-p99,   binds_to: slis.api_latency_p99   }
```

### 6.5 Alertmanager route fragment (generated from `alerting.routes`)

```yaml
route:
  receiver: default
  group_by: [alertname, service, severity]
  routes:
    - matchers: [ service="payment-service", severity="SEV1" ]
      receiver: payments-sev1
      group_wait: 30s
      group_interval: 5m
      repeat_interval: 4h

receivers:
  - name: payments-sev1
    pagerduty_configs:    [{ service_key: ${PD_PAYMENTS_KEY}, severity: critical }]
    msteams_configs:      [{ webhook_url: ${TEAMS_PAYMENTS_ONCALL} }]
    webhook_configs:      [{ url: https://oncall.internal/whatsapp/payments }]
```

### 6.6 Synthetic check (Elastic-native + OTel-instrumented)

```yaml
validation:
  synthetic_checks:
    - id: api-checkout-canary
      kind: elastic-synthetics            # browser or HTTP monitor
      target: https://api.example.com/payments/checkout/canary
      interval: 1m
      otel_instrumentation: true          # probe propagates traceparent; trace shows up in APM
      assertions:
        - { status_code: 200 }
        - { latency_lt: 500ms }
      on_fail_severity: SEV2
```

The `otel_instrumentation: true` flag is the magic: synthetic probes generate the same span tree real users would. Probe failures correlate to real-user impact in Elastic APM by trace.

---

## 8. Open questions

A handful of decisions deliberately left open for review:

- **Mimir vs Thanos for long-term retention.** Either works; we've defaulted to Mimir in §4.1 because of its native multi-tenancy. If we adopt Thanos for cost reasons, this section needs to be re-pinned.
- **Elastic APM vs OTel-native Tempo.** Sticking with Elastic for now since logs already live there and the join story is cleaner. Worth revisiting when Tempo's TraceQL matures further.
- **Grafana Alerting vs Prometheus Alertmanager.** Two separate alerting engines exist (Grafana built-in + Alertmanager). Today the binding pins Alertmanager because it's already deployed; Grafana Alerting may make sense for log-derived alerts in a future revision.
- **OnCall integration.** Grafana OnCall is the standard for tier-1 routing; the binding currently assumes PagerDuty. A future revision may pin Grafana OnCall instead.
