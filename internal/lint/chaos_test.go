package lint_test

import (
	"strings"
	"testing"

	"github.com/example/observability-pack/internal/lint"
	"github.com/example/observability-pack/internal/pack"
)

// rawChaos returns the raw validation.chaos_experiments entry with the given
// id. The map is shared with p.Raw, so edits reach the schema pass.
func rawChaos(t *testing.T, p *pack.Pack, id string) map[string]any {
	t.Helper()
	spec, _ := p.Raw["spec"].(map[string]any)
	validation, _ := spec["validation"].(map[string]any)
	list, _ := validation["chaos_experiments"].([]any)
	for _, e := range list {
		if m, ok := e.(map[string]any); ok && m["id"] == id {
			return m
		}
	}
	t.Fatalf("no chaos experiment %q in the example", id)
	return nil
}

// Spec 1.5: the example records the newest run of api-pod-kill, and a 1.4
// experiment (no last_result) still validates beside it.
func TestExampleRecordsAChaosRun(t *testing.T) {
	p, schema := loadExample(t)
	got := rawChaos(t, p, "api-pod-kill")["last_result"]
	lr, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("api-pod-kill should carry a last_result; got %v", got)
	}
	if lr["outcome"] != "pass" || lr["at"] == nil {
		t.Errorf("last_result needs at and outcome: pass; got %v", lr)
	}
	if _, ok := lr["at"].(string); !ok {
		t.Errorf("last_result.at must stay a string after YAML decoding; got %T", lr["at"])
	}
	if _, has := rawChaos(t, p, "api-network-latency")["last_result"]; has {
		t.Error("api-network-latency is meant to show a 1.4-shaped experiment, with no last_result")
	}
	if r := runSchema(t, p, schema); !r.SchemaOK {
		t.Errorf("the example must validate; findings: %v", r.Findings)
	}
}

func TestChaosInProcessOnDemandValidates(t *testing.T) {
	p, schema := loadExample(t)
	exp := rawChaos(t, p, "api-network-latency")
	for _, engine := range []string{"in-process", "custom"} {
		for _, schedule := range []string{"on-demand", "ci"} {
			exp["engine"], exp["schedule"] = engine, schedule
			if r := runSchema(t, p, schema); !r.SchemaOK {
				t.Errorf("engine %s, schedule %s must validate; findings: %v", engine, schedule, r.Findings)
			}
		}
	}
}

func TestChaosBadEngineAndScheduleFailListingTheValues(t *testing.T) {
	p, schema := loadExample(t)
	exp := rawChaos(t, p, "api-pod-kill")
	exp["engine"] = "toxiproxy"
	r := runSchema(t, p, schema)
	f := findingUnder(r, "/spec/validation/chaos_experiments/0/engine")
	if r.SchemaOK || f == nil {
		t.Fatalf("engine: toxiproxy must fail at its path; got %v", r.Findings)
	}
	for _, v := range []string{"chaos-mesh", "in-process", "custom"} {
		if !strings.Contains(f.Message, v) {
			t.Errorf("finding should list %q among the engines; got %q", v, f.Message)
		}
	}
	exp["engine"] = "chaos-mesh"
	exp["schedule"] = "hourly"
	r = runSchema(t, p, schema)
	f = findingUnder(r, "/spec/validation/chaos_experiments/0/schedule")
	if r.SchemaOK || f == nil || !strings.Contains(f.Message, "on-demand") {
		t.Errorf("schedule: hourly must fail listing on-demand; got %v", r.Findings)
	}
}

func TestChaosRunResultRequiresAtAndOutcome(t *testing.T) {
	for _, field := range []string{"at", "outcome"} {
		p, schema := loadExample(t)
		lr := rawChaos(t, p, "api-pod-kill")["last_result"].(map[string]any)
		delete(lr, field)
		r := runSchema(t, p, schema)
		f := findingUnder(r, "/spec/validation/chaos_experiments/0/last_result")
		if r.SchemaOK || f == nil || !strings.Contains(f.Message, field) {
			t.Errorf("a last_result without %s must fail naming it; got %v", field, r.Findings)
		}
	}
}

func TestChaosRunResultRefusesUnknownOutcomeAndProperty(t *testing.T) {
	p, schema := loadExample(t)
	lr := rawChaos(t, p, "api-pod-kill")["last_result"].(map[string]any)
	lr["outcome"] = "passed"
	if r := runSchema(t, p, schema); r.SchemaOK || findingUnder(r, "/spec/validation/chaos_experiments/0/last_result/outcome") == nil {
		t.Errorf("outcome: passed must fail at its path; got %v", r.Findings)
	}
	lr["outcome"] = "fail"
	lr["history"] = []any{}
	if r := runSchema(t, p, schema); r.SchemaOK || findingUnder(r, "/spec/validation/chaos_experiments/0/last_result") == nil {
		t.Errorf("an unknown property on last_result must fail (additionalProperties: false); got %v", r.Findings)
	}
}

// on-demand and ci say nothing about how often an experiment runs, so they
// never satisfy the "monthly+" cadence clause (tier-2 2.8) by declaration.
func TestChaosOnDemandDoesNotMeetTheMonthlyCadence(t *testing.T) {
	check := func(schedule string) bool {
		p := &pack.Pack{
			Metadata: pack.Metadata{Bindings: pack.Bindings{Criticality: "tier-2"}},
			Spec: pack.Spec{Validation: pack.Validation{ChaosExperiments: []map[string]any{
				{"id": "x", "schedule": schedule, "steady_state_hypothesis": "ref:slos.a"},
			}}},
		}
		r := &lint.Result{}
		lint.Conformance(p, r)
		for _, c := range r.Conformance.Tier2 {
			if c.ID == "2.8" {
				return c.Pass
			}
		}
		t.Fatal("tier-2 rubric has no clause 2.8")
		return false
	}
	if !check("monthly") {
		t.Error("a monthly experiment with a hypothesis must pass 2.8")
	}
	for _, s := range []string{"on-demand", "ci"} {
		if check(s) {
			t.Errorf("schedule %s must not pass the monthly cadence clause", s)
		}
	}
}
