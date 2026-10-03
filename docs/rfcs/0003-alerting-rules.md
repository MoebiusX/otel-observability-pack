# RFC-0003 — Operational alert rules (`alerting.rules`)

| | |
|---|---|
| RFC | 0003 |
| Title | Operational alert rules: `alerting.rules[]` for the alerts that are not burn-rate alerts |
| Status | **Proposed** |
| Author | KrystalineX Platform Engineering |
| Created | 2026-10-03 |
| Target version | 1.4 |
| Discussion | issues/comments on the repo, or platform-engineering@ |

---

## 1. Summary

A pack can say which burn-rate alerts guard its SLOs (`policy.burn_rate_alerts`, required `[slo, windows]`) and where alerts are routed (`alerting`, required `[routes]`, `additionalProperties: false`). It cannot say which other alert rules the service runs. A pod restarting, a connection pool near its limit, a certificate about to expire, an error-log spike: these are *operational* alerts — they say something is wrong now, not that an error budget is being spent — and they outnumber burn-rate alerts in every real repository and every running Grafana or Prometheus. Spec 1.3 has no home for them.

This RFC adds one optional array to `alerting`: `rules[]`, one entry per operational rule, `{ name, expr }` required and `severity`, `for`, `labels`, `annotations`, `engine`, `source` optional. `routes` stays the only required key of `alerting`, so every 1.3 pack validates unchanged and means what it meant; `apiVersion` stays `observability.platform/v1`. It is an additive minor: spec 1.3 → 1.4, versioned the way RFC-0002 versioned 1.2 → 1.3.

---

## 2. Motivation

### 2.1 The reconciliation gap

The downstream compiler (Observogram) reads a service repository into a pack and reads the live system the repository deploys into a second pack, then compares the two. Its crawler discovers every Prometheus and Grafana alert rule in the repository, keeps the ones that reference a recorded ratio series as burn-rate alerts in `policy.burn_rate_alerts`, and drops every other rule with the warning

> Excluded N operational alert(s) from burn-rate policy — not SLO burn-rate alerts (no recorded-ratio reference). They remain available as alerting signals.

They are not available anywhere: the pack has no field to hold them, so they are emitted nowhere and lost. The live side does the same with the rules a running Grafana reports (`mcp.discovered.alert_rules_operational` counts them, nothing carries them). When a live snapshot — every provisioned alert rule, with its title — is reconciled against the crawled pack, zero operational alerts match, although the repository holds the very provisioning files those rules came from, with identical titles. The join key exists on both sides; the pack has nowhere to write it.

### 2.2 Why the existing fields cannot take them

- `policy.burn_rate_alerts` requires `slo` and at least two `windows` with `factor` and `severity`. An operational rule has no SLO and no burn factor; forcing one in manufactures an L1 contract that can never match the live system, which is exactly what the crawler refuses to do.
- `alerting` is `additionalProperties: false` and holds routing only (`routes`, `dedup`, `suppress`). A reader that wants to keep a rule has no key to put it under; a `labels`-only workaround on a route misdescribes a route.
- `metadata.annotations` is `Record<string,string>`: the names could be listed there (the live fetcher does list them in `mcp.discovered.alert_rule_names`), but a name list is not a rule — no `expr`, no `for`, no labels — and nothing downstream can validate, project or compare it.

### 2.3 Why the spec, not a downstream convention

Both readers vendor the schema and validate against it. A downstream-only field would fail validation against the vendored schema; a downstream-only annotation convention would be invisible to every other consumer. The gap is in the contract, so the fix is in the contract, as it was for `good_when` (RFC-0002 §2.2).

---

## 3. Detailed design

### 3.1 The field

```yaml
alerting:
  routes: [ ... ]                                  # unchanged; still the only required key
  rules:                                           # optional; operational (non-SLO) alert rules
    - name: PaymentServicePodRestarting            # required — the rule's exact name in its engine
      expr: increase(kube_pod_container_status_restarts_total{namespace="payments",container="payment-service"}[15m]) > 3
      for: 10m                                     # Duration
      severity: SEV3                               # the pack's routing severity (Severity)
      engine: prometheus                           # AlertEngine; absent means prometheus
      labels: { severity: warning, team: payments }
      annotations: { summary: "payment-service restarted more than 3 times in 15m" }
      source: observability/alerts.yml#payment-service.operational/PaymentServicePodRestarting
```

| Field | Type | Required | Meaning |
|---|---|---|---|
| `name` | string, 1–190 chars | yes | The rule's exact name: `alert:` in a Prometheus or Loki rule file, `title` of a Grafana-managed rule. The key a reconciler joins on against the live engine. |
| `expr` | string | yes | The condition in the engine's own language: PromQL, LogQL, or for a Grafana-managed rule the query of its data node (the condition reference, e.g. `C`, when the rule is built from expression nodes only). |
| `severity` | `Severity` (SEV1–SEV4) | no | The pack's routing severity, so `alerting.routes` apply to the rule. |
| `for` | `Duration` | no | The rule's pending duration, as the engine spells it. |
| `labels` | `Record<string,string>` | no | The engine's labels, verbatim — including its own `severity: critical \| warning \| …`. |
| `annotations` | `Record<string,string>` | no | The engine's annotations, verbatim. |
| `engine` | `AlertEngine` | no | The evaluator: `prometheus` (default), `mimir`, `thanos`, `victoriametrics`, `loki`, `grafana`, `alertmanager`. |
| `source` | string | no | Provenance: `<file path>#<rule group>/<rule name>` for a rule file, folder and group for a Grafana-managed rule. Never used for matching. |

### 3.2 Decisions, and why

**`name` is a free string, not a `Slug`.** The schema's identifiers are slugs because the pack mints them. A rule name is minted by the engine (`HighCPU`, `Payment API — 5xx spike`, Grafana titles with spaces and punctuation) and is the join key against that engine. Normalising it would break the join. The 190-character cap is Grafana's title limit, the longest any listed engine accepts.

**`expr` is not a `Ref`.** A burn-rate alert references an SLO and the compiler derives the expression; an operational rule *is* its expression, evaluated by the engine. `expr: ref:…` would mean nothing to a ruler.

**`severity` is the schema's `Severity`, and the engine's label is kept in `labels`.** Two vocabularies exist and both matter: the pack's SEV1–SEV4 is what `alerting.routes` key on, so a rule with a `severity` is routed like any policy event; the engine's `critical` / `warning` / `page` is what the rule file says and what the live engine reports, so it must survive verbatim for reconciliation. Collapsing them into one field would lose one. A reader that maps one onto the other (the crawler maps `critical → SEV1`, `warning → SEV2`, `info → SEV3`, the mapping it already applies to Alertmanager routes) writes both.

**`engine` is a closed enum of Product-registry names, not the open `Product` pattern.** The `Product` registry is open because a backend catalogue should accept a product lint has not heard of. `engine` is different: a reader *dispatches* on it — PromQL to a ruler, LogQL to Loki's ruler, a condition graph to Grafana — so an unknown value is a rule nobody can evaluate, and the schema should say so. Every value is the registry's own name for the product (`prometheus`, `mimir`, `thanos`, `victoriametrics`, `loki`, `grafana`, `alertmanager`), so a rule's engine joins to `telemetry.backends[].product` without a lookup table. `prometheus`, `mimir`, `thanos` and `victoriametrics` are the PromQL rulers the binding and its downstream compile to; `loki` is the LogQL ruler (the same rule-file format with a LogQL `expr`); `grafana` is a Grafana-managed rule, provisioned as `groups[].rules[].title` with `data[].model` nodes; `alertmanager` is a rule expressed as an Alertmanager matcher set over other alerts (a meta-alert or inhibition rule), kept so a reconciler that reads Alertmanager's own rule-like objects has a value. **Absent means `prometheus`**, the default binding's ruler, and a validator MUST NOT require the field. Adding an engine later is an additive minor; the open call for review is whether `alertmanager` belongs (§8).

**Division of labour is by kind, not by engine.** A rule that guards an SLO's recorded series is a burn-rate alert and belongs in `policy.burn_rate_alerts`, whichever engine evaluates it; every other rule belongs in `alerting.rules`. A rule MUST NOT appear in both. This is the classification the downstream crawler already applies (a rule whose `expr` references a recorded ratio series binds to that series' SLO); the RFC gives the other branch of that classification somewhere to go.

**`rules` lives under `alerting`, not `policy`.** `policy` is the SLO-derived layer: everything in it is computed *from* L1. An operational rule derives from nothing in the pack; it is an alerting fact about the service, beside the routes that deliver it.

### 3.3 Semantics

- `rules` is optional. Absent and `[]` mean the same thing: the service declares no operational rules.
- `name` and `expr` are required; a rule missing either is a schema violation (`/spec/alerting/rules/N: missing properties: 'expr'`).
- `engine` outside the enumeration is a schema violation naming the allowed values; absent reads as `prometheus`.
- `severity` outside SEV1–SEV4 is a schema violation; the engine's own severity belongs in `labels.severity`.
- The field changes no arithmetic and no other section. SLIs, SLOs, burn-rate alerts, routes, remediation and the rubric keep their definitions.

---

## 4. Compatibility

- **1.3 packs are unchanged.** No 1.3 pack contains `alerting.rules` (`$defs/Alerting` has `additionalProperties: false`, so the schema refused it), so every existing pack validates against the 1.4 schema and means what it meant. The reference example, the downstream compiler's examples, reference packs, library goldens and crawl golden — all 1.3-shaped — validate unchanged against the 1.4 schema (this was run before the RFC was written; see §7).
- **`routes` stays the only required key of `alerting`.** A validator MUST NOT require `rules`.
- **`apiVersion` stays `observability.platform/v1`.** The manifest gains an optional array; nothing is renamed, removed or re-typed.
- **A 1.3 consumer reading a 1.4 pack** that carries `rules`: a consumer that validates against the 1.3 schema refuses the whole key (`additionalProperties: false` on `Alerting`) until it vendors 1.4; it neither misreads the rules nor needs any advice. A consumer that reads packs without validating sees an unknown key under `alerting` and, by the usual rule for unknown optional keys, ignores it: the rules are not acted on, which is today's behaviour, and nothing else changes meaning.
- **Why 1.4 and not 1.3.x.** This repository versions the spec as `major.minor` and has no patch level: §1.3 of the spec says the minor moves for backward-compatible additions and the major for breaking changes, and RFC-0002 took 1.2 → 1.3 for an optional field on an existing object. An optional array on an existing object is the same kind of change, so it takes the same kind of bump. A patch level would also be invisible to the downstream vendoring tool, which reads `| Spec version | x.y |` from the spec's header table and lands each version in its own directory.

---

## 5. What readers must do

| Reader | What changes |
|---|---|
| Validators (this repository's `packlint`, the downstream vendored walker) | Accept `alerting.rules[]`; require `name` and `expr` per entry; refuse an unknown `engine` and a non-SEV `severity`; keep accepting an `alerting` block without `rules`. |
| Repository crawlers (Observogram `tools/lib/crawler.mjs`) | Stop dropping operational alerts: emit every alert rule that is not a burn-rate alert into `alerting.rules[]` with its exact `name`, `expr`, `for`, `labels`, `annotations`, `engine` (Prometheus rule file vs Grafana provisioning vs Loki) and `source` (file path and rule group). The burn-rate classification is unchanged. The "excluded … remain available" warning becomes a true summary line. |
| Live readers (Observogram `tools/fetch-live-pack.mjs`) | Emit the rules a running engine reports that bind to no SLO into `alerting.rules[]` the same way, so the two sides pair by `name`. |
| Layered projections and reconcilers (Observogram `tools/lib/adapter.mjs`, the artefact model) | Project `alerting.rules[i]` as alerting artefacts titled by rule name, keyed by name, so the existing name-based pairing finds the repository's rule and the live engine's rule to be one. |
| Compilers | Nothing. Compile targets consume `policy.burn_rate_alerts` and `alerting.routes`; an operational rule is already deployed by whatever provisioned it, and compiling it again would duplicate the live rule. A compiler MAY offer to re-emit `alerting.rules` in a rule file later; that is out of scope here. |
| Studio editors | An alerting form gains a list of operational rules beside the routes; the schema-driven validator already enforces the shape. |
| The maturity rubric | Nothing in this RFC (§7). |

---

## 6. Alternatives considered

### 6.1 Widen `policy.burn_rate_alerts` to accept rules without an SLO

Make `slo` and `windows` optional and add `name`/`expr`.

- **Pros**: one list of alerts.
- **Cons**: `policy` is the SLO-derived layer and the rubric counts `len(burn_rate_alerts) >= len(slos)` (clause 3.6) — mixing operational rules in would let a pack pass that clause with no burn-rate alert at all; the compiler would have to filter the list before every use; two shapes in one array is the schema smell `oneOf` exists to avoid.
- **Verdict**: rejected.

### 6.2 A new top-level `spec.alerts` section

- **Pros**: no change to `alerting`.
- **Cons**: a twelfth top-level key for a concern the spec already names (`alerting`, L4); every reader that walks sections gains a case; the layered model places operational alerts in L4 alerting beside routes, which is where a reader looks for them.
- **Verdict**: rejected in favour of a key under `alerting`.

### 6.3 Annotations only (`metadata.annotations['alerts.operational']`)

- **Pros**: no schema change; the live fetcher already lists names in `mcp.discovered.alert_rule_names`.
- **Cons**: everything in §2.2: a string is not a rule; nothing can validate, project or compare it; two readers would invent two encodings.
- **Verdict**: rejected. It is the workaround this RFC retires.

### 6.4 `engine` as the open `Product` pattern

- **Pros**: no enum to maintain.
- **Cons**: a reader dispatches on the engine; an unknown value is a rule no one can evaluate, which the schema should refuse rather than accept and flag. The enum reuses the registry's names, so nothing is duplicated.
- **Verdict**: rejected (§3.2).

### 6.5 A single `severity` that accepts both vocabularies

- **Cons**: `alerting.routes` key on SEV1–SEV4; a route cannot match `critical`. Keeping the engine's word in `labels` costs nothing and keeps the pack faithful to the file.
- **Verdict**: rejected (§3.2).

---

## 7. Reference implementation

The Go linter in this repository is the reference validator:

- `schema/observability-pack.schema.json` — `$defs/AlertEngine` (the enum, with its description) and `$defs/AlertRule` (`required: [name, expr]`, `additionalProperties: false`); `$defs/Alerting` gains `rules: { type: array, items: { $ref: AlertRule } }`; `required` stays `[routes]`; `$id` is unchanged.
- `internal/pack/pack.go` — `Alerting.Rules []AlertRule` (`json:"rules,omitempty"`), the `AlertRule` struct, the `AlertEngine*` constants and `(AlertRule).EffectiveEngine()`, which returns `AlertEnginePrometheus` when the field is absent. Downstream Go code reads the engine through the accessor, never the raw field, so the default lives in one place (the `EffectiveGoodWhen` pattern of RFC-0002).
- `internal/lint/lint_test.go` — the example declares three operational rules and none names an SLO; an `alerting` block without `rules` validates and so does `rules: []`; a rule without `expr` or without `name` fails under `/spec/alerting/rules/0` naming the field; `engine: nagios` fails listing the allowed values; `severity: critical` fails (the engine's label belongs in `labels`); an unknown property on a rule fails; `EffectiveEngine` reads absent as `prometheus`.
- `examples/payment-service.pack.yaml` — three operational rules: `PaymentServicePodRestarting` (Prometheus, engine stated), `PaymentDbConnectionPoolSaturated` (Prometheus, engine absent to show the default) and `PaymentCertificateExpiringSoon` (Grafana-managed, over the blackbox probe's series the synthetic check already produces).

**Compatibility, as run.** `packlint` on the example keeps `schema=true refs=true`; its conformance findings are unchanged in kind from 98be4ae. The 1.4 schema was run over every schema-valid 1.3-shaped pack the downstream compiler ships or tests with (its two examples, its reference packs, its library goldens, its site fixture and its crawl golden) and every one validates with no finding.

**Rubric.** `docs/maturity-model.md` is deliberately untouched. Operational-alert coverage is not an SLO-contract property: a service with no operational rules may be perfectly observed through its SLOs, and a service with forty may have no SLO at all, so a count of `alerting.rules` says nothing about maturity by itself. Clause 3.6 keeps counting `policy.burn_rate_alerts` against `slos`, clause 3.7 keeps counting routes, and no clause counts operational rules, so no existing pack's tier changes. If the platform later wants to score them (for instance, "every `alerting.rules[]` entry declares a `severity` so that a route applies to it", the SHOULD in §5.8), that is a rubric revision under `docs/maturity-model.md` §8 — an informational or SHOULD row, since a new MUST is a major bump there — and not part of this RFC.

---

## 8. Open questions for review

1. **`alertmanager` in the engine enum.** Alertmanager routes and inhibits alerts; it does not evaluate alert rules in the sense the other six do. It is listed so that a reader of Alertmanager's own rule-like objects (inhibition rules, matcher-defined meta-alerts) has a value. If reviewers prefer the enum to name evaluators only, dropping it before 1.4 is accepted costs nothing; adding it back later is an additive minor.
2. **Whether `alerting.rules` should ever be a compile target.** This RFC says no (an operational rule is already deployed by what provisioned it). A later RFC could let the compiler re-emit them into a rule file for a service that wants the pack to be the single source of its alerts.

---

## Change log

| Date | Author | Change |
|---|---|---|
| 2026-10-03 | Platform Engineering | Initial text, proposed (RFC-0003) |
