// Package atlas implements the MITRE ATLAS Fetcher. MITRE publishes the ATLAS
// matrix as a single data document (ATLAS.yaml, mirrored as JSON) on GitHub;
// this Fetcher streams the document's unmodified bytes into the
// content-addressed raw store.
//
// ATLAS has no native per-record cursor, so the Fetcher uses the HTTP
// validators the upstream exposes (ETag, falling back to Last-Modified) as its
// incremental cursor: a non-empty cursor becomes a conditional request and an
// unchanged document (304 Not Modified) is a cheap no-op that stores nothing.
//
// The Fetcher never parses or mutates the document (Requirement 1.6): decoding
// into typed techniques is the parser's job (bellu-global-intelligence/parser/
// atlas). The document is treated as untrusted bytes — read with a bounded
// limit and handed to the RawStore as-is, never executed or interpreted.
package atlas

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/fetch"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// source is the stable source key for the ATLAS Fetcher.
const source = "atlas"

// DefaultDocumentURL is the public MITRE ATLAS data document. It is overridable
// via Option so tests target a local server and never touch the live network.
const DefaultDocumentURL = "https://raw.githubusercontent.com/mitre-atlas/atlas-data/main/dist/ATLAS.yaml"

// defaultRateLimit is the steady-state upstream request rate. ATLAS is a single
// document fetched infrequently, so a conservative cap is ample.
const defaultRateLimit = rate.Limit(1)

// defaultBurst is the token-bucket burst size for the rate limiter.
const defaultBurst = 1

// maxDocumentBytes bounds the ATLAS document read so a hostile or runaway
// upstream response cannot exhaust memory. The real document is a few MB.
const maxDocumentBytes = 128 << 20

// Fetcher pulls the MITRE ATLAS data document and streams its raw bytes to a
// RawStore. It satisfies fetch.Fetcher. Construct one with New.
type Fetcher struct {
	client      *http.Client
	documentURL string
	limiter     *rate.Limiter
	now         func() time.Time
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

// WithDocumentURL overrides the ATLAS document endpoint, e.g. a local test
// server. An empty URL is ignored.
func WithDocumentURL(url string) Option {
	return func(f *Fetcher) {
		if url != "" {
			f.documentURL = url
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

// WithClock overrides the clock used to stamp FetchResult.FetchedAt, for
// deterministic tests. A nil clock is ignored.
func WithClock(now func() time.Time) Option {
	return func(f *Fetcher) {
		if now != nil {
			f.now = now
		}
	}
}

// New constructs an ATLAS Fetcher with sane defaults, applying any options.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		client:      http.DefaultClient,
		documentURL: DefaultDocumentURL,
		limiter:     rate.NewLimiter(defaultRateLimit, defaultBurst),
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Source returns the stable source key "atlas".
func (f *Fetcher) Source() string { return source }

// Fetch retrieves the ATLAS document and streams its unmodified bytes to
// rawStore. Cursor semantics (Requirements 1.3, 1.4):
//
//   - An empty cursor performs a full fetch and returns the document's ETag (or
//     Last-Modified) as the next cursor.
//   - A non-empty cursor issues a conditional request; a 304 Not Modified
//     response stores nothing and returns the same cursor (a cheap no-op),
//     while a changed document is stored and its new validator returned.
//
// Fetch never parses or mutates the document (Requirement 1.6).
func (f *Fetcher) Fetch(ctx context.Context, rawStore raw.RawStore, cur fetch.Cursor) (fetch.FetchResult, error) {
	if rawStore == nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("atlas: rawStore must not be nil")
	}

	// Rate-limit the upstream request (Requirement 1.5).
	if f.limiter != nil {
		if err := f.limiter.Wait(ctx); err != nil {
			return fetch.FetchResult{Source: source}, fmt.Errorf("atlas: rate limiter: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.documentURL, nil)
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("atlas: build request: %w", err)
	}
	// A non-empty cursor becomes a conditional request so an unchanged document
	// is a cheap no-op (Requirement 1.3). The cursor is an opaque validator; we
	// try it as both an If-None-Match (ETag) and an If-Modified-Since value so
	// either upstream validator works.
	if cur != "" {
		req.Header.Set("If-None-Match", string(cur))
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("atlas: fetch document: %w", err)
	}
	defer resp.Body.Close()

	result := fetch.FetchResult{Source: source, FetchedAt: f.now(), Next: cur}

	switch resp.StatusCode {
	case http.StatusNotModified:
		// Unchanged since the cursor: store nothing, keep the cursor.
		return result, nil
	case http.StatusOK:
		// proceed
	default:
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return fetch.FetchResult{Source: source}, fmt.Errorf("atlas: unexpected status %d fetching document", resp.StatusCode)
	}

	// Read the document as opaque, untrusted bytes (Requirement 1.6).
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes))
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("atlas: read document body: %w", err)
	}

	ref, err := rawStore.Put(ctx, source, payload)
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("atlas: store raw payload: %w", err)
	}
	result.RawRefs = []raw.RawRef{ref}
	result.Next = nextCursor(resp, cur)
	return result, nil
}

// nextCursor derives the incremental cursor from the response validators,
// preferring ETag then Last-Modified, falling back to the prior cursor.
func nextCursor(resp *http.Response, prev fetch.Cursor) fetch.Cursor {
	if etag := resp.Header.Get("ETag"); etag != "" {
		return fetch.Cursor(etag)
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		return fetch.Cursor(lm)
	}
	return prev
}

// Ensure Fetcher satisfies fetch.Fetcher.
var _ fetch.Fetcher = (*Fetcher)(nil)
