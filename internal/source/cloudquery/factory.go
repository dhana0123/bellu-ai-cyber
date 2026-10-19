package cloudquery

import (
	"fmt"
	"path/filepath"

	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// DefaultMappingDir is where LoadCatalog looks for a provider's table-mapping
// YAML when the config names a "connection" rather than an explicit "catalog"
// path. On a service image the file is not present on disk; LoadCatalog falls
// back to the mapping embedded in the binary (see catalog_embed.go), matched by
// base name, so this default works regardless of the process working directory.
const DefaultMappingDir = "internal/source/cloudquery/mapping"

// Factory builds a cloudquery.Source from the connector's config map. It is the
// production entry point the discovery orchestrator calls in place of the
// (now-removed) steampipe.Factory, so the Steampipe->CloudQuery cutover was a
// wiring swap rather than a pipeline change: the Source it returns sits at the
// same boundary the Steampipe source occupied and every downstream normalizer
// is unchanged.
//
// Required config keys:
//   - a destination/staging DSN: either an explicit "dsn", or the
//     host/port/user/password/database quartet the Factory assembles into one;
//   - the mapping: an explicit "catalog" path, or a "connection" that resolves
//     to <mapping_dir>/<connection>.yaml (mapping_dir defaults to
//     DefaultMappingDir, with the embedded fallback);
//   - the optional "capabilities" gating list (same semantics as the Steampipe
//     source: a table whose capability is not enabled is skipped).
//
// CloudQuery-specific keys drive the collection job:
//   - "runner" (JobRunner): the job runner that launches `cloudquery sync`
//     before any staging row is read. When absent, the Factory wires the
//     production CLI runner (NewCLIRunner) using the optional "cloudquery_bin".
//     An explicit nil runner value (key present, value nil) means "staging is
//     already populated" — the Source skips the sync and reads existing rows,
//     the mode parity/golden tests use.
//   - "credential_env" (map[string]string): the run's STS session credentials,
//     handed to the runner for this run only. It travels in memory from the
//     discovery plan to the runner's subprocess environment and is discarded
//     after the run on every path; it is never persisted to the staging DB, the
//     on-disk spec, logs, or errors (Requirement 16.4). It is accepted here,
//     rather than on a separate sink, because the CloudQuery JobRunner — not an
//     external process — is what needs the credentials.
//
// The returned Source is credential-safe to hold: once Collect runs the sync,
// the credential env is dropped from the Source on every path.
func Factory(cfg map[string]any) (source.Source, error) {
	dsn, err := dsnFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	cat, err := catalogFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	src := New(dsn, cat)

	// Capability gating: a missing/empty "capabilities" key leaves enabledCaps
	// nil, preserving the collect-everything behavior, exactly like the
	// Steampipe source.
	src.enabledCaps = capsToSet(capsFromConfig(cfg["capabilities"]))

	// Wire the collection job. The spec is generated from the (capability-gated)
	// mapping and the staging DSN; it is credential-free. The runner launches
	// the sync before any staging row is read, so a failed/cancelled sync emits
	// zero RawEvents (Requirements 1.6, 1.7).
	runner, wireRunner := runnerFromConfig(cfg)
	if wireRunner {
		spec, err := SpecFromCatalog(cat, dsn, src.enabledCaps)
		if err != nil {
			return nil, err
		}
		src.runner = runner
		src.spec = spec
		src.credentialEnv = credentialEnvFromConfig(cfg["credential_env"])
	}

	return src, nil
}

// dsnFromConfig resolves the customer PostgreSQL DSN the Source reads cq_staging
// from (and the sync writes its destination into). An explicit "dsn" wins;
// otherwise it is assembled from the host/port/user/password/database quartet,
// mirroring steampipe.Factory's connection shape.
func dsnFromConfig(cfg map[string]any) (string, error) {
	if dsn := strOr(cfg, "dsn", ""); dsn != "" {
		return dsn, nil
	}
	host := strOr(cfg, "host", "")
	if host == "" {
		return "", fmt.Errorf("cloudquery: config requires \"dsn\" or \"host\"")
	}
	port := intOr(cfg, "port", 5432)
	user := strOr(cfg, "user", "postgres")
	password := strOr(cfg, "password", "")
	database := strOr(cfg, "database", "postgres")
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		user, password, host, port, database), nil
}

// catalogFromConfig resolves the table-mapping Catalog: an explicit "catalog"
// path wins; otherwise a "connection" resolves to <mapping_dir>/<conn>.yaml
// (mapping_dir defaults to DefaultMappingDir, with the embedded fallback in
// LoadCatalog). The resolved connection name labels the collector.
func catalogFromConfig(cfg map[string]any) (*Catalog, error) {
	catalogPath := strOr(cfg, "catalog", "")
	conn := strOr(cfg, "connection", "")
	if catalogPath == "" {
		if conn == "" {
			return nil, fmt.Errorf("cloudquery: config requires \"catalog\" or \"connection\"")
		}
		dir := strOr(cfg, "mapping_dir", DefaultMappingDir)
		catalogPath = filepath.Join(dir, conn+".yaml")
	}
	cat, err := LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	if conn != "" && cat.Connection == "" {
		cat.Connection = conn
	}
	return cat, nil
}

// runnerFromConfig resolves the JobRunner to wire onto the Source. It returns
// the runner and whether a runner should be wired at all:
//   - key absent: wire the production CLI runner (NewCLIRunner), so a normal
//     discovery run launches `cloudquery sync` before reading staging. wire=true.
//   - key present with a JobRunner value: use it (tests inject a fake). wire=true.
//   - key present with a nil value: "staging already populated" — wire no runner
//     so Collect reads existing cq_staging rows without a sync. wire=false.
func runnerFromConfig(cfg map[string]any) (JobRunner, bool) {
	v, ok := cfg["runner"]
	if !ok {
		return NewCLIRunner(strOr(cfg, "cloudquery_bin", "")), true
	}
	if v == nil {
		return nil, false
	}
	if r, ok := v.(JobRunner); ok {
		return r, true
	}
	// An unexpected type for "runner" falls back to the production runner rather
	// than silently skipping the sync, so a miswired config fails closed (a sync
	// is attempted) rather than reading stale/empty staging.
	return NewCLIRunner(strOr(cfg, "cloudquery_bin", "")), true
}

// credentialEnvFromConfig extracts the per-run STS credential environment from
// a Factory config value. A caller may pass map[string]string (the typed path
// from the discovery plan) or map[string]any (after a round-trip). Any other
// type — including a missing key — yields nil, which means "no credentials to
// inject" (the runner relies on the ambient environment).
func credentialEnvFromConfig(v any) map[string]string {
	switch env := v.(type) {
	case map[string]string:
		return env
	case map[string]any:
		out := make(map[string]string, len(env))
		for k, val := range env {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	default:
		return nil
	}
}

// strOr reads a string config value with a default. It mirrors the helper the
// Steampipe factory uses so the two factories read config identically.
func strOr(cfg map[string]any, key, def string) string {
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

// intOr reads an int config value with a default, tolerating the int/float64
// ambiguity a JSON/YAML round-trip introduces.
func intOr(cfg map[string]any, key string, def int) int {
	switch v := cfg[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return def
	}
}
