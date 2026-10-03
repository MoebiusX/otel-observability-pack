# otel-observability-pack

> The platform-engineering standard for OpenTelemetry-native observability — one declarative manifest per service, reconciled into Prometheus + Elasticsearch + Grafana.

A platform-engineering standard that binds every observability concern for a single service — SLIs/SLOs, telemetry collection, storage, queries, dashboards, alert policy, routing, self-healing, reliability baselines, and validation — into one declarative, versioned manifest.

One service = one pack = one PR to change anything observable about it.

---

## Layout

```
otel-observability-pack/
├── README.md                       <- you are here
|
├── spec/                           <- the standard itself
│   └── ObservabilityPack-Spec.md                 (Markdown, renders on GitHub)
│
├── schema/                         <- machine-checkable contract
│   └── observability-pack.schema.json     (JSON Schema 2020-12)
│
├── examples/                       <- reference packs
│   └── payment-service.pack.yaml          (HTTP API + Kafka consumer)
│
├── docs/                           <- supporting documents
│   ├── operator-design.md                 (controller / reconciliation design)
│   └── maturity-model.md                  (tier-1/2/3 conformance rubric)
│
├── diagrams/                       <- architecture diagrams
│   └── observability-pack-layered-model.svg / .png    (the layered model)
│
└── bindings/                       <- concrete realisations of the abstract spec
    └── otel-elastic-prometheus-grafana.md             (OTel + Prom + Elastic + Grafana — default)
```

---

## The model in one paragraph

The pack is organised as four concentric layers — **L1 Contract** (SLIs/SLOs), **L2 Telemetry** (OTel pipelines + storage), **L3 Insight** (queries + dashboards), **L4 Action** (policy + alerting + self-healing) — wrapped by a fifth, orthogonal layer, **L5 Validation** (chaos, synthetic probes, MTTD/MTTR baselines), that proves the four below it actually work. A `Pack` custom resource is reconciled by the ObservabilityPack Operator, a meta-operator that uses other operators and automation surfaces as tools: in SKE and bare Kubernetes it writes OpenTelemetry Operator, Prometheus Operator, Grafana Operator, Alertmanager, storage, Argo, and Chaos Mesh resources; for Azure targets it emits an auditable pipeline invocation that applies the same effective pack through Azure-native automation. Referential integrity is enforced at CI time.

The default binding pins an explicit **OTel realisation**: instrumentation is OpenTelemetry, metrics live in Prometheus, logs and traces live in Elasticsearch (Elastic APM-compatible), dashboards are in Grafana. See `bindings/otel-elastic-prometheus-grafana.md` for the full binding contract.

See `spec/ObservabilityPack-Spec.md` for the abstract model and `bindings/otel-elastic-prometheus-grafana.md` for the OTel binding deltas.

---

## Quickstart

1. **Read the spec** — `spec/ObservabilityPack-Spec.md` is the canonical reference.
2. **Copy the example** — `examples/payment-service.pack.yaml` is a working tier-1 pack covering an HTTP API and a Kafka consumer.
3. **Validate locally** — two options:

   **Option A — `packlint` (recommended, includes maturity-model conformance):**

   ```bash
   go run ./cmd/packlint --schema schema/observability-pack.schema.json \
     examples/payment-service.pack.yaml
   ```

   Output formats: `--format text|yaml|json`. Exit code `0` on full pass, `1` on any
   error-severity finding, `2` on invocation error. CI uses `--format yaml` and
   archives the report.

   `packlint` runs three independent passes:

   - **Schema** — JSON Schema 2020-12 compliance.
   - **References** — every `slis.<id>`, `slos.<id>`, dashboard panel binding,
     remediation trigger, and chaos `expected_alerts` entry resolves to a
     defined object. External `ref:platform/...` imports are deferred to the
     import resolver (info-level).
   - **Conformance** — clause-by-clause scoring against the maturity rubric
     in `docs/maturity-model.md` §3, cumulative across tier-3 → tier-2 → tier-1
     up to the pack's declared `metadata.bindings.criticality`.

   **Option B — Python jsonschema only (schema check, no conformance):**

   ```bash
   pip install jsonschema pyyaml
   python -c "import yaml,json; from jsonschema import Draft202012Validator; \
     v = Draft202012Validator(json.load(open('schema/observability-pack.schema.json'))); \
     errs = list(v.iter_errors(yaml.safe_load(open('examples/payment-service.pack.yaml')))); \
     print('OK' if not errs else f'FAIL: {len(errs)} error(s)')"
   ```

   The example pack ships schema-valid; this is the same check the CI gate runs.

4. **Read the maturity model** — `docs/maturity-model.md` tells you what's required at tier-3 vs tier-2 vs tier-1, so service teams can grow into conformance rather than facing an all-or-nothing bar.

---

## Conformance, in one table

| Layer | Tier-3 (minimum) | Tier-2 | Tier-1 (full) |
|---|---|---|---|
| L1 SLIs/SLOs | Availability SLO | + Latency SLO | + Domain SLO |
| L2 Collection | Default scrape | + OTel metrics | + OTel traces, logs |
| L3 Queries | Recording rule per SLO | + Derived view | + Per-tenant rollups |
| L3 Dashboards | Service overview | + SLO burn | + Deployment, customer-impact |
| L4 Policy | Single-window alert | Multi-window burn rate | + Forecast |
| L4 Alerting | Chat | + Voice for SEV1 | + WhatsApp out-of-band |
| L4 Self-healing | Optional | Optional | At least one automation |
| L5 Baselines | Declared | + Regression warning | + Release gate |
| L5 Validation | Synthetic probe | + Monthly chaos | + Weekly prod chaos |

Full clause-level rubric in `docs/maturity-model.md`.


---

## Status

| | |
|---|---|
| Spec version | 1.4 |
| Author | Carlos Montero |
| Status | Draft for review |
| First publication | 2026-05-08 |
| Default binding | `otel-elastic-prometheus-grafana` |

---

