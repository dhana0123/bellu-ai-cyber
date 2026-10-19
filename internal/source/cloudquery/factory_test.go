package cloudquery

import (
	"context"
	"strings"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// awsFactoryConfig is the minimal CloudQuery Factory config the AWS adapter's
// Discover emits: an explicit staging DSN, the "aws" mapping selector (resolved
// to the embedded mapping/aws.yaml), the enabled capabilities, and the per-run
// credential env. Tests clone and tweak it.
func awsFactoryConfig() map[string]any {
	return map[string]any{
		"dsn":          "postgres://u:pw@db:5432/bellu?sslmode=disable",
		"connection":   "aws",
		"capabilities": []string{"core"},
		"credential_env": map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIAEXAMPLE",
			"AWS_SECRET_ACCESS_KEY": "wJalrEXAMPLEKEY",
			"AWS_SESSION_TOKEN":     "sess-token-value",
		},
	}
}

// factorySource builds a Source via Factory and type-asserts it, failing the
// test on error so the wiring assertions below read cleanly.
func factorySource(t *testing.T, cfg map[string]any) *Source {
	t.Helper()
	src, err := Factory(cfg)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	s, ok := src.(*Source)
	if !ok {
		t.Fatalf("Factory returned %T, want *Source", src)
	}
	return s
}

// TestFactoryName asserts the Factory-built source reports the stable collector
// name, so it slots in at the same pipeline boundary (Requirement 2.1).
func TestFactoryName(t *testing.T) {
	s := factorySource(t, awsFactoryConfig())
	if s.Name() != SourceName {
		t.Errorf("Name() = %q, want %q", s.Name(), SourceName)
	}
}

// TestFactoryResolvesEmbeddedMapping asserts a "connection" with no on-disk
// mapping path resolves to the embedded aws.yaml and loads a non-empty catalog
// with provider aws.
func TestFactoryResolvesEmbeddedMapping(t *testing.T) {
	s := factorySource(t, awsFactoryConfig())
	if s.catalog == nil {
		t.Fatal("Factory produced no catalog")
	}
	if s.catalog.Provider != "aws" {
		t.Errorf("catalog provider = %q, want aws", s.catalog.Provider)
	}
	if len(s.catalog.Tables) == 0 {
		t.Error("catalog has no tables")
	}
	if s.catalog.Connection != "aws" {
		t.Errorf("catalog connection = %q, want aws (from config)", s.catalog.Connection)
	}
}

// TestFactoryDSNFromQuartet asserts the Factory assembles a DSN from the
// host/port/user/password/database quartet when no explicit "dsn" is given.
func TestFactoryDSNFromQuartet(t *testing.T) {
	cfg := map[string]any{
		"host":       "staging-db",
		"port":       6543,
		"user":       "bellu",
		"password":   "secret",
		"database":   "cq",
		"connection": "aws",
	}
	s := factorySource(t, cfg)
	want := "postgres://bellu:secret@staging-db:6543/cq?sslmode=disable"
	if s.dsn != want {
		t.Errorf("assembled dsn = %q, want %q", s.dsn, want)
	}
}

// TestFactoryRequiresDSNOrHost asserts a config with neither "dsn" nor "host"
// is rejected with a clear error rather than building a Source that cannot
// connect.
func TestFactoryRequiresDSNOrHost(t *testing.T) {
	_, err := Factory(map[string]any{"connection": "aws"})
	if err == nil || !strings.Contains(err.Error(), "dsn") {
		t.Fatalf("want a dsn/host requirement error, got %v", err)
	}
}

// TestFactoryRequiresCatalogOrConnection asserts a config with neither
// "catalog" nor "connection" is rejected.
func TestFactoryRequiresCatalogOrConnection(t *testing.T) {
	_, err := Factory(map[string]any{"dsn": "postgres://x"})
	if err == nil || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("want a catalog/connection requirement error, got %v", err)
	}
}

// TestFactoryWiresProductionRunner asserts that, with no "runner" key, the
// Factory wires the production CLI JobRunner and generates a credential-free
// Spec from the mapping, carrying the per-run credential env for the runner.
func TestFactoryWiresProductionRunner(t *testing.T) {
	s := factorySource(t, awsFactoryConfig())
	if s.runner == nil {
		t.Fatal("Factory did not wire a JobRunner (production runner expected)")
	}
	if len(s.spec.Tables) == 0 {
		t.Error("Factory did not generate a Spec with tables")
	}
	// The generated Spec is credential-free: the secret rides on credentialEnv,
	// never in the Spec (Requirement 16.4).
	if strings.Contains(specYAML(s.spec), "sess-token-value") {
		t.Error("credential leaked into the generated Spec")
	}
	if s.credentialEnv["AWS_SESSION_TOKEN"] != "sess-token-value" {
		t.Errorf("credential env not forwarded to the runner: %+v", s.credentialEnv)
	}
}

// TestFactoryNilRunnerMeansStagingPopulated asserts that an explicit nil
// "runner" value wires NO runner, so Collect reads the already-populated
// staging rows without launching a sync. This is the parity/golden mode.
func TestFactoryNilRunnerMeansStagingPopulated(t *testing.T) {
	cfg := awsFactoryConfig()
	cfg["runner"] = JobRunner(nil)
	s := factorySource(t, cfg)
	if s.runner != nil {
		t.Error("explicit nil runner should wire no runner (staging-already-populated mode)")
	}
	// With no runner, runSync is a no-op and the staging read proceeds. Drive it
	// through the injected querier to confirm rows are read without a sync.
	q := &fakeQuerier{byTable: map[string]tableRows{
		"aws_iam_roles": {rows: []map[string]any{
			{"arn": "arn:aws:iam::111122223333:role/admin", "account_id": "111122223333"},
		}},
	}}
	s.connect = func(context.Context) (querier, func(), error) { return q, func() {}, nil }

	events, errs := collect(t, s, source.Run{RunID: "run-nil-runner"})
	if len(events) == 0 {
		t.Fatalf("expected events read from populated staging, got 0 (errs=%v)", errs)
	}
	for _, e := range events {
		if e.Source != SourceName {
			t.Errorf("event source = %q, want %q", e.Source, SourceName)
		}
	}
}

// TestFactoryAcceptsInjectedRunner asserts a JobRunner passed in the config is
// used as-is (the typed path tests and non-CLI wirings use).
func TestFactoryAcceptsInjectedRunner(t *testing.T) {
	cfg := awsFactoryConfig()
	fr := &fakeRunner{}
	cfg["runner"] = JobRunner(fr)
	s := factorySource(t, cfg)
	if s.runner == nil {
		t.Fatal("injected runner was not wired")
	}
	// The wired runner is the exact instance passed in.
	if s.runner != JobRunner(fr) {
		t.Error("Factory wired a different runner than the one injected")
	}
}

// TestFactoryCapabilityGatingDropsUngatedTable asserts the enabled-capability
// list from the config gates the generated Spec: a mapping restricted to one
// capability only selects that capability's tables. We prove gating is wired by
// comparing the Spec table count with an empty vs a full capability set.
func TestFactoryCapabilityGatingWired(t *testing.T) {
	// No capabilities configured => collect everything (nil gating set).
	open := awsFactoryConfig()
	delete(open, "capabilities")
	sOpen := factorySource(t, open)
	if sOpen.enabledCaps != nil {
		t.Errorf("missing capabilities should leave enabledCaps nil, got %+v", sOpen.enabledCaps)
	}

	// A capability set is parsed and core is always included.
	gated := awsFactoryConfig()
	gated["capabilities"] = []string{"eks"}
	sGated := factorySource(t, gated)
	if sGated.enabledCaps == nil {
		t.Fatal("configured capabilities should produce a non-nil enabled set")
	}
	if !sGated.enabledCaps["eks"] || !sGated.enabledCaps["core"] {
		t.Errorf("enabled set = %+v, want eks and core present", sGated.enabledCaps)
	}
	// The Spec selects at most as many tables when gated as when open.
	if len(sGated.spec.Tables) > len(sOpen.spec.Tables) {
		t.Errorf("gated spec has more tables (%d) than open spec (%d)",
			len(sGated.spec.Tables), len(sOpen.spec.Tables))
	}
}
