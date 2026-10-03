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
	// The placement rule is `"good_when": { "not": {} }` in the ratio branch;
	// santhosh-tekuri v5 reports a value caught by a `not` sub-schema as
	// "not failed". Pinned like the enum message above, so a change of
	// mechanism is a visible change.
	if f.Message != "not failed" {
		t.Errorf("message must say the not-subschema refused the field; got %q", f.Message)
	}
}

// The custom branch carries the same placement rule as the ratio branch;
// the example has no custom SLI, so one is made from api_availability.
func TestGoodWhenOnCustomFails(t *testing.T) {
	p, schema := loadExample(t)
	s := rawSLI(t, p, "api_availability")
	s["type"] = "custom"
	s["expression"] = "1"
	s["good_when"] = "below"
	r := runSchema(t, p, schema)
	if r.SchemaOK {
		t.Fatal("good_when on a custom SLI must fail the schema, even with a valid value")
	}
	f := findingOnGoodWhen(r)
	if f == nil {
		t.Fatalf("expected a finding naming good_when on the custom SLI; got %v", r.Findings)
	}
	if !strings.HasPrefix(f.Path, "/spec/slis/") {
		t.Errorf("finding must point into spec.slis; got path %q", f.Path)
	}
	if f.Message != "not failed" {
		t.Errorf("message must say the not-subschema refused the field; got %q", f.Message)
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

// The example shows good_when both ways: one floor (above) and one SLI that
// states the default (below) explicitly, each covered by an SLO and a
// burn-rate policy entry like every other SLI in the file.
func TestExampleDeclaresAFloorAndAnExplicitCeiling(t *testing.T) {
	p, _ := loadExample(t)
	floor := typedSLI(t, p, "settlement_consumers_active")
	if floor.Type != "threshold" || floor.GoodWhen != pack.GoodWhenAbove || floor.EffectiveGoodWhen() != pack.GoodWhenAbove {
		t.Errorf("settlement_consumers_active must be a threshold SLI with good_when: above; got type=%q good_when=%q", floor.Type, floor.GoodWhen)
	}
	if floor.Threshold != 2 || floor.Unit != "consumers" {
		t.Errorf("settlement_consumers_active must be a floor of 2 consumers; got threshold=%v unit=%q", floor.Threshold, floor.Unit)
	}
	if ceiling := typedSLI(t, p, "consumer_freshness"); ceiling.GoodWhen != pack.GoodWhenBelow {
		t.Errorf("consumer_freshness must state good_when: below explicitly; got %q", ceiling.GoodWhen)
	}
	sloID := ""
	for _, s := range p.Spec.SLOs {
		if s.SLI == floor.ID {
			sloID = s.ID
		}
	}
	if sloID == "" {
		t.Fatal("no SLO covers settlement_consumers_active")
	}
	covered := false
	for _, br := range p.Spec.Policy.BurnRateAlerts {
		if br.SLO == sloID && len(br.Windows) >= 2 {
			covered = true
		}
	}
	if !covered {
		t.Errorf("no burn-rate policy entry with two windows for %s", sloID)
	}
}

func TestEffectiveGoodWhen(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", pack.GoodWhenBelow},
		{"below", pack.GoodWhenBelow},
		{"above", pack.GoodWhenAbove},
		// The accessor supplies the default, not validation: an invalid value
		// comes back unchanged. lint.Schema is the gate; the operator path,
		// which runs only lint.Refs, must check the result itself.
		{"sideways", "sideways"},
	} {
		if got := (pack.SLI{GoodWhen: tc.in}).EffectiveGoodWhen(); got != tc.want {
			t.Errorf("EffectiveGoodWhen(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// rawAlerting returns the raw (schema-side) spec.alerting block. The map is
// shared with p.Raw, so edits reach the schema pass.
func rawAlerting(t *testing.T, p *pack.Pack) map[string]any {
	t.Helper()
	spec, _ := p.Raw["spec"].(map[string]any)
	alerting, ok := spec["alerting"].(map[string]any)
	if !ok {
		t.Fatal("the example has no spec.alerting block")
	}
	return alerting
}

// rawAlertRule returns the raw alerting.rules entry with the given name.
func rawAlertRule(t *testing.T, p *pack.Pack, name string) map[string]any {
	t.Helper()
	rules, _ := rawAlerting(t, p)["rules"].([]any)
	for _, r := range rules {
		if m, ok := r.(map[string]any); ok && m["name"] == name {
			return m
		}
	}
	t.Fatalf("no alerting rule %q in the example", name)
	return nil
}

// findingUnder returns the first schema finding whose instance path has the
// given prefix, or nil.
func findingUnder(r *lint.Result, prefix string) *lint.Finding {
	for i := range r.Findings {
		if strings.HasPrefix(r.Findings[i].Path, prefix) {
			return &r.Findings[i]
		}
	}
	return nil
}

// Spec 1.4: the example declares three operational rules under
// alerting.rules — two Prometheus rules (one with the default engine stated,
// one leaving it absent) and one Grafana-managed rule — and none of them is a
// burn-rate alert (none names an SLO of the pack).
func TestExampleDeclaresOperationalAlertRules(t *testing.T) {
	p, _ := loadExample(t)
	rules := p.Spec.Alerting.Rules
	if len(rules) != 3 {
		t.Fatalf("example alerting.rules: got %d, want 3", len(rules))
	}
	want := map[string]string{
		"PaymentServicePodRestarting":      pack.AlertEnginePrometheus,
		"PaymentDbConnectionPoolSaturated": pack.AlertEnginePrometheus,
		"PaymentCertificateExpiringSoon":   pack.AlertEngineGrafana,
	}
	for _, r := range rules {
		engine, ok := want[r.Name]
		if !ok {
			t.Errorf("unexpected rule %q", r.Name)
			continue
		}
		if r.EffectiveEngine() != engine {
			t.Errorf("%s: EffectiveEngine() = %q, want %q", r.Name, r.EffectiveEngine(), engine)
		}
		if r.Expr == "" || r.Severity == "" || r.For == "" || r.Labels["severity"] == "" || r.Source == "" {
			t.Errorf("%s: the example states expr, severity, for, labels.severity and source; got %+v", r.Name, r)
		}
		for _, slo := range p.Spec.SLOs {
			if strings.Contains(r.Expr, slo.ID) || r.Labels["slo"] == slo.ID {
				t.Errorf("%s names SLO %s: a burn-rate alert belongs in policy.burn_rate_alerts, not alerting.rules", r.Name, slo.ID)
			}
		}
	}
	if typed := p.Spec.Alerting.Rules[1]; typed.Engine != "" {
		t.Errorf("PaymentDbConnectionPoolSaturated must leave engine absent to show the default; got %q", typed.Engine)
	}
}

// A 1.3-shaped alerting block (routes, dedup, suppress — no rules) still
// validates, and so does an empty rules array.
func TestAlertingWithoutRulesValidates(t *testing.T) {
	p, schema := loadExample(t)
	alerting := rawAlerting(t, p)
	delete(alerting, "rules")
	if r := runSchema(t, p, schema); !r.SchemaOK {
		t.Errorf("alerting without rules (1.3 shape) must validate; findings: %v", r.Findings)
	}
	alerting["rules"] = []any{}
	if r := runSchema(t, p, schema); !r.SchemaOK {
		t.Errorf("alerting with rules: [] must validate; findings: %v", r.Findings)
	}
}

func TestAlertRuleWithoutExprFailsNamingTheField(t *testing.T) {
	p, schema := loadExample(t)
	delete(rawAlertRule(t, p, "PaymentServicePodRestarting"), "expr")
	r := runSchema(t, p, schema)
	if r.SchemaOK {
		t.Fatal("an alerting rule without expr must fail")
	}
	f := findingUnder(r, "/spec/alerting/rules/0")
	if f == nil || !strings.Contains(f.Message, "expr") {
		t.Errorf("want a finding under /spec/alerting/rules/0 naming expr; got %v", r.Findings)
	}
}

func TestAlertRuleWithoutNameFails(t *testing.T) {
	p, schema := loadExample(t)
	delete(rawAlertRule(t, p, "PaymentServicePodRestarting"), "name")
	r := runSchema(t, p, schema)
	if r.SchemaOK {
		t.Fatal("an alerting rule without name must fail")
	}
	if f := findingUnder(r, "/spec/alerting/rules/0"); f == nil || !strings.Contains(f.Message, "name") {
		t.Errorf("want a finding under /spec/alerting/rules/0 naming name; got %v", r.Findings)
	}
}

func TestAlertRuleBadEngineFailsListingTheValues(t *testing.T) {
	p, schema := loadExample(t)
	rawAlertRule(t, p, "PaymentServicePodRestarting")["engine"] = "nagios"
	r := runSchema(t, p, schema)
	if r.SchemaOK {
		t.Fatal("engine: nagios must fail")
	}
	f := findingUnder(r, "/spec/alerting/rules/0/engine")
	if f == nil {
		t.Fatalf("want a finding at /spec/alerting/rules/0/engine; got %v", r.Findings)
	}
	for _, v := range []string{"prometheus", "loki", "grafana", "alertmanager"} {
		if !strings.Contains(f.Message, v) {
			t.Errorf("finding should list %q among the allowed engines; got %q", v, f.Message)
		}
	}
}

func TestAlertRuleBadSeverityAndUnknownPropertyFail(t *testing.T) {
	p, schema := loadExample(t)
	rule := rawAlertRule(t, p, "PaymentServicePodRestarting")
	rule["severity"] = "critical" // the engine's label, not the pack's SEV1..SEV4
	if r := runSchema(t, p, schema); r.SchemaOK || findingUnder(r, "/spec/alerting/rules/0/severity") == nil {
		t.Errorf("severity: critical must fail at /spec/alerting/rules/0/severity; got %v", r.Findings)
	}
	rule["severity"] = "SEV3"
	rule["keep_firing_for"] = "5m"
	if r := runSchema(t, p, schema); r.SchemaOK || findingUnder(r, "/spec/alerting/rules/0") == nil {
		t.Errorf("an unknown property on a rule must fail (additionalProperties: false); got %v", r.Findings)
	}
}

func TestEffectiveEngine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", pack.AlertEnginePrometheus},
		{"prometheus", pack.AlertEnginePrometheus},
		{"loki", pack.AlertEngineLoki},
		{"grafana", pack.AlertEngineGrafana},
		// The accessor supplies the default, not validation (as EffectiveGoodWhen).
		{"nagios", "nagios"},
	} {
		if got := (pack.AlertRule{Engine: tc.in}).EffectiveEngine(); got != tc.want {
			t.Errorf("EffectiveEngine(%q) = %q, want %q", tc.in, got, tc.want)
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
