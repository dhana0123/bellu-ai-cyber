package cloudquery

import (
	"fmt"
	"sort"
	"strings"
)

// Spec is the generated CloudQuery configuration for one discovery run. It is
// derived from a connection's table-mapping Catalog and the destination DSN;
// a JobRunner turns it into an on-disk `cloudquery sync` spec file and the
// `cloudquery sync` invocation.
//
// A Spec carries NO credentials. STS session credentials for the run travel
// separately, through the per-run env map handed to JobRunner.Run, and are
// never serialized into the Spec, the on-disk spec file, logs, or errors
// (Requirement 16.4; mirrors the DiscoveryPlan.CredentialEnv contract).
type Spec struct {
	// Provider selects the CloudQuery source plugin: aws | azure | gcp | k8s.
	Provider string
	// Tables are the CloudQuery staging table selectors to sync, taken from the
	// mapping's cq_table values (deduplicated, stable-sorted).
	Tables []string
	// Regions optionally restricts the sync to a fixed set of regions. Empty
	// means the plugin's default (all regions).
	Regions []string
	// DestSchema is the PostgreSQL schema the sync writes into. Always the
	// package default "cq_staging" for a Spec built by SpecFromCatalog.
	DestSchema string
	// DestDSN is the customer PostgreSQL DSN the postgresql destination writes
	// to. This is a connection string, not a credential for the source plugin;
	// it is intentionally part of the destination spec.
	DestDSN string
	// WriteMode is the CloudQuery destination write mode. The default
	// ("overwrite-delete-stale") was pinned by the destination-behavior spike
	// (task 1); see the Risks register in design.md.
	WriteMode string
}

// DefaultWriteMode is the CloudQuery postgresql destination write mode used for
// a per-run sync. It is isolated here, inside the package, with every other
// staging/destination assumption.
const DefaultWriteMode = "overwrite-delete-stale"

// SpecFromCatalog builds the per-connection Spec for a run from a table-mapping
// Catalog and the destination DSN. The table selectors are the mapping's
// cq_table values (deduplicated and stable-sorted so the generated spec is
// deterministic). enabled gates the table set by the connection's capabilities
// with the same semantics as Source.Collect: a table whose capability is not
// enabled is excluded from the sync, so an ungranted capability never asks the
// plugin for resources it cannot read.
//
// SpecFromCatalog never reads or references credentials: the returned Spec is
// safe to serialize to disk and to log.
func SpecFromCatalog(cat *Catalog, destDSN string, enabled map[string]bool) (Spec, error) {
	if cat == nil {
		return Spec{}, fmt.Errorf("cloudquery: nil catalog")
	}

	seen := map[string]bool{}
	var tables []string
	regionSet := map[string]bool{}
	for _, t := range cat.Tables {
		if !capabilityEnabled(t.Capability, enabled) {
			continue
		}
		cq := t.cqTable()
		if cq != "" && !seen[cq] {
			seen[cq] = true
			tables = append(tables, cq)
		}
		for _, r := range t.Regions {
			if r != "" {
				regionSet[r] = true
			}
		}
	}
	if len(tables) == 0 {
		return Spec{}, fmt.Errorf("cloudquery: mapping %s has no enabled tables", cat.Provider)
	}
	sort.Strings(tables)

	var regions []string
	for r := range regionSet {
		regions = append(regions, r)
	}
	sort.Strings(regions)

	return Spec{
		Provider:   cat.Provider,
		Tables:     tables,
		Regions:    regions,
		DestSchema: DefaultStagingSchema,
		DestDSN:    destDSN,
		WriteMode:  DefaultWriteMode,
	}, nil
}

// capabilityEnabled reports whether a table's capability gate is satisfied by
// the connection's enabled set, mirroring the Source read-path semantics: an
// empty capability is ungated (always on), "core" is always on, and a nil
// enabled map means "no gating" (every table is enabled). Otherwise the
// capability must be present in enabled.
func capabilityEnabled(capability string, enabled map[string]bool) bool {
	if capability == "" || capability == "core" {
		return true
	}
	if enabled == nil {
		return true
	}
	return enabled[capability]
}

// specYAML renders a Spec as a CloudQuery sync spec file (source plugin +
// postgresql destination into DestSchema). It contains NO credentials: the
// source plugin reads its credentials from the process environment the runner
// injects, and the destination connection string is the DestDSN. This is the
// single place a Spec becomes an on-disk document, so the credential-exclusion
// guarantee lives here.
func specYAML(spec Spec) string {
	var b strings.Builder

	// Source plugin block. Tables and regions are the only spec_ fields; the
	// plugin picks up AWS_*/AZURE_*/etc. credentials from the environment.
	b.WriteString("kind: source\n")
	b.WriteString("spec:\n")
	b.WriteString("  name: " + spec.Provider + "\n")
	b.WriteString("  path: cloudquery/" + spec.Provider + "\n")
	b.WriteString("  registry: cloudquery\n")
	b.WriteString("  tables:\n")
	for _, t := range spec.Tables {
		b.WriteString("    - " + t + "\n")
	}
	b.WriteString("  destinations:\n")
	b.WriteString("    - postgresql\n")
	if len(spec.Regions) > 0 {
		b.WriteString("  spec:\n")
		b.WriteString("    regions:\n")
		for _, r := range spec.Regions {
			b.WriteString("      - " + r + "\n")
		}
	}
	b.WriteString("---\n")

	// Destination plugin block. connection_string carries the DestDSN; the
	// schema the sync writes into is DestSchema.
	b.WriteString("kind: destination\n")
	b.WriteString("spec:\n")
	b.WriteString("  name: postgresql\n")
	b.WriteString("  path: cloudquery/postgresql\n")
	b.WriteString("  registry: cloudquery\n")
	b.WriteString("  write_mode: " + spec.WriteMode + "\n")
	b.WriteString("  spec:\n")
	b.WriteString("    connection_string: ${CQ_DEST_DSN}\n")
	b.WriteString("    pgx_log_level: error\n")
	b.WriteString("    schema: " + spec.DestSchema + "\n")

	return b.String()
}
