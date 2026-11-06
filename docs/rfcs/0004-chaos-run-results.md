# RFC-0004 — How a chaos experiment is run, and what its last run showed

| | |
|---|---|
| RFC | 0004 |
| Title | Chaos experiments: `engine: in-process \| custom`, `schedule: on-demand \| ci`, and `last_result` |
| Status | **Proposed** |
| Author | KrystalineX Platform Engineering |
| Created | 2026-11-06 |
| Target version | 1.5 |
| Discussion | issues/comments on the repo, or platform-engineering@ |

---

## 1. Summary

The spec asks every tier-1 SLO to be covered by a chaos experiment, and a failed experiment to be "recorded as failed" (§5.11). A pack can only declare an experiment run by Chaos Mesh, Litmus or Gremlin, on a daily, weekly or monthly calendar. It has nowhere to record a run.

This RFC makes three additive changes to `validation.chaos_experiments[]`:

- **`engine`** gains `in-process`, for a service that injects faults into itself, and `custom`, for any other tool.
- **`schedule`** gains `on-demand`, for an experiment a person starts, and `ci`, for one a pipeline step runs.
- **`last_result`**, optional, records the newest run: `{ at, outcome }` required, plus `environment`, `observed_mttd`, `alerts_fired` and `evidence`.

Every 1.4 pack validates unchanged and means what it meant, and `apiVersion` stays `observability.platform/v1`. Spec 1.4 becomes 1.5, an additive minor.

---

## 2. Motivation

### 2.1 Declared is not proven

The downstream compiler, Observogram, grades a service on whether recovery has been *proven by fault injection*. With 1.4 it can only check that an experiment is **declared**. A pack can say what should break and which alert should fire, but not whether anyone broke it or whether the alert fired. So a reader is forced into one of two wrong answers:

- Count a declared experiment as proof. Then a template copied from a library proves recovery.
- Never count anything. Then a team that runs its experiments every week looks the same as one that never ran them.

The spec already names the missing record: *"a chaos experiment that fails to trigger the expected alert within `expected_mttd` is recorded as failed"*. That MUST has had no field to satisfy it since 1.0.

### 2.2 Real experiments the enums refuse

The case that prompted this RFC is a service with its own fault injection. A middleware adds latency or errors on chosen routes, following a scenario table in its code: five scenarios with durations, delays, error rates and the alerts each is meant to trigger. A control API guarded by a key starts and stops them, and a chart value switches the API on. People run the scenarios by hand. None of it can be written down in 1.4:

- `engine` must be `chaos-mesh`, `litmus` or `gremlin`.
- `schedule` must be `daily`, `weekly` or `monthly`.
- The object is `additionalProperties: false`.

The faithful pack is therefore invalid. The valid one lies about the engine and the cadence.

The same holds for toxiproxy, cloud fault-injection services and load-test scripts that break a dependency on purpose. It also holds for any experiment run from a pipeline rather than a calendar.

### 2.3 Why the spec, not a downstream convention

Both readers vendor the schema and validate against it. A downstream-only field fails validation against the vendored schema. A downstream-only annotation (`metadata.annotations` is `Record<string,string>`) is invisible to every other consumer and cannot be validated. As in RFC-0002 §2.2 and RFC-0003 §2.3, the gap is in the contract, so the fix is in the contract.

---

## 3. Detailed design

### 3.1 The fields

```yaml
validation:
  chaos_experiments:
    - id: api-latency-spike
      engine: in-process               # NEW values: in-process | custom
      target: payment-api
      steady_state_hypothesis: ref:slos.checkout_latency_99_5_p99_300ms
      fault: { kind: latency, duration: 180s, delay_ms: 5000 }   # fault was already open
      expected_alerts: [ checkout-latency-burn-fast ]
      expected_mttd: 6m
      schedule: on-demand              # NEW values: on-demand | ci
      last_result:                     # NEW, optional: the newest recorded run
        at: "2026-10-28T09:40:00Z"
        outcome: pass
        environment: staging
        observed_mttd: 4m12s
        alerts_fired: [ checkout-latency-burn-fast ]
        evidence: https://ci.example/runs/8812
```

| Field | Type | Required | Meaning |
|---|---|---|---|
| `engine` | enum | yes (unchanged) | Adds `in-process` (the service injects the fault through its own code) and `custom` (a tool the enum does not name). |
| `schedule` | enum | yes (unchanged) | Adds `on-demand` (a person starts it) and `ci` (a pipeline step runs it). |
| `last_result` | `ChaosRunResult` | no | The newest recorded run. |
| `last_result.at` | string, `date-time` | yes | When the run started, in RFC 3339. Quote it in YAML so a YAML 1.1 reader keeps it a string. |
| `last_result.outcome` | `pass` \| `fail` \| `aborted` | yes | `pass`: every expected alert fired within `expected_mttd`, and the steady state held or recovered as the hypothesis says. `fail`: one of those did not happen. `aborted`: the run stopped before it could tell. |
| `last_result.environment` | `Slug` | no | Where it ran, if not the experiment's `environment`. |
| `last_result.observed_mttd` | `Duration` | no | Time from the injection to the first expected alert firing. |
| `last_result.alerts_fired` | string[] | no | The expected alerts that fired, in the alerting engine's spelling. |
| `last_result.evidence` | string | no | A run id, a link or a file. It is never used for matching. |

### 3.2 Decisions, and why

**The newest run only, not a history.** A pack is a manifest that is reviewed and versioned. A run history grows with every run and would turn each run into a commit. The history belongs to whatever measured it, such as a certification harness, a CI log or the engine's own records, and `evidence` points at it.

**`at` and `outcome` are required, the rest is optional.** A run without a time cannot be weighed against the experiment's cadence, and a run without an outcome records nothing. The other fields are useful when the runner has them, and many runners do not.

**`aborted` is an outcome of its own.** A run stopped by a guardrail or by a person neither proved nor disproved the loop. Folding it into `fail` would make a guardrail look like a broken alert. Folding it into `pass` would be false.

**The cadence clauses do not count `on-demand` or `ci`.** Clause 2.8 ("monthly+") and clause 1.9 ("weekly in production") read `schedule`. An on-demand or CI experiment says nothing about how often it runs, so it never satisfies a cadence clause by declaration. The reference implementation needs no change for this, since it counts the three calendar values by name. Whether a recent `last_result` should count instead is left open (§8).

**The required fields of an experiment stay required.** L5 is the chain "names the SLO it stresses and the alert it expects" (§3). Relaxing `steady_state_hypothesis`, `expected_alerts` or `expected_mttd` would make a valid experiment that proves nothing.

**No new fault vocabulary.** `fault` is already `additionalProperties: true`, so delays, error rates, routes and phases fit it today.

---

## 4. Compatibility

- **1.4 packs are unchanged.** No 1.4 pack can carry `last_result` or the new enum values, because the 1.4 schema refuses them. So every existing pack validates against 1.5 and keeps its meaning.
- **`apiVersion` stays `observability.platform/v1`.** Two enums widen and an optional object is added. Nothing is renamed, removed or re-typed.
- **A 1.4 consumer reading a 1.5 pack** that uses the additions refuses it when it validates against 1.4, until it vendors 1.5. A consumer that reads without validating ignores `last_result` as an unknown optional key. It may see an `engine` or `schedule` value it does not know, and SHOULD treat that as "not one of mine": an `in-process` experiment is not a Chaos Mesh resource to apply.
- **Why 1.5.** An optional field and widened enums on an existing object are the same kind of change RFC-0002 and RFC-0003 made, so this is a minor bump. The 2026-11-01 revision stayed inside 1.4 because it only widened a pattern. This RFC adds a field, so it opens 1.5.

---

## 5. What readers must do

- **Validators** accept the new values and `last_result`, and refuse an unknown key inside `last_result`.
- **Chaos operators** that apply experiments (Chaos Mesh, Litmus) skip `in-process` and `custom` entries. They are not theirs to run.
- **Graders** that weigh `last_result` SHOULD weigh its age against `schedule`. A pass from seven months ago is not evidence for a weekly experiment.
- **Writers** of `last_result` record the newest run and replace the previous one. They keep the history where they measured it.

---

## 6. Alternatives considered

### 6.1 `validation.chaos_runs[]`, a run history in the pack

This was rejected for the reasons in §3.2. A history grows without bound, every run becomes a pack change, and the pack's review flow is the wrong place for a run ledger.

### 6.2 Relaxing `required` to `[id, engine, target]`

This would let a repository scan emit an experiment whose expected alert it cannot find. But it would weaken L5 for every pack. A reader that cannot fill a field should fill it from the best evidence it has, and say so in its own annotations.

### 6.3 `engine` as the open `Product` pattern

An open string would admit every tool without a new RFC. But a chaos operator could no longer tell its own experiments from someone else's. `custom` covers the long tail, and a named engine can join the enum when its operator needs to recognise it.

### 6.4 Annotations only

Annotations are string-valued and never validated, and other readers would not see them. This is the same argument as RFC-0003 §6.3.

---

## 7. Reference implementation

- **Schema:** `$defs/Validation.chaos_experiments.items` widens `engine` and `schedule` and adds `last_result`, a `$ref` to the new `$defs/ChaosRunResult`.
- **Spec:** §5.11 gains a section on how an experiment is run and what its last run showed. Its "recorded as failed" MUST now names `last_result.outcome: fail`. The §9 mapping row names in-process injection code. The header table and §1.3 say 1.5.
- **Example:** `api-pod-kill` in `examples/payment-service.pack.yaml` records its newest run. The other two experiments stay 1.4-shaped.
- **Go:**
  - `pack.Validation.ChaosExperiments` is already `[]map[string]any`, so it needs no change.
  - `internal/lint/chaos_test.go` covers the example's run; the new engine and schedule values; the refused values, listed in the finding; `at` and `outcome` required; an unknown outcome or key refused; and `on-demand` and `ci` failing the monthly cadence clause.
  - `packlint` reports the same 13 findings on the example as before.
- **Maturity model:** `docs/maturity-model.md` is unchanged. No clause reads `last_result`, so no pack's tier moves.

---

## 8. Open questions for review

1. Should a passing `last_result` within the cadence window count for clauses 2.8 and 1.9 when `schedule` is `on-demand` or `ci`? This RFC says no, and leaves the question for a later revision.
2. Should the spec name a metric family that harnesses publish run results under, so a live reader can find runs without a pack change? Today only one downstream lab publishes such a feed.

---

## Change log

- 2026-11-06: proposed.
