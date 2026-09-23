// Package pack defines a loose Go representation of an ObservabilityPack
// manifest, sufficient for the lint / conformance checks. Sections the
// linter does not yet reason about are kept as raw map[string]any so the
// type stays forward-compatible with schema additions.
package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// Pack is the in-memory shape of a parsed observability-pack manifest.
type Pack struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   Metadata       `json:"metadata"`
	Spec       Spec           `json:"spec"`
	Raw        map[string]any `json:"-"` // original document, for schema validation
}

type Metadata struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Binding     string            `json:"binding,omitempty"`
	Owners      []string          `json:"owners"`
	Imports     []Import          `json:"imports,omitempty"`
	Bindings    Bindings          `json:"bindings"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type Bindings struct {
	Service      string   `json:"service"`
	Environments []string `json:"environments"`
	Criticality  string   `json:"criticality"`
}

type Import struct {
	Ref  string         `json:"ref"`
	With map[string]any `json:"with,omitempty"`
}

type Spec struct {
	Otel         Otel                   `json:"otel"`
	SLIs         []SLI                  `json:"slis"`
	SLOs         []SLO                  `json:"slos"`
	Telemetry    Telemetry              `json:"telemetry,omitempty"`
	Environments map[string]Environment `json:"environments,omitempty"`
	Pipelines    Pipelines              `json:"pipelines"`
	Storage      map[string]any         `json:"storage,omitempty"`
	Queries      Queries                `json:"queries"`
	Dashboards   []Dashboard            `json:"dashboards"`
	Profiling    *SignalBlock           `json:"profiling,omitempty"`
	Network      *SignalBlock           `json:"network,omitempty"`
	PolicyEngine *SignalBlock           `json:"policy_engine,omitempty"`
	Mesh         []Component            `json:"mesh,omitempty"`
	Collection   []Component            `json:"collection,omitempty"`
	Policy       Policy                 `json:"policy"`
	Alerting     Alerting               `json:"alerting"`
	Remediation  []Remediation          `json:"remediation,omitempty"`
	Baselines    Baselines              `json:"baselines"`
	Validation   Validation             `json:"validation"`
}

// Telemetry is the generalized, multi-product backend catalog. Each backend
// names a product, a signal class, and an optional version/gating policy.
type Telemetry struct {
	Backends []Backend `json:"backends,omitempty"`
}

// Backend is one named, versioned telemetry backend instance.
type Backend struct {
	ID        string           `json:"id"`
	Signal    string           `json:"signal"`
	Product   string           `json:"product"`
	Version   *VersionSpec     `json:"version,omitempty"`
	Endpoints []string         `json:"endpoints,omitempty"`
	Instances []map[string]any `json:"instances,omitempty"`
	Auth      map[string]any   `json:"auth,omitempty"`
	Tenant    string           `json:"tenant,omitempty"`
	Default   bool             `json:"default,omitempty"`
}

// VersionSpec captures the declared product version plus an optional gating
// policy, mirroring the MCP server's capability/version model.
type VersionSpec struct {
	Declared     string   `json:"declared,omitempty"`
	Min          string   `json:"min,omitempty"`
	Max          string   `json:"max,omitempty"`
	Gating       string   `json:"gating,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Environment is a named deployment overlay (prod, staging, ...).
type Environment struct {
	Target       string            `json:"target,omitempty"`
	Criticality  string            `json:"criticality,omitempty"`
	Backends     map[string]string `json:"backends,omitempty"`
	Overrides    map[string]any    `json:"overrides,omitempty"`
	Suppress     []string          `json:"suppress,omitempty"`
	PromoteAfter string            `json:"promote_after,omitempty"`
}

// SignalBlock is a thin reference from a signal dimension (profiling, network,
// policy_engine) to a telemetry backend, with an optional version policy.
type SignalBlock struct {
	Backend string       `json:"backend,omitempty"`
	Product string       `json:"product,omitempty"`
	Version *VersionSpec `json:"version,omitempty"`
}

// Component is a mesh or collection-pipeline element.
type Component struct {
	Product string       `json:"product"`
	Role    string       `json:"role,omitempty"`
	Backend string       `json:"backend,omitempty"`
	Version *VersionSpec `json:"version,omitempty"`
}

type Otel struct {
	SemConv            string             `json:"semconv"`
	ResourceAttributes ResourceAttributes `json:"resource_attributes"`
	SDK                SDK                `json:"sdk"`
}

type ResourceAttributes struct {
	Required []string `json:"required"`
	Custom   []string `json:"custom,omitempty"`
}

type SDK struct {
	Languages      []string       `json:"languages"`
	Sampling       map[string]any `json:"sampling"`
	Propagators    []string       `json:"propagators,omitempty"`
	LogCorrelation bool           `json:"log_correlation,omitempty"`
}

type SLI struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Description   string  `json:"description,omitempty"`
	Owner         string  `json:"owner,omitempty"`
	SemConvMetric string  `json:"semconv_metric,omitempty"`
	Good          string  `json:"good,omitempty"`
	Total         string  `json:"total,omitempty"`
	Query         string  `json:"query,omitempty"`
	Threshold     float64 `json:"threshold,omitempty"`
	GoodWhen      string  `json:"good_when,omitempty"` // spec 1.3: below | above; read it through EffectiveGoodWhen
	Unit          string  `json:"unit,omitempty"`
	Percentile    float64 `json:"percentile,omitempty"`
	Expression    string  `json:"expression,omitempty"`
}

// Values of SLI.GoodWhen, the direction of a threshold or distribution
// SLI's bound (spec 1.3, §5.1).
const (
	GoodWhenBelow = "below" // a sample is good at or below the bound: latency, lag, error rate (the default)
	GoodWhenAbove = "above" // a sample is good at or above the bound, a floor: replicas, capacity, consumers
)

// EffectiveGoodWhen returns the direction of the SLI's bound, reading an
// absent good_when as GoodWhenBelow: spec 1.3 says absent means below, which
// is the only meaning a 1.2 pack could express. Downstream code reads the
// direction through this accessor, never the raw field, so the default lives
// in one place. It is only meaningful for threshold and distribution SLIs;
// the schema refuses the field on the other types.
func (s SLI) EffectiveGoodWhen() string {
	if s.GoodWhen == "" {
		return GoodWhenBelow
	}
	return s.GoodWhen
}

type SLO struct {
	ID                string            `json:"id"`
	SLI               string            `json:"sli"`
	Objective         float64           `json:"objective"`
	Window            string            `json:"window"`
	ErrorBudgetPolicy string            `json:"error_budget_policy"`
	Labels            map[string]string `json:"labels,omitempty"`
}

type Pipelines struct {
	Receivers  []map[string]any `json:"receivers"`
	Processors []map[string]any `json:"processors"`
	Exporters  Exporters        `json:"exporters"`
}

type Exporters struct {
	Metrics map[string]any `json:"metrics"`
	Logs    map[string]any `json:"logs"`
	Traces  map[string]any `json:"traces"`
}

type Queries struct {
	RecordingRules []RecordingRule `json:"recording_rules,omitempty"`
	DerivedViews   []DerivedView   `json:"derived_views,omitempty"`
}

type RecordingRule struct {
	Name     string            `json:"name"`
	Expr     string            `json:"expr"`
	Interval string            `json:"interval,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type DerivedView struct {
	ID     string         `json:"id"`
	Bind   string         `json:"bind,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

type Dashboard struct {
	ID            string         `json:"id"`
	Provider      map[string]any `json:"provider"`
	Folder        string         `json:"folder,omitempty"`
	Source        string         `json:"source,omitempty"`
	Template      string         `json:"template,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	Datasources   map[string]any `json:"datasources,omitempty"`
	PanelBindings []PanelBinding `json:"panel_bindings,omitempty"`
}

type PanelBinding struct {
	Panel   string `json:"panel"`
	BindsTo string `json:"binds_to"`
}

type Policy struct {
	BurnRateAlerts []BurnRateAlert `json:"burn_rate_alerts"`
	Forecasts      []Forecast      `json:"forecasts,omitempty"`
}

type BurnRateAlert struct {
	SLO     string       `json:"slo"`
	Windows []BurnWindow `json:"windows"`
}

type BurnWindow struct {
	Short    string  `json:"short"`
	Long     string  `json:"long"`
	Factor   float64 `json:"factor"`
	Severity string  `json:"severity"`
}

type Forecast struct {
	SLO               string `json:"slo"`
	Method            string `json:"method"`
	Horizon           string `json:"horizon"`
	OnProjectedBreach string `json:"on_projected_breach,omitempty"`
}

type Alerting struct {
	Routes   []Route  `json:"routes"`
	Dedup    string   `json:"dedup,omitempty"`
	Suppress []string `json:"suppress,omitempty"`
}

type Route struct {
	Severity string            `json:"severity"`
	Channels []map[string]any  `json:"channels"`
	Match    map[string]string `json:"match,omitempty"`
}

type Remediation struct {
	Trigger    string     `json:"trigger"`
	Runbook    string     `json:"runbook"`
	Automation string     `json:"automation"`
	Guardrails Guardrails `json:"guardrails"`
}

type Guardrails struct {
	MaxInvocationsPerHour int            `json:"max_invocations_per_hour"`
	RequiresHumanAbove    string         `json:"requires_human_above,omitempty"`
	RollbackOnFailure     bool           `json:"rollback_on_failure,omitempty"`
	CooldownAfterSuccess  string         `json:"cooldown_after_success,omitempty"`
	CircuitBreaker        map[string]any `json:"circuit_breaker,omitempty"`
}

type Baselines struct {
	MTTDTargetP50     string `json:"mttd_target_p50"`
	MTTRTargetP50     string `json:"mttr_target_p50"`
	MTTDTargetP95     string `json:"mttd_target_p95,omitempty"`
	MTTRTargetP95     string `json:"mttr_target_p95,omitempty"`
	MeasurementSource string `json:"measurement_source,omitempty"`
	ReviewCadence     string `json:"review_cadence,omitempty"`
	RegressionGate    string `json:"regression_gate,omitempty"`
}

type Validation struct {
	ChaosExperiments  []map[string]any `json:"chaos_experiments,omitempty"`
	SyntheticChecks   []SyntheticCheck `json:"synthetic_checks,omitempty"`
	ConformanceChecks []map[string]any `json:"conformance_checks,omitempty"`
}

type SyntheticCheck struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind,omitempty"`
	Target              string `json:"target,omitempty"`
	Interval            string `json:"interval,omitempty"`
	OTelInstrumentation bool   `json:"otel_instrumentation,omitempty"`
}

// Load reads a pack manifest from disk. Accepts .yaml/.yml or .json.
// It both populates the typed Pack and retains the raw document for
// JSON-Schema validation.
func Load(path string) (*Pack, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// sigs.k8s.io/yaml accepts YAML and JSON transparently and converts to JSON
	// before unmarshalling, which is what jsonschema needs.
	asJSON, err := yaml.YAMLToJSON(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(asJSON, &raw); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	var p Pack
	if err := json.Unmarshal(asJSON, &p); err != nil {
		return nil, fmt.Errorf("structure %s: %w", path, err)
	}
	p.Raw = raw
	return &p, nil
}

// RawJSON returns the canonical JSON bytes for schema validation.
func (p *Pack) RawJSON() ([]byte, error) {
	return json.Marshal(p.Raw)
}

// String makes a Pack identifiable in error messages.
func (p *Pack) String() string {
	if p == nil {
		return "<nil pack>"
	}
	parts := []string{p.Metadata.Name}
	if p.Metadata.Version != "" {
		parts = append(parts, "@"+p.Metadata.Version)
	}
	return strings.Join(parts, "")
}
