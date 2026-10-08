// This file adds the read-only side of IntelStore: the two methods the
// Intelligence Matcher's intelcorrelate.IntelSource interface requires
// (VulnsByPurl and AllVulns). The Matcher reads the canonical corpus through
// that small interface, so IntelStore satisfying it lets `global-intel
// correlate`/`score` drive the real intel_* tables (task 16.1) without the
// matcher depending on this package directly.
//
// These methods are strictly read-only: they issue parameterized SELECTs
// against the intel_* tables and never write. A canonical vuln is hydrated by
// joining intel_vulnerabilities with intel_aliases and intel_affected_packages
// so the returned canonical.CanonicalVuln carries the same shape the
// canonicalizers produced on ingest (id, summary, cvss, severity, kev, epss,
// aliases, and affected purl ranges).
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
)

// VulnsByPurl returns every canonical vuln whose affected set names the given
// purl (the purl name, without a version qualifier). It first resolves the
// distinct vuln ids affecting the purl via the intel_affected_packages.purl
// index, then hydrates each one. The purl is passed as a bound parameter, never
// interpolated.
//
// This is the purl-keyed fast path the matcher uses for Package nodes, so it
// fetches only the handful of vulns that mention a purl rather than scanning
// the whole corpus. It satisfies intelcorrelate.IntelSource.
func (s *IntelStore) VulnsByPurl(ctx context.Context, purlName string) ([]canonical.CanonicalVuln, error) {
	rows, err := s.store.Pool().Query(ctx,
		`SELECT DISTINCT vuln_id
		   FROM intel_affected_packages
		  WHERE purl = $1
		  ORDER BY vuln_id`,
		purlName)
	if err != nil {
		return nil, fmt.Errorf("database: vulns by purl %q: %w", purlName, err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("database: vulns by purl %q: %w", purlName, err)
	}
	return s.hydrateVulns(ctx, ids)
}

// AllVulns returns the full canonical corpus for non-purl-keyed matching
// (container-image digests and AI-asset identifiers). It satisfies
// intelcorrelate.IntelSource.
func (s *IntelStore) AllVulns(ctx context.Context) ([]canonical.CanonicalVuln, error) {
	rows, err := s.store.Pool().Query(ctx,
		`SELECT id FROM intel_vulnerabilities ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("database: all vulns: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("database: all vulns: %w", err)
	}
	return s.hydrateVulns(ctx, ids)
}

// VulnFilter bounds a ListVulns read. Zero-value fields are unset: an empty
// Severity or Purl is not filtered on, KEV filters only when KEVOnly is set,
// and a non-positive Limit falls back to defaultListLimit. The filter is
// pushed into SQL so the list endpoint hydrates only the bounded page rather
// than the whole corpus.
type VulnFilter struct {
	Severity canonical.Severity
	Purl     string
	KEVOnly  bool
	HasKEV   bool
	Limit    int
}

// defaultListLimit caps an unbounded ListVulns request so the list endpoint
// never hydrates the entire corpus by accident.
const defaultListLimit = 200

// ListVulns returns a bounded, filtered page of canonical vulns for the read
// API. Severity, KEV, and purl are applied as a SQL WHERE so the id set is
// narrowed and LIMIT-ed before hydration — only the resulting page is hydrated
// (base row + aliases + affected). This replaces the previous "AllVulns then
// filter in Go" path that hydrated all rows and timed out on large corpora.
//
// AllVulns is retained for Rebuild/correlation, which legitimately need the
// whole corpus; ListVulns is the API read path.
func (s *IntelStore) ListVulns(ctx context.Context, f VulnFilter) ([]canonical.CanonicalVuln, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}

	// Build a parameterized WHERE from the set filters. Args are bound, never
	// interpolated. When a purl is given, the id set is constrained to vulns
	// that name the purl via a subquery against intel_affected_packages.
	var (
		where []string
		args  []any
	)
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Severity != "" {
		add("v.severity = $%d", string(f.Severity))
	}
	if f.HasKEV {
		add("v.known_exploited = $%d", f.KEVOnly)
	}
	if f.Purl != "" {
		add("EXISTS (SELECT 1 FROM intel_affected_packages ap WHERE ap.vuln_id = v.id AND ap.purl = $%d)", f.Purl)
	}

	query := "SELECT v.id FROM intel_vulnerabilities v"
	if len(where) > 0 {
		query += " WHERE " + where[0]
		for _, w := range where[1:] {
			query += " AND " + w
		}
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY v.id LIMIT $%d", len(args))

	rows, err := s.store.Pool().Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("database: list vulns: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("database: list vulns: %w", err)
	}
	return s.hydrateVulns(ctx, ids)
}

// VulnByID loads a single canonical vulnerability by its canonical id,
// hydrated from the intel_* tables (base row + aliases + affected ranges). It
// is the single-vuln read the correlate/score stages use to resolve the
// canonical record a Candidate cites.
func (s *IntelStore) VulnByID(ctx context.Context, id string) (canonical.CanonicalVuln, error) {
	return s.hydrateVuln(ctx, id)
}

// scanIDs collects a single text column from a result set into a slice. It
// closes the rows before returning.
func scanIDs(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// hydrateVulns loads a full canonical.CanonicalVuln for each id, in id order.
func (s *IntelStore) hydrateVulns(ctx context.Context, ids []string) ([]canonical.CanonicalVuln, error) {
	out := make([]canonical.CanonicalVuln, 0, len(ids))
	for _, id := range ids {
		v, err := s.hydrateVuln(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// hydrateVuln loads one canonical vulnerability from the intel_* tables: the
// base row (summary/cvss/severity/kev/epss) plus its aliases and affected
// ranges. All three reads are parameterized on the vuln id.
func (s *IntelStore) hydrateVuln(ctx context.Context, id string) (canonical.CanonicalVuln, error) {
	v := canonical.CanonicalVuln{ID: id}

	// Base row. nullable cvss/epss columns are read through pointers so a NULL
	// stays a zero score rather than erroring.
	var (
		cvssScore *float64
		epss      *float64
		severity  string
		published *time.Time
		modified  *time.Time
	)
	err := s.store.Pool().QueryRow(ctx,
		`SELECT summary, cvss_score, cvss_vector, cvss_version, severity,
		        known_exploited, epss, published, modified, sources
		   FROM intel_vulnerabilities
		  WHERE id = $1`,
		id,
	).Scan(
		&v.Summary, &cvssScore, &v.CVSS.Vector, &v.CVSS.Version, &severity,
		&v.KnownExploited, &epss, &published, &modified, &v.Sources,
	)
	if err != nil {
		return canonical.CanonicalVuln{}, fmt.Errorf("database: hydrate vuln %q: %w", id, err)
	}
	v.Severity = canonical.Severity(severity)
	if cvssScore != nil {
		v.CVSS.Score = *cvssScore
	}
	if epss != nil {
		v.EPSS = *epss
	}
	// published/modified are nullable TIMESTAMPTZ columns (nullableTime stores a
	// zero time as NULL on write), so they are scanned through *time.Time and
	// left as the zero value when NULL — scanning NULL straight into time.Time
	// would error and 500 the intel API for KEV/EPSS-only records.
	if published != nil {
		v.Published = *published
	}
	if modified != nil {
		v.Modified = *modified
	}

	aliases, err := s.loadAliases(ctx, id)
	if err != nil {
		return canonical.CanonicalVuln{}, err
	}
	v.Aliases = aliases

	affected, err := s.loadAffected(ctx, id)
	if err != nil {
		return canonical.CanonicalVuln{}, err
	}
	v.Affected = affected

	return v, nil
}

// loadAliases reads every alias pointing at the vuln id. The canonical id is
// guaranteed to be among them (UpsertVuln writes it as an alias), so the
// hydrated vuln satisfies canonical.Validate's "ID present in Aliases" rule.
func (s *IntelStore) loadAliases(ctx context.Context, id string) ([]string, error) {
	rows, err := s.store.Pool().Query(ctx,
		`SELECT alias FROM intel_aliases WHERE vuln_id = $1 ORDER BY alias`, id)
	if err != nil {
		return nil, fmt.Errorf("database: load aliases %q: %w", id, err)
	}
	aliases, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("database: load aliases %q: %w", id, err)
	}
	return aliases, nil
}

// loadAffected reads the affected package ranges for the vuln id and groups
// them by purl so each AffectedPackage carries all of its ranges, matching the
// shape the canonicalizers produced. An all-empty (introduced, fixed) row
// becomes an empty-but-present range list for that purl.
func (s *IntelStore) loadAffected(ctx context.Context, id string) ([]canonical.AffectedPackage, error) {
	rows, err := s.store.Pool().Query(ctx,
		`SELECT purl, ecosystem, introduced, fixed
		   FROM intel_affected_packages
		  WHERE vuln_id = $1
		  ORDER BY purl, introduced, fixed`,
		id)
	if err != nil {
		return nil, fmt.Errorf("database: load affected %q: %w", id, err)
	}
	defer rows.Close()

	// Group ranges by purl, preserving first-seen purl order for determinism.
	byPurl := map[string]*canonical.AffectedPackage{}
	var order []string
	for rows.Next() {
		var purl, ecosystem, introduced, fixed string
		if err := rows.Scan(&purl, &ecosystem, &introduced, &fixed); err != nil {
			return nil, fmt.Errorf("database: scan affected %q: %w", id, err)
		}
		ap, ok := byPurl[purl]
		if !ok {
			ap = &canonical.AffectedPackage{Purl: purl, Ecosystem: ecosystem}
			byPurl[purl] = ap
			order = append(order, purl)
		}
		// A row with no bounds was written only to record the affected purl
		// (see upsertAffected); it carries no usable range, so skip adding an
		// empty SemverRange that would match every version.
		if introduced != "" || fixed != "" {
			ap.Ranges = append(ap.Ranges, canonical.SemverRange{
				Introduced: introduced,
				Fixed:      fixed,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: load affected %q: %w", id, err)
	}

	out := make([]canonical.AffectedPackage, 0, len(order))
	for _, purl := range order {
		out = append(out, *byPurl[purl])
	}
	return out, nil
}
