# RFC-0002 — Threshold direction (`good_when`)

| | |
|---|---|
| RFC | 0002 |
| Title | Threshold direction: `good_when` on threshold and distribution SLIs |
| Status | **Accepted — 2026-09-23** |
| Author | KrystalineX Platform Engineering |
| Created | 2026-09-23 |
| Target version | 1.3 |
| Discussion | issues/comments on the repo, or platform-engineering@ |

---

## 1. Summary

A `threshold` or `distribution` SLI carries a bound (`threshold`) but, in spec 1.2, no direction: every reader assumes the bound is a ceiling and that a sample above it is bad. That is right for latency, lag, error rate and queue age, and wrong for every quantity whose failure mode is *too little*: in-sync replicas, free capacity, connected consumers, throughput, active brokers.

This RFC adds one optional field to the SLI, `good_when: below | above`, default `below`. `below` is today's meaning; `above` makes a floor first-class. Absent means `below`, so every 1.2 pack stays valid and keeps its meaning; `apiVersion` stays `observability.platform/v1`. It is an additive minor: spec 1.2 → 1.3.

The name was chosen over `comparison: lte | gte` because it says what is compared (the good sample against the bound) and which side is good, in the words an SLO review uses.

---

## 2. Motivation

### 2.1 What a floor costs today

Two workarounds exist, and both are in use. This RFC retires them.

**Encoding the floor as a ratio SLI.** The author writes `good` as a comparison that is true when the value is above the floor and `total` as the expected sample count:

```yaml
- id: settlement_consumers_active
  type: ratio
  good:  sum(kafka_consumer_group_members{group="payment-settler"} >= bool 2)
  total: "1"
```

The pack no longer says the number that matters (2), the unit (consumers) or the quantity being watched; it says a probability. The dashboard cannot draw the floor as a line, the SLO review cannot discuss the floor, and the downstream compiler has to recognise the `>= bool` shape to recover a sample count it could have read from a `threshold` field.

**Inverting the quantity into a "headroom" ceiling.** The author rewrites the query so that a ceiling holds: `2 - members` with `threshold: 0`, `1 - free_ratio` with `threshold: 0.8`. The number in the pack is now the negative or the complement of the operator's number, the `unit` is a lie (a "consumers" gauge that reads −1), and every panel and runbook that shows the raw quantity disagrees with the SLI.

### 2.2 The downstream compiler refuses a floor, and says so

Observogram's burn-rate generator (`tools/lib/burn-rules.mjs`) reads a threshold SLI as

```
bad = sum_over_time((max(series) > bool threshold)[w:step])
```

and warns, on a ratio-shaped query or a `ratio` / `percent` unit, that "the policy reads threshold as an upper bound (bad = samples above it) … suggests a floor, which the spec cannot express (no direction field)". The dashboard library colours every threshold SLI with `okAbove` (higher is worse). The compiler cannot guess a direction, and the maintainer's conclusion on 2026-09-23 was the correct one: the spec has to carry it.

---

## 3. Detailed design

### 3.1 The field

```yaml
slis:
  - id: <slug>
    type: threshold | distribution
    query: <PromQL>
    threshold: <number>          # the bound, in `unit`
    good_when: below | above     # optional; default below
    unit: <string>
    percentile: <0..1>           # distribution only
```

| `good_when` | A sample is good when it is … | Bad samples | Typical quantities |
|---|---|---|---|
| `below` (default) | at or below `threshold` | `value > threshold` | latency, lag, error rate, queue age, saturation |
| `above` | at or above `threshold` | `value < threshold` | in-sync replicas, free capacity, connected consumers, throughput, active brokers |

The bound itself is good in both directions (the comparison is strict on the bad side), which is what every 1.2 reader already implements for `below`.

### 3.2 Semantics

- `good_when` is meaningful only where a bound exists: `threshold` and `distribution` SLIs. On `ratio` and `custom` SLIs it is invalid (schema: `properties.good_when: false` in their `then` branches).
- For a `distribution` SLI the bound applies to the value at `percentile`; `good_when` says which side of that value is good, exactly as for `threshold`.
- The field changes no arithmetic other than the comparison. Error ratio, burn rate, error budget and objective keep their definitions: a bad sample is a bad sample whichever side it fell on.
- Absent means `below`. A validator MUST treat a missing `good_when` as `below`; it MUST NOT require the field.

### 3.3 Two examples

```yaml
# A ceiling. good_when: below is the default and may be omitted.
- id: api_latency_p99
  type: threshold
  query: histogram_quantile(0.99, sum by (le)(rate(http_server_request_duration_seconds_bucket{service_name="payment-service"}[5m])))
  threshold: 0.5
  unit: seconds

# A floor. Fewer than two live consumers is bad; two or more is good.
- id: settlement_consumers_active
  type: threshold
  good_when: above
  query: min(kafka_consumer_group_members{group="payment-settler"})
  threshold: 2
  unit: consumers
```

---

## 4. Compatibility

- **1.2 packs are unchanged.** No 1.2 pack contains `good_when` (the schema's `additionalProperties: false` refused it), so every existing pack validates against the 1.3 schema and means what it meant.
- **Validators treat absent as `below`.** A 1.3 validator applied to a 1.2 pack reaches the same verdict as a 1.2 validator.
- **`apiVersion` stays `observability.platform/v1`.** The manifest shape gains an optional field; nothing is renamed, removed or re-typed.
- **A 1.2 consumer reading a 1.3 pack** that uses `above` will misread the floor as a ceiling. That is the failure mode this RFC removes for consumers that upgrade; consumers that vendor the spec should refuse `good_when: above` until they implement it rather than silently invert it (see §5).

---

## 5. Downstream impact

| Consumer | What changes |
|---|---|
| Burn-rate rule generation (Observogram `tools/lib/burn-rules.mjs`) | The threshold leg becomes `> bool t` for `below` and `< bool t` for `above`; the "no direction field" warning is retired and replaced by an explicit read of `good_when`. Until then the generator should warn on `above` instead of emitting an inverted alert. |
| Dashboard threshold colouring (Observogram `tools/lib/dashboards/lib.mjs`) | Threshold SLI tiles pick `okAbove` (higher is worse) for `below` and `okBelow` (lower is worse) for `above`; the dashed SLO line is drawn at the same bound either way. |
| Studio editors | The SLI form gains a two-value direction control next to `threshold`, shown only for `threshold` and `distribution`, default `below`; the schema-driven validator already rejects the other placements. |
| Library entries that inverted a floor | Every entry that today encodes a floor as a ratio SLI or a headroom ceiling (§2.1) is rewritten to the natural quantity with `good_when: above`; the recording rule that feeds it keeps its series, only the alert comparison flips. |
| Reference and downstream packs | Observogram vendors spec 1.3 (`vendor/observability-pack-spec/v1.3/`) and teaches its generator, dashboards and editor the direction; the MQ lab then re-vendors and can express its own floors (connected channels, running listeners) directly. |

---

## 6. Alternatives considered

### 6.1 `comparison: lte | gte`

Same information, expressed as the operator applied to the good sample.

- **Pros**: one token, familiar to anyone who has written an alert rule.
- **Cons**: the reader has to work out which side is good from an operator and a field name; `lte` vs `lt` invites a debate about the bound that `good_when` settles in prose ("the bound itself is good"); an SLO review talks about ceilings and floors, not operators.
- **Verdict**: rejected in favour of `good_when`, which says what is compared and which side is good.

### 6.2 Encoding floors as ratio SLIs

Keep the spec as it is; document the `>= bool` idiom.

- **Pros**: no schema change.
- **Cons**: everything in §2.1: the pack loses the bound, the unit and the quantity; the dashboard cannot draw the floor; the compiler has to pattern-match a comparison out of PromQL to recover a sample count.
- **Verdict**: rejected. It is the workaround this RFC retires.

### 6.3 A signed threshold

Let a negative `threshold` mean a floor, or add a sign convention in the query.

- **Pros**: no new field.
- **Cons**: the sign has nothing to do with the quantity (a floor of 2 consumers is not −2 consumers); it silently breaks every reader that treats `threshold` as a number in `unit`; it cannot express a floor of 0.
- **Verdict**: rejected.

### 6.4 A per-type default (floors for `distribution`, ceilings for `threshold`)

- **Cons**: the direction is a property of the quantity, not of the SLI type; a p99 latency and a p1 throughput are both distributions.
- **Verdict**: rejected.

---

## 7. Reference implementation

The Go linter in this repository is the reference validator:

- `schema/observability-pack.schema.json` — `good_when` on `$defs/SLI` (`enum: [below, above]`, `default: below`); the existing `allOf` if/then gains `"properties": { "good_when": false }` in the `ratio` and `custom` branches, so a misplaced field is a schema violation (`/spec/slis/N/good_when: not allowed`) and a bad value is `value must be one of "below", "above"`.
- `internal/pack/pack.go` — `SLI.GoodWhen` (`json:"good_when,omitempty"`), the constants `GoodWhenBelow` / `GoodWhenAbove`, and `(SLI).EffectiveGoodWhen()`, which returns `GoodWhenBelow` when the field is absent. Downstream Go code reads the direction through the accessor, never the raw field, so the default lives in one place.
- `internal/lint/lint_test.go` — a threshold SLI with `above` validates; `sideways` fails naming the field; `good_when` on a ratio SLI fails; absent passes; the example passes.
- `examples/payment-service.pack.yaml` — `settlement_consumers_active`, a floor of two live settlement consumers, next to `consumer_freshness`, which states the default `below` explicitly.

Nothing in the linter's conformance rubric changes: tier clause 2.1 keeps counting `threshold` and `distribution` SLIs by type.

---

## Change log

| Date | Author | Change |
|---|---|---|
| 2026-09-23 | Platform Engineering | Initial text, accepted the same day (RFC-0002) |
