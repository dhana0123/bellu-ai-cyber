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

// Reserved-for-later Fetcher stubs (Requirement 1.7). NVD, GHSA, ATLAS, OWASP
// GenAI, AVID, MCP intelligence, and vendor advisories are exposed as Fetcher
// stubs now and implemented in a later phase. Each returns a Fetcher whose
// Source() is addressable and whose Fetch() returns ErrReservedSource.
func NewNVDFetcher() Fetcher            { return reservedFetcher{source: "nvd"} }
func NewGHSAFetcher() Fetcher           { return reservedFetcher{source: "ghsa"} }
func NewATLASFetcher() Fetcher          { return reservedFetcher{source: "atlas"} }
func NewOWASPGenAIFetcher() Fetcher     { return reservedFetcher{source: "owasp-genai"} }
func NewAVIDFetcher() Fetcher           { return reservedFetcher{source: "avid"} }
func NewMCPFetcher() Fetcher            { return reservedFetcher{source: "mcp"} }
func NewVendorAdvisoryFetcher() Fetcher { return reservedFetcher{source: "vendor-advisory"} }

// Compile-time assertion that the stub satisfies the Fetcher interface.
var _ Fetcher = reservedFetcher{}
