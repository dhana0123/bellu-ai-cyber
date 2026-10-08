// Package search is the fast lookup/index layer over the canonical
// global-intelligence corpus. PostgreSQL remains the authoritative system of
// record; this index is a derived, disposable view that can be rebuilt in full
// from the intel_* tables (Rebuild). The default implementation is
// OpenSearch-backed (see opensearch.go) and connects to BELLU_SEARCH_ENDPOINT.
//
// The index supports three first-class lookups over canonical intelligence:
//   - vulnerability lookup   (by alias/CVE/GHSA/OSV id, or free text)
//   - package lookup         (by purl name / ecosystem)
//   - package-version lookup (purl name + a concrete version, matched against
//     the affected half-open [introduced, fixed) ranges stored per document)
package search

import "context"

// Document is a single canonical vulnerability projected into the shape the
// index stores. It is a lookup projection, not the authoritative model — the
// full record always lives in PostgreSQL — so Result references a Document by
// ID rather than carrying the whole payload.
type Document struct {
	// ID is the canonical vulnerability id (intel_vulnerabilities primary key).
	ID string `json:"id"`

	// Aliases are the alternate identifiers (CVE, GHSA, OSV, PYSEC, ...) that a
	// vulnerability lookup can match. Stored normalized (uppercased).
	Aliases []string `json:"aliases,omitempty"`

	// Affected is the per-package affected-range projection that powers package
	// and package-version lookups.
	Affected []AffectedPackage `json:"affected,omitempty"`

	// Purls is the flattened set of affected purl names (no version), kept
	// alongside Affected so a plain package lookup is a single term query.
	Purls []string `json:"purls,omitempty"`

	// Text is the free-text content (summary) made searchable.
	Text string `json:"text,omitempty"`

	// Severity / KnownExploited / EPSS / CVSS are filterable, sortable facets.
	Severity       string  `json:"severity,omitempty"`
	KnownExploited bool    `json:"known_exploited"`
	EPSS           float64 `json:"epss,omitempty"`
	CVSS           float64 `json:"cvss,omitempty"`

	// Sources lists which intel sources contributed, for provenance filtering.
	Sources []string `json:"sources,omitempty"`
}

// AffectedPackage is the index projection of one affected package: its purl
// name, ecosystem, and the half-open version ranges in which it is affected.
type AffectedPackage struct {
	Purl      string          `json:"purl"`
	Ecosystem string          `json:"ecosystem,omitempty"`
	Ranges    []AffectedRange `json:"ranges,omitempty"`
}

// AffectedRange is a half-open [Introduced, Fixed) interval. Empty Introduced
// means "from the start"; empty Fixed means "unbounded above".
type AffectedRange struct {
	Introduced string `json:"introduced,omitempty"`
	Fixed      string `json:"fixed,omitempty"`
}

// Query describes a search request. The three lookup shapes are expressed by
// which fields are set; all set constraints are ANDed:
//   - vulnerability: set Alias or Text
//   - package:       set Purl (and optionally Ecosystem)
//   - version:       set Purl + Version (post-filtered against affected ranges)
type Query struct {
	// Text is free-text query terms over the summary. Empty = no text constraint.
	Text string

	// Alias restricts to documents carrying this identifier (CVE/GHSA/OSV/...).
	Alias string

	// Purl restricts to documents affecting this package (name, no version).
	Purl string

	// Ecosystem optionally narrows a purl/package lookup by ecosystem.
	Ecosystem string

	// Version, when set together with Purl, keeps only documents whose affected
	// ranges for that purl contain the version (introduced <= version < fixed).
	Version string

	// Severity / KnownExploited are exact-match facets. KnownExploitedOnly
	// gates whether KnownExploited is applied as a filter at all.
	Severity           string
	KnownExploitedOnly bool

	// Limit caps results. Zero or negative uses the implementation default.
	Limit int
}

// Result is a single match, referencing the canonical record by ID.
type Result struct {
	ID    string
	Score float64
}

// Indexer indexes canonical intelligence documents and serves lookups. It is
// safe for concurrent use. The OpenSearch-backed implementation lives in
// opensearch.go; PostgreSQL stays the system of record, so if the index is lost
// it is rebuilt from Postgres via Rebuild (through an injected DocumentSource).
type Indexer interface {
	// EnsureIndex creates the index with the canonical mapping if it is absent.
	EnsureIndex(ctx context.Context) error

	// Index upserts the given documents keyed on Document.ID. Idempotent:
	// re-indexing the same documents must not create duplicates.
	Index(ctx context.Context, docs []Document) error

	// Search runs a query and returns matching ids + scores, highest first.
	// An empty slice (not an error) means no matches.
	Search(ctx context.Context, q Query) ([]Result, error)

	// Rebuild drops the index and reindexes the full corpus from src.
	Rebuild(ctx context.Context, src DocumentSource) error
}

// DocumentSource yields the full canonical corpus as index documents. The
// database.IntelStore satisfies an adapter around this via FromCanonical, so
// the index is always rebuildable from the authoritative Postgres store.
type DocumentSource interface {
	// Documents returns every canonical vulnerability projected for indexing.
	Documents(ctx context.Context) ([]Document, error)
}
