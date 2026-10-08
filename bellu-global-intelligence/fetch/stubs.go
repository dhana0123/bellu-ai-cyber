package fetch

import (
	"context"
	"errors"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// ErrReservedSource is returned by a reserved-for-later Fetcher stub when its
// Fetch method is invoked. These sources are defined as interface stubs now
// (Requirement 1.7) and implemented in a later phase.
var ErrReservedSource = errors.New("fetch: source reserved for a later phase, not yet implemented")

// reservedFetcher is a stub Fetcher for sources that are planned but not yet
// implemented. It satisfies the Fetcher interface so the source key is
// addressable, but every Fetch returns ErrReservedSource rather than
// contacting any upstream.
type reservedFetcher struct {
	source string
}

// Source returns the stable source key for this reserved source.
func (f reservedFetcher) Source() string { return f.source }

// Fetch always returns ErrReservedSource: the source is reserved for a later
// phase and performs no upstream request.
func (f reservedFetcher) Fetch(_ context.Context, _ raw.RawStore, _ Cursor) (FetchResult, error) {
	return FetchResult{Source: f.source}, ErrReservedSource
}

// Reserved-for-later Fetcher stubs (Requirement 1.7). NVD, GHSA, and vendor
// advisories remain reserved stubs: each returns a Fetcher whose Source() is
// addressable and whose Fetch() returns ErrReservedSource.
//
// The former reserved stubs for ATLAS, OWASP GenAI, AVID, and MCP have been
// superseded by real Phase-3 implementations (Requirement 15.1). To avoid two
// conflicting Fetchers for the same source key, those stub constructors were
// removed; construct the real Fetchers directly from their subpackages:
// fetch/atlas, fetch/owasp, fetch/avid, fetch/mcp (each exposes New(...Option)).
func NewNVDFetcher() Fetcher            { return reservedFetcher{source: "nvd"} }
func NewGHSAFetcher() Fetcher           { return reservedFetcher{source: "ghsa"} }
func NewVendorAdvisoryFetcher() Fetcher { return reservedFetcher{source: "vendor-advisory"} }

// Compile-time assertion that the stub satisfies the Fetcher interface.
var _ Fetcher = reservedFetcher{}
