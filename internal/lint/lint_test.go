package lint_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/observability-pack/internal/lint"
	"github.com/example/observability-pack/internal/pack"
)

// repoRoot resolves paths relative to the repo root regardless of where
// `go test` is invoked from. The test file lives at internal/lint/.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	return root
}

// loadExample loads the reference pack and returns it with the schema path,
// so a test can edit the raw document and run the schema pass on the result
// exactly as packlint would.
func loadExample(t *testing.T) (*pack.Pack, string) {
	t.Helper()
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return p, filepath.Join(root, "schema", "observability-pack.schema.json")
}

// rawSLI returns the raw (schema-side) document of the SLI with the given id.
// The map is shared with p.Raw, so edits reach the schema pass.
func rawSLI(t *testing.T, p *pack.Pack, id string) map[string]any {
	t.Helper()
	spec, _ := p.Raw["spec"].(map[string]any)
	slis, _ := spec["slis"].([]any)
	for _, s := range slis {
		if m, ok := s.(map[string]any); ok && m["id"] == id {
			return m
		}
	}
	t.Fatalf("no SLI %q in the example", id)
	return nil
}

// typedSLI returns the parsed (Go-side) SLI with the given id.
func typedSLI(t *testing.T, p *pack.Pack, id string) pack.SLI {
	t.Helper()
	for _, s := range p.Spec.SLIs {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no SLI %q in the example", id)
	return pack.SLI{}
}

func runSchema(t *testing.T, p *pack.Pack, schemaPath string) *lint.Result {
	t.Helper()
	r := &lint.Result{}
	if err := lint.Schema(p, schemaPath, r); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return r
}

// findingOnGoodWhen returns the schema finding whose instance path ends in
// /good_when, i.e. the one that names the field, or nil.
func findingOnGoodWhen(r *lint.Result) *lint.Finding {
	for i := range r.Findings {
		if strings.HasSuffix(r.Findings[i].Path, "/good_when") {
			return &r.Findings[i]
		}
	}
	return nil
}

func TestGoodWhenAboveOnThresholdValidates(t *testing.T) {
	p, schema := loadExample(t)
	rawSLI(t, p, "api_latency_p99")["good_when"] = "above"
	if r := runSchema(t, p, schema); !r.SchemaOK {
		t.Errorf("threshold SLI with good_when: above must validate; findings: %v", r.Findings)
	}
}

func TestGoodWhenAboveOnDistributionValidates(t *testing.T) {
	p, schema := loadExample(t)
	s := rawSLI(t, p, "api_latency_p99")
	s["type"] = "distribution"
	s["percentile"] = 0.99
	s["good_when"] = "above"
	if r := runSchema(t, p, schema); !r.SchemaOK {
		t.Errorf("distribution SLI with good_when: above must validate; findings: %v", r.Findings)
	}
}

func TestGoodWhenBadValueFailsNamingTheField(t *testing.T) {
	p, schema := loadExample(t)
	rawSLI(t, p, "api_latency_p99")["good_when"] = "sideways"
	r := runSchema(t, p, schema)
	if r.SchemaOK {
		t.Fatal("good_when: sideways must fail the schema")
	}
	f := findingOnGoodWhen(r)
	if f == nil {
		t.Fatalf("expected a finding naming good_when; got %v", r.Findings)
	}
	if f.Severity != lint.SeverityError || f.Code != "schema/violation" {
		t.Errorf("finding must be an error schema/violation; got %+v", *f)
	}
	if !strings.Contains(f.Message, `"below"`) || !strings.Contains(f.Message, `"above"`) {
		t.Errorf("message must name the two allowed values; got %q", f.Message)
	}
}

func TestGoodWhenOnRatioFails(t *testing.T) {
	p, schema := loadExample(t)
	rawSLI(t, p, "api_availability")["good_when"] = "below"
	r := runSchema(t, p, schema)
	if r.SchemaOK {
		t.Fatal("good_when on a ratio SLI must fail the schema, even with a valid value")
	}
	f := findingOnGoodWhen(r)
	if f == nil {
		t.Fatalf("expected a finding naming good_when on the ratio SLI; got %v", r.Findings)
	}
	if !strings.HasPrefix(f.Path, "/spec/slis/") {
		t.Errorf("finding must point into spec.slis; got path %q", f.Path)
	}
}

func TestGoodWhenAbsentPassesAndReadsAsBelow(t *testing.T) {
	p, schema := loadExample(t)
	if _, declared := rawSLI(t, p, "api_latency_p99")["good_when"]; declared {
		t.Fatal("test premise: the example's api_latency_p99 must not declare good_when")
	}
	if r := runSchema(t, p, schema); !r.SchemaOK {
		t.Errorf("a threshold SLI without good_when must validate; findings: %v", r.Findings)
	}
	s := typedSLI(t, p, "api_latency_p99")
	if s.GoodWhen != "" {
		t.Errorf("raw field must stay empty when absent; got %q", s.GoodWhen)
	}
	if got := s.EffectiveGoodWhen(); got != pack.GoodWhenBelow {
		t.Errorf("absent good_when must read as %q; got %q", pack.GoodWhenBelow, got)
	}
}

func TestEffectiveGoodWhen(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", pack.GoodWhenBelow},
		{"below", pack.GoodWhenBelow},
		{"above", pack.GoodWhenAbove},
	} {
		if got := (pack.SLI{GoodWhen: tc.in}).EffectiveGoodWhen(); got != tc.want {
			t.Errorf("EffectiveGoodWhen(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPaymentServiceExampleLoads(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.Metadata.Name != "payment-service" {
		t.Fatalf("name: got %q", p.Metadata.Name)
	}
	if p.Metadata.Bindings.Criticality != "tier-1" {
		t.Fatalf("criticality: got %q", p.Metadata.Bindings.Criticality)
	}
	if got := len(p.Spec.SLIs); got < 1 {
		t.Fatalf("expected ≥1 SLI, got %d", got)
	}
}

func TestSchemaPassesOnExample(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := &lint.Result{}
	if err := lint.Schema(p, filepath.Join(root, "schema", "observability-pack.schema.json"), r); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if !r.SchemaOK {
		t.Errorf("expected schema to pass; findings: %v", r.Findings)
	}
}

func TestConformanceProducesAllTiersForTier1(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := &lint.Result{Criticality: p.Metadata.Bindings.Criticality}
	lint.Conformance(p, r)
	if r.TierTarget != "tier-1" {
		t.Fatalf("tier_target: got %q", r.TierTarget)
	}
	if len(r.Conformance.Tier3) == 0 || len(r.Conformance.Tier2) == 0 || len(r.Conformance.Tier1) == 0 {
		t.Fatalf("expected all three tier rubrics, got 3=%d 2=%d 1=%d",
			len(r.Conformance.Tier3), len(r.Conformance.Tier2), len(r.Conformance.Tier1))
	}
}

func TestBackendsPassIsCleanOnExample(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := &lint.Result{}
	lint.Backends(p, r)
	for _, f := range r.Findings {
		if f.Severity == lint.SeverityError {
			t.Errorf("unexpected backends error: [%s] %s %s", f.Code, f.Path, f.Message)
		}
	}
}

func TestBackendsFlagsRegistryVersionAndRefs(t *testing.T) {
	p := &pack.Pack{
		Spec: pack.Spec{
			Telemetry: pack.Telemetry{
				Backends: []pack.Backend{
					{ID: "m1", Signal: "metrics", Product: "prometheus",
						Version: &pack.VersionSpec{Declared: "2.40", Min: "2.53", Gating: "enforce"}},
					{ID: "x1", Signal: "logs", Product: "notareal",
						Version: &pack.VersionSpec{Declared: "1.0", Min: "0.5", Gating: "warn"}},
				},
			},
			Profiling: &pack.SignalBlock{Backend: "does-not-exist"},
		},
	}
	r := &lint.Result{}
	lint.Backends(p, r)

	want := map[string]lint.Severity{
		"versions/below_min":       lint.SeverityError, // enforce -> error
		"registry/unknown_product": lint.SeverityWarn,
		"backends/unresolved_ref":  lint.SeverityError,
	}
	got := map[string]lint.Severity{}
	for _, f := range r.Findings {
		got[f.Code] = f.Severity
	}
	for code, sev := range want {
		if got[code] != sev {
			t.Errorf("expected finding %q at severity %q; got %q", code, sev, got[code])
		}
	}
}

func TestBackendsGatingOffSkipsVersionCheck(t *testing.T) {
	p := &pack.Pack{
		Spec: pack.Spec{
			Telemetry: pack.Telemetry{
				Backends: []pack.Backend{
					{ID: "p1", Signal: "profiles", Product: "pyroscope",
						Version: &pack.VersionSpec{Declared: "0.1", Min: "9.9", Gating: "off"}},
				},
			},
		},
	}
	r := &lint.Result{}
	lint.Backends(p, r)
	for _, f := range r.Findings {
		if f.Code == "versions/below_min" {
			t.Errorf("gating=off must skip version check, but got %s", f.Message)
		}
	}
}
