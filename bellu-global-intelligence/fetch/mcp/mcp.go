// Package mcp implements the Model Context Protocol (MCP) security-advisory
// Fetcher. MCP security intelligence is advisory-style; this Fetcher pulls a
// single configurable JSON advisory feed and streams its unmodified bytes into
// the content-addressed raw store.
//
// Because there is no single canonical MCP advisory API yet, the feed URL is
// injectable/configurable (DefaultFeedURL is a sensible default, not a brittle
// scrape). The Fetcher uses the HTTP ETag/Last-Modified validators as its
// incremental cursor, so an unchanged feed is a cheap 304 no-op.
//
// The Fetcher never parses or mutates the feed (Requirement 1.6). Assumed
// format: a JSON document matching parser/mcp (an "advisories" array).
package mcp

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/fetch"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/fetch/internal/jsonfeed"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// source is the stable source key for the MCP Fetcher.
const source = "mcp"

// DefaultFeedURL is the default MCP advisory feed endpoint. It is a
// configurable default (overridable via Option), not a hardcoded scrape.
const DefaultFeedURL = "https://raw.githubusercontent.com/modelcontextprotocol/advisories/main/dist/advisories.json"

const (
	defaultRateLimit = rate.Limit(1)
	defaultBurst     = 1
	maxFeedBytes     = 64 << 20
)

// Fetcher pulls the MCP advisory feed and streams its raw bytes to a RawStore.
// It satisfies fetch.Fetcher. Construct one with New.
type Fetcher struct {
	client  *http.Client
	feedURL string
	limiter *rate.Limiter
	now     func() time.Time
}

// Option customizes a Fetcher. All upstream-facing dependencies are injectable
// so the Fetcher is testable without live network access.
type Option func(*Fetcher)

// WithHTTPClient sets the HTTP client used for upstream requests. Nil ignored.
func WithHTTPClient(c *http.Client) Option {
	return func(f *Fetcher) {
		if c != nil {
			f.client = c
		}
	}
}

// WithFeedURL overrides the feed endpoint, e.g. a local test server. An empty
// URL is ignored.
func WithFeedURL(url string) Option {
	return func(f *Fetcher) {
		if url != "" {
			f.feedURL = url
		}
	}
}

// WithRateLimiter overrides the rate limiter. A nil limiter is ignored.
func WithRateLimiter(l *rate.Limiter) Option {
	return func(f *Fetcher) {
		if l != nil {
			f.limiter = l
		}
	}
}

// WithClock overrides the clock used to stamp FetchResult.FetchedAt. Nil
// ignored.
func WithClock(now func() time.Time) Option {
	return func(f *Fetcher) {
		if now != nil {
			f.now = now
		}
	}
}

// New constructs an MCP Fetcher with sane defaults, applying options.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		client:  http.DefaultClient,
		feedURL: DefaultFeedURL,
		limiter: rate.NewLimiter(defaultRateLimit, defaultBurst),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Source returns the stable source key "mcp".
func (f *Fetcher) Source() string { return source }

// Fetch retrieves the MCP advisory feed and streams its unmodified bytes to
// rawStore, using the HTTP ETag/Last-Modified validators as the incremental
// cursor (Requirements 1.3, 1.4, 1.6).
func (f *Fetcher) Fetch(ctx context.Context, rawStore raw.RawStore, cur fetch.Cursor) (fetch.FetchResult, error) {
	return jsonfeed.Fetch(ctx, source, f.client, f.feedURL, f.limiter, f.now, maxFeedBytes, rawStore, cur)
}

// Ensure Fetcher satisfies fetch.Fetcher.
var _ fetch.Fetcher = (*Fetcher)(nil)
