package cloudquery

import (
	"sort"
	"testing"
)

// TestNewProviderMappingsParse asserts every provider mapping shipped for the
// additional-collection-providers work (task 19) loads and validates: each file
// resolves through LoadCatalog (the embedded fallback matches by base name),
// parses, Validate() passes, and the parsed Provider is the one the mapping
// declares. Keeping the parse assertion data-driven over the real mapping files
// means a malformed or renamed mapping fails this test rather than silently
// shipping (Requirements 14.1, 14.2, 14.3).
func TestNewProviderMappingsParse(t *testing.T) {
	cases := []struct {
		file         string
		wantProvider string
	}{
		{"azure.yaml", "azure"},
		{"gcp.yaml", "gcp"},
		{"k8s.yaml", "k8s"},
		{"identity.yaml", "identity"},
		{"datadog.yaml", "datadog"},
		{"sentry.yaml", "sentry"},
		// aws.yaml is the pre-existing mapping; include it so the parse
		// assertion covers the full shipped set.
		{"aws.yaml", "aws"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			cat, err := LoadCatalog(tc.file)
			if err != nil {
				t.Fatalf("LoadCatalog(%q) = %v, want nil (must parse and Validate)", tc.file, err)
			}
			if cat.Provider != tc.wantProvider {
				t.Fatalf("LoadCatalog(%q) provider = %q, want %q", tc.file, cat.Provider, tc.wantProvider)
			}
			if len(cat.Tables) == 0 {
				t.Fatalf("LoadCatalog(%q) produced no tables", tc.file)
			}
		})
	}
}

// TestNewProviderMappingTableSets asserts each new mapping declares exactly the
// expected Bellu `table` set — the table names every downstream normalizer
// consumes. A drift in the mapping's table set (a renamed or dropped table)
// fails here, catching it before it reaches the collector (Requirements 14.1,
// 14.2, 14.3).
func TestNewProviderMappingTableSets(t *testing.T) {
	cases := []struct {
		file  string
		table []string
	}{
		{
			file: "azure.yaml",
			table: []string{
				"azure_subscription",
				"azure_managed_identity",
				"azure_storage_account",
				"azure_compute_virtual_machine",
				"azure_kubernetes_cluster",
				"azure_container_app",
				"azure_openai",
				"azure_role_definition",
				"azure_role_assignment",
			},
		},
		{
			file: "gcp.yaml",
			table: []string{
				"gcp_project",
				"gcp_service_account",
				"gcp_storage_bucket",
				"gcp_compute_instance",
				"gcp_kubernetes_cluster",
				"gcp_cloud_run_service",
				"gcp_vertex_ai_model",
				"gcp_iam_binding",
			},
		},
		{
			file: "k8s.yaml",
			table: []string{
				"kubernetes_namespace",
				"kubernetes_service_account",
				"kubernetes_pod",
				"kubernetes_deployment",
			},
		},
		{
			file: "identity.yaml",
			table: []string{
				"identity_provider_user",
				"identity_provider_group",
				"identity_provider_app",
			},
		},
		{
			file:  "datadog.yaml",
			table: []string{"datadog_monitor", "datadog_dashboard"},
		},
		{
			file:  "sentry.yaml",
			table: []string{"sentry_monitor", "sentry_project"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			cat, err := LoadCatalog(tc.file)
			if err != nil {
				t.Fatalf("LoadCatalog(%q) = %v", tc.file, err)
			}
			got := make([]string, 0, len(cat.Tables))
			for _, spec := range cat.Tables {
				got = append(got, spec.Table)
			}
			assertStringSetEqual(t, got, tc.table, "table set")
		})
	}
}

// TestNewProviderMappingCapabilityGating asserts capability gating produces the
// expected read set for each new mapping: a table whose capability is not
// enabled is excluded, and is included once that capability is granted. The
// read set is observed through SpecFromCatalog (the same gate the Source read
// loop and the generated sync Spec use), with the enabled set driving which
// cq_table selectors survive.
//
// Each case lists the capabilities to enable and the staging (cq_table)
// selectors that must appear in the resulting Spec. A nil enabled set means
// "no gating configured" and collects every table (the backward-compatible
// default). This proves monitoring/identity/ai/workload/eks/dspm tables are
// gated off when their capability is absent and on when present
// (Requirements 14.1, 14.2, 14.3).
func TestNewProviderMappingCapabilityGating(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		enabled map[string]bool
		want    []string // expected cq_table selectors in the gated Spec
	}{
		// Azure: core-only enabled excludes the eks/workload/ai tables.
		{
			name:    "azure core only excludes eks/workload/ai",
			file:    "azure.yaml",
			enabled: map[string]bool{"core": true},
			want: []string{
				"azure_subscriptions",
				"azure_resources_user_assigned_identities",
				"azure_storage_accounts",
				"azure_compute_virtual_machines",
				"azure_authorization_role_definitions",
				"azure_authorization_role_assignments",
			},
		},
		// Azure: granting eks+workload+ai adds exactly those gated tables.
		{
			name:    "azure with eks+workload+ai includes gated tables",
			file:    "azure.yaml",
			enabled: map[string]bool{"core": true, "eks": true, "workload": true, "ai": true},
			want: []string{
				"azure_subscriptions",
				"azure_resources_user_assigned_identities",
				"azure_storage_accounts",
				"azure_compute_virtual_machines",
				"azure_containerservice_managed_clusters",
				"azure_appcontainers_container_apps",
				"azure_cognitiveservices_accounts",
				"azure_authorization_role_definitions",
				"azure_authorization_role_assignments",
			},
		},
		// GCP: core-only enabled excludes the eks/workload/ai tables.
		{
			name:    "gcp core only excludes eks/workload/ai",
			file:    "gcp.yaml",
			enabled: map[string]bool{"core": true},
			want: []string{
				"gcp_projects",
				"gcp_iam_service_accounts",
				"gcp_storage_buckets",
				"gcp_compute_instances",
				"gcp_iam_policies",
			},
		},
		// GCP: granting eks+workload+ai adds exactly those gated tables.
		{
			name:    "gcp with eks+workload+ai includes gated tables",
			file:    "gcp.yaml",
			enabled: map[string]bool{"core": true, "eks": true, "workload": true, "ai": true},
			want: []string{
				"gcp_projects",
				"gcp_iam_service_accounts",
				"gcp_storage_buckets",
				"gcp_compute_instances",
				"gcp_container_clusters",
				"gcp_run_services",
				"gcp_aiplatform_models",
				"gcp_iam_policies",
			},
		},
		// Kubernetes: all core, so core-only collects every table.
		{
			name:    "k8s core only collects all",
			file:    "k8s.yaml",
			enabled: map[string]bool{"core": true},
			want: []string{
				"k8s_core_namespaces",
				"k8s_core_service_accounts",
				"k8s_core_pods",
				"k8s_apps_deployments",
			},
		},
		// Identity: gated entirely on the `identity` capability.
		{
			name:    "identity without capability excludes every table",
			file:    "identity.yaml",
			enabled: map[string]bool{"core": true},
			want:    nil,
		},
		{
			name:    "identity with capability includes every table",
			file:    "identity.yaml",
			enabled: map[string]bool{"core": true, "identity": true},
			want: []string{
				"identity_provider_users",
				"identity_provider_groups",
				"identity_provider_apps",
			},
		},
		// Datadog: gated entirely on `monitoring`.
		{
			name:    "datadog without monitoring excludes every table",
			file:    "datadog.yaml",
			enabled: map[string]bool{"core": true},
			want:    nil,
		},
		{
			name:    "datadog with monitoring includes every table",
			file:    "datadog.yaml",
			enabled: map[string]bool{"monitoring": true},
			want:    []string{"datadog_monitors", "datadog_dashboards"},
		},
		// Sentry: gated entirely on `monitoring`.
		{
			name:    "sentry without monitoring excludes every table",
			file:    "sentry.yaml",
			enabled: map[string]bool{"core": true},
			want:    nil,
		},
		{
			name:    "sentry with monitoring includes every table",
			file:    "sentry.yaml",
			enabled: map[string]bool{"monitoring": true},
			want:    []string{"sentry_monitors", "sentry_projects"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, err := LoadCatalog(tc.file)
			if err != nil {
				t.Fatalf("LoadCatalog(%q) = %v", tc.file, err)
			}

			spec, err := SpecFromCatalog(cat, "postgres://unused", tc.enabled)
			if len(tc.want) == 0 {
				// Every table gated off: SpecFromCatalog reports "no enabled
				// tables" rather than returning an empty Spec, so an ungranted
				// source never schedules a sync for zero tables.
				if err == nil {
					t.Fatalf("SpecFromCatalog(%q, enabled=%v) = nil error, want 'no enabled tables' (all tables gated off)", tc.file, tc.enabled)
				}
				return
			}
			if err != nil {
				t.Fatalf("SpecFromCatalog(%q, enabled=%v) = %v, want nil", tc.file, tc.enabled, err)
			}
			assertStringSetEqual(t, spec.Tables, tc.want, "gated cq_table set")
		})
	}
}

// TestNewProviderMappingNilEnabledCollectsEverything asserts a nil enabled set
// (no gating configured) selects every table in a gated mapping, preserving the
// collect-everything default even for mappings whose tables are all behind a
// non-core capability (identity, datadog, sentry).
func TestNewProviderMappingNilEnabledCollectsEverything(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"identity.yaml", []string{"identity_provider_users", "identity_provider_groups", "identity_provider_apps"}},
		{"datadog.yaml", []string{"datadog_monitors", "datadog_dashboards"}},
		{"sentry.yaml", []string{"sentry_monitors", "sentry_projects"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			cat, err := LoadCatalog(tc.file)
			if err != nil {
				t.Fatalf("LoadCatalog(%q) = %v", tc.file, err)
			}
			spec, err := SpecFromCatalog(cat, "postgres://unused", nil)
			if err != nil {
				t.Fatalf("SpecFromCatalog(%q, nil) = %v, want nil (collect everything)", tc.file, err)
			}
			assertStringSetEqual(t, spec.Tables, tc.want, "nil-gating cq_table set")
		})
	}
}

// assertStringSetEqual compares two string slices as sets (order-independent),
// failing with a readable diff when they differ.
func assertStringSetEqual(t *testing.T, got, want []string, label string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) != len(w) {
		t.Fatalf("%s: got %d items %v, want %d items %v", label, len(g), g, len(w), w)
	}
	for i := range g {
		if g[i] != w[i] {
			t.Fatalf("%s: got %v, want %v", label, g, w)
		}
	}
}
