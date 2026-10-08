// Package owasp implements the OWASP GenAI / LLM Top 10 Fetcher. The OWASP
// GenAI project is release/document based with no native machine cursor, so
// this Fetcher pulls a single configurable JSON feed and streams its unmodified
// bytes into the content-addressed raw store.
//
// Because the exact upstream artifact is release-driven rather than a stable
// API, the feed URL is injectable/configurable (DefaultFeedURL is a sensible
// default, not a brittle scrape target). The Fetcher uses the HTTP ETag /
// Last-Modified validators as its incremental cursor: a non-empty cursor
// becomes a conditional request and a 304 Not Modified is a cheap no-op.
//
// The Fetcher never parses or mutates the feed (Requirement 1.6): the payload
// is read as bounded, untrusted bytes and handed to the RawStore as-is.
// Assumed format: a JSON document matching parser/owasp (an "entries"/"risks"
// array of LLM Top 10 risks).
package owasp

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/global-intel/fetch"
	"github.com/bellu/bellu-ai-cyber/global-intel/fetch/internal/jsonfeed"
	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

// source is the stable source key for the OWASP GenAI Fetcher.
const source = "owasp-genai"

// DefaultFeedURL is the default OWASP GenAI feed endpoint. It is a configurable
// default (overridable via Option), not a hardcoded scrape, because the
// upstream artifact is release-driven.
const DefaultFeedURL = "https://raw.githubusercontent.com/OWASP/www-project-top-10-for-large-language-model-applications/main/dist/owasp-llm-top10.json"

const (
	defaultRateLimit = rate.Limit(1)
	defaultBurst     = 1
	maxFeedBytes     = 64 << 20
)

// Fetcher pulls the OWASP GenAI feed and streams its raw bytes to a RawStore.
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

// WithFeedURL overrides the feed endpoint, e.g. a local test server or a
// customer-pinned release artifact. An empty URL is ignored.
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

// New constructs an OWASP GenAI Fetcher with sane defaults, applying options.
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

// Source returns the stable source key "owasp-genai".
func (f *Fetcher) Source() string { return source }

// Fetch retrieves the OWASP GenAI feed and streams its unmodified bytes to
// rawStore, using the HTTP ETag/Last-Modified validators as the incremental
// cursor (Requirements 1.3, 1.4, 1.6). See package doc for cursor semantics.
func (f *Fetcher) Fetch(ctx context.Context, rawStore raw.RawStore, cur fetch.Cursor) (fetch.FetchResult, error) {
	return jsonfeed.Fetch(ctx, source, f.client, f.feedURL, f.limiter, f.now, maxFeedBytes, rawStore, cur)
}

// Ensure Fetcher satisfies fetch.Fetcher.
var _ fetch.Fetcher = (*Fetcher)(nil)
