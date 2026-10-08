// Package search defines the interface for a fast lookup/index layer over the
// canonical global-intelligence data.
//
// DEFERRED / INTERFACE ONLY (Requirement 17).
//
// In Phase 1 this package is intentionally an interface definition only: it
// declares the contract a future search backend must satisfy, plus the small
// plain types that cross that boundary. There is no implementation here and no
// OpenSearch (or any other search engine) runtime dependency is added to the
// module — this package imports only the standard library (Req 17.1, 17.2).
//
// Phase 1 relies on PostgreSQL B-tree and optional full-text indexes, which are
// sufficient for the purl/alias lookups the system needs today. An
// OpenSearch-backed implementation of Indexer is future work (Phase 3).
//
// PostgreSQL remains the authoritative system of record. The search index is a
// derived, disposable view: if it is lost or corrupted it can be dropped and
// rebuilt in full from the canonical intel_* tables via Indexer.Rebuild.
package search

import "context"

// Document is a single canonical intelligence record projected into the shape
// the index stores. It is a lookup projection, not the authoritative model: the
// full record always lives in PostgreSQL. ID is the canonical vulnerability id
// (the system of record's primary key) and is what Result references back to.
type Document struct {
	// ID is the canonical identifier of the indexed record (e.g. the
	// intel_vulnerabilities primary key). It links a search Result back to the
	// authoritative row in PostgreSQL.
	ID string

	// Aliases are the known alternate identifiers for the record (e.g. CVE,
	// GHSA, OSV ids) that should match a query.
	Aliases []string

	// Purls are the affected package URLs associated with the record, enabling
	// package-oriented lookups.
	Purls []string

	// Text is the free-text content (title, summary, details) made searchable
	// for full-text queries.
	Text string

	// Fields carries additional filterable attributes (e.g. "severity",
	// "known_exploited") as plain string values. Kept generic so the index
	// contract does not have to change as the canonical model grows.
	Fields map[string]string
}

// Query describes a search request against the index.
type Query struct {
	// Text is the free-text query terms. Empty means no full-text constraint.
	Text string

	// Purl, when set, restricts results to documents affecting this package URL.
	Purl string

	// Alias, when set, restricts results to documents carrying this identifier.
	Alias string

	// Filters are exact-match constraints on Document.Fields (e.g.
	// {"severity": "critical"}). All filters must match (logical AND).
	Filters map[string]string

	// Limit caps the number of results returned. A zero or negative value means
	// the implementation's default limit applies.
	Limit int
}

// Result is a single match for a Query, ordered by descending Score by the
// implementation. It references the canonical record by ID rather than carrying
// the full document, since PostgreSQL is the system of record.
type Result struct {
	// ID is the canonical identifier of the matching record (see Document.ID).
	ID string

	// Score is the relevance score assigned by the backend; higher is more
	// relevant. Scores are only comparable within a single Search response.
	Score float64
}

// Indexer indexes canonical intelligence documents for fast lookup.
//
// It is an interface only in Phase 1; an OpenSearch-backed implementation is
// future work (Requirement 17), and no search-engine dependency is added now.
// PostgreSQL remains the system of record; if the index is lost it is rebuilt
// from Postgres via Rebuild.
//
// Implementations should treat all methods as safe to call concurrently.
type Indexer interface {
	// Index adds or updates the given documents in the index. It is an upsert
	// keyed on Document.ID and is idempotent: re-indexing the same documents
	// must not create duplicates.
	Index(ctx context.Context, docs []Document) error

	// Search runs a query and returns matching document ids and scores, ordered
	// by descending Score. An empty slice (not an error) means no matches.
	Search(ctx context.Context, q Query) ([]Result, error)

	// Rebuild drops the index and rebuilds it in full from the authoritative
	// store (PostgreSQL). It exists so the index can be recovered from scratch,
	// since the index is a derived, disposable view of the canonical data.
	Rebuild(ctx context.Context) error
}
