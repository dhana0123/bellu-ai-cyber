package steampipe

import (
	"testing"
)

// TestCapabilityEnabled is a table-driven unit test of the gate predicate. It
// covers the no-gating fallback, the always-on core/ungated cases, and the
// enabled/not-enabled branches.
func TestCapabilityEnabled(t *testing.T) {
	cases := []struct {
		name       string
		enabled    []string // passed through capsToSet
		capability string
		want       bool
	}{
		{"no gating configured, gated table -> collected", nil, "eks", true},
		{"no gating configured, core table -> collected", nil, "core", true},
		{"no gating configured, ungated table -> collected", nil, "", true},
		{"gating on, ungated table always collected", []string{"core"}, "", true},
		{"gating on, core always collected", []string{"eks"}, "core", true},
		{"gating on, enabled capability collected", []string{"core", "eks"}, "eks", true},
		{"gating on, not-enabled capability skipped", []string{"core"}, "eks", false},
		{"gating on, another not-enabled capability skipped", []string{"core", "eks"}, "ai", false},
		{"gating on, enabled among several", []string{"core", "eks", "workload", "ai"}, "ai", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Source{enabledCaps: capsToSet(tc.enabled)}
			if got := s.capabilityEnabled(tc.capability); got != tc.want {
				t.Errorf("capabilityEnabled(%q) with enabled %v = %v, want %v", tc.capability, tc.enabled, got, tc.want)
			}
		})
	}
}

// TestCapsToSet verifies the enabled-set builder: nil/empty in -> nil out (the
// no-gating sentinel), and a non-empty input always includes core.
func TestCapsToSet(t *testing.T) {
	if got := capsToSet(nil); got != nil {
		t.Errorf("capsToSet(nil) = %v, want nil", got)
	}
	if got := capsToSet([]string{}); got != nil {
		t.Errorf("capsToSet(empty) = %v, want nil", got)
	}
	if got := capsToSet([]string{"", ""}); got != nil {
		t.Errorf("capsToSet(all-empty-strings) = %v, want nil (no real caps)", got)
	}
	set := capsToSet([]string{"eks"})
	if !set["eks"] {
		t.Errorf("capsToSet([eks]) missing eks")
	}
	if !set["core"] {
		t.Errorf("capsToSet([eks]) must always include core")
	}
}

// TestCapsFromConfig verifies the config-value parser handles the typed
// []string path (AWS adapter), the []any path (JSON/YAML round-trip), and any
// other/missing type (nil = no gating).
func TestCapsFromConfig(t *testing.T) {
	if got := capsFromConfig([]string{"core", "eks"}); len(got) != 2 || got[0] != "core" || got[1] != "eks" {
		t.Errorf("capsFromConfig([]string) = %v, want [core eks]", got)
	}
	got := capsFromConfig([]any{"core", "eks", 42, ""})
	if len(got) != 2 || got[0] != "core" || got[1] != "eks" {
		t.Errorf("capsFromConfig([]any) = %v, want [core eks] (non-strings/empties dropped)", got)
	}
	if got := capsFromConfig(nil); got != nil {
		t.Errorf("capsFromConfig(nil) = %v, want nil", got)
	}
	if got := capsFromConfig("core"); got != nil {
		t.Errorf("capsFromConfig(string) = %v, want nil (unsupported type)", got)
	}
}

// TestCollectGateSkipsUngrantedTables exercises the gate through Collect: with
// only eks enabled, the eks table is queried and the ai/workload tables are
// skipped, while core and ungated tables are always queried.
func TestCollectGateSkipsUngrantedTables(t *testing.T) {
	s, rec := newRecordingSource(gatingCatalog(), []string{"eks"})
	_, _ = drain(t, s)

	mustQuery := []string{"aws_account" /*core*/, "aws_eks_cluster" /*enabled*/, "aws_ungated_table" /*ungated*/}
	for _, tbl := range mustQuery {
		if !rec.queried(tbl) {
			t.Errorf("table %q should have been queried with enabled [eks]", tbl)
		}
	}
	mustSkip := []string{"aws_lambda_function" /*workload*/, "aws_bedrock_custom_model" /*ai*/, "aws_rds_db_instance" /*dspm*/, "aws_made_up_table" /*unused*/}
	for _, tbl := range mustSkip {
		if rec.queried(tbl) {
			t.Errorf("table %q should have been SKIPPED with enabled [eks]", tbl)
		}
	}
}

// TestFactoryWiresCapabilities confirms Factory reads the "capabilities" config
// key into the Source's enabled set for both the []string and []any encodings,
// and that a missing key leaves gating off.
func TestFactoryWiresCapabilities(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"host":       "localhost",
			"port":       9193,
			"user":       "steampipe",
			"database":   "steampipe",
			"connection": "aws", // resolves to embedded aws.yaml
		}
	}

	t.Run("[]string", func(t *testing.T) {
		cfg := base()
		cfg["capabilities"] = []string{"core", "eks"}
		src, err := Factory(cfg)
		if err != nil {
			t.Fatalf("Factory: %v", err)
		}
		s := src.(*Source)
		if !s.enabledCaps["eks"] || !s.enabledCaps["core"] {
			t.Errorf("expected enabledCaps to contain core+eks, got %v", s.enabledCaps)
		}
		if s.enabledCaps["ai"] {
			t.Errorf("ai should not be enabled, got %v", s.enabledCaps)
		}
	})

	t.Run("[]any", func(t *testing.T) {
		cfg := base()
		cfg["capabilities"] = []any{"core", "workload"}
		src, err := Factory(cfg)
		if err != nil {
			t.Fatalf("Factory: %v", err)
		}
		s := src.(*Source)
		if !s.enabledCaps["workload"] || !s.enabledCaps["core"] {
			t.Errorf("expected enabledCaps to contain core+workload, got %v", s.enabledCaps)
		}
	})

	t.Run("missing key -> no gating", func(t *testing.T) {
		cfg := base()
		src, err := Factory(cfg)
		if err != nil {
			t.Fatalf("Factory: %v", err)
		}
		s := src.(*Source)
		if s.enabledCaps != nil {
			t.Errorf("missing capabilities key must leave gating off (nil set), got %v", s.enabledCaps)
		}
	})
}

// knownCapabilities mirrors internal/connector/capability.Catalog. It is
// duplicated here (rather than imported) because the capability package imports
// this steampipe package, so importing it back would create a cycle. The
// capability package's own steampipe_coverage_test.go cross-checks the two.
var knownCapabilities = map[string]bool{
	"core":     true,
	"eks":      true,
	"workload": true,
	"dspm":     true,
	"ai":       true,
}

// TestAWSCatalogCapabilitiesAreKnown asserts every capability annotation in the
// embedded aws.yaml names a real capability. A typo (e.g. "esk") would silently
// gate a table out forever; this fails the build instead.
func TestAWSCatalogCapabilitiesAreKnown(t *testing.T) {
	cat, err := LoadCatalog("catalog/aws.yaml")
	if err != nil {
		t.Fatalf("load aws.yaml: %v", err)
	}
	for _, tbl := range cat.Tables {
		if tbl.Capability == "" {
			continue // ungated is valid
		}
		if !knownCapabilities[tbl.Capability] {
			t.Errorf("table %q declares unknown capability %q (not in capability catalog)", tbl.Table, tbl.Capability)
		}
	}
}
