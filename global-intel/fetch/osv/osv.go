// Package osv implements the OSV (Open Source Vulnerabilities) Fetcher. It
// pulls the per-ecosystem bulk vulnerability archives OSV publishes and streams
// the raw bytes, unmodified, into the content-addressed raw store.
//
// Phase 1 implements the purl ecosystems with PyPI as the first ecosystem
// (Requirement 1.2). The fetcher never parses or mutates payloads
// (Requirement 1.6) — decoding is the OSV parser's job (task 6.1). Upstream
// data is treated as untrusted: it is read as opaque bytes and handed to the
// RawStore; nothing in it is executed or interpreted.
package osv

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/global-intel/fetch"
	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

const (
	// source is the stable source key for the OSV fetcher.
	source = "osv"

	// DefaultBaseURL is the public OSV bulk-export bucket. Per-ecosystem
	// archives live at "<base>/<Ecosystem>/all.zip".
	DefaultBaseURL = "https://osv-vulns.storage.googleapis.com"

	// DefaultRateLimit is the sustained request rate applied to upstream
	// requests (Requirement 1.5). OSV's bucket is generous, but a modest cap
	// keeps the fetcher a well-behaved client.
	DefaultRateLimit = rate.Limit(2) // 2 requests/second
	// DefaultBurst is the number of requests allowed to burst above the rate.
	DefaultBurst = 1

	// httpTimeout bounds a single upstream request so a stalled connection
	// cannot hang a run indefinitely.
	httpTimeout = 5 * time.Minute
)

// DefaultEcosystems is the ordered set of purl ecosystems the fetcher pulls.
// PyPI is first (Requirement 1.2); additional ecosystems can be enabled by
// overriding Fetcher.Ecosystems.
var DefaultEcosystems = []string{"PyPI"}

// Fetcher pulls OSV bulk vulnerability archives for one or more purl
// ecosystems and streams the raw bytes into a raw.RawStore.
//
// The upstream endpoint, the set of ecosystems, the HTTP client, and the rate
// limit are all configurable via struct fields; zero values fall back to
// sensible defaults, so a zero Fetcher is usable. Supplying an in-memory
// http.Client (via a custom Transport) makes the fetcher fully testable
// without any live network access.
type Fetcher struct {
	// BaseURL is the upstream bulk-export root. Empty means DefaultBaseURL.
	BaseURL string
	// Ecosystems is the ordered list of purl ecosystems to fetch. Empty means
	// DefaultEcosystems (PyPI first).
	Ecosystems []string
	// HTTPClient issues upstream requests. Nil means a default client with a
	// bounded timeout. Inject a client with a custom RoundTripper to test the
	// fetcher without real network calls.
	HTTPClient *http.Client
	// Limiter rate-limits upstream requests (Requirement 1.5). Nil means a
	// limiter built from RateLimit/Burst (or their defaults).
	Limiter *rate.Limiter
	// RateLimit is the sustained upstream request rate used when Limiter is
	// nil. Zero means DefaultRateLimit.
	RateLimit rate.Limit
	// Burst is the burst size used when Limiter is nil. Zero means DefaultBurst.
	Burst int
}

// Compile-time assertion that *Fetcher satisfies the fetch.Fetcher interface.
var _ fetch.Fetcher = (*Fetcher)(nil)

// New returns an OSV Fetcher that writes into the given raw store using the
// default endpoint, ecosystems, HTTP client, and rate limit. Callers that need
// to customize any of these can construct a Fetcher literal directly.
func New() *Fetcher { return &Fetcher{} }

// Source returns the stable OSV source key.
func (f *Fetcher) Source() string { return source }

// Fetch retrieves the OSV bulk archives for the configured ecosystems and
// streams their raw bytes to rawStore.
//
// Cursor semantics (Requirements 1.3, 1.4):
//   - An empty cursor performs a full fetch of every configured ecosystem.
//   - A non-empty cursor is a conditional marker (the HTTP Last-Modified value
//     from the previous run). The fetcher issues an If-Modified-Since request;
//     an archive the upstream reports as unchanged (304 Not Modified) is
//     skipped, so only changed payloads are retrieved.
//
// The returned Next cursor is the newest Last-Modified value observed across
// the fetched ecosystems, suitable for the next incremental run. When nothing
// changed, the incoming cursor is returned unchanged.
//
// Fetch never parses or mutates payloads (Requirement 1.6): each archive is
// read as opaque bytes and handed to rawStore.Put as-is.
func (f *Fetcher) Fetch(ctx context.Context, rawStore raw.RawStore, cur fetch.Cursor) (fetch.FetchResult, error) {
	if rawStore == nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("osv: fetch: raw store is nil")
	}

	limiter := f.limiter()
	ecosystems := f.ecosystems()
	base := strings.TrimRight(f.baseURL(), "/")

	result := fetch.FetchResult{
		Source:    source,
		FetchedAt: time.Now().UTC(),
	}
	// Carry the incoming cursor forward so an all-unchanged run returns it
	// unchanged; replace it as newer Last-Modified values are observed.
	nextCursor := cur

	for _, eco := range ecosystems {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		url := fmt.Sprintf("%s/%s/all.zip", base, eco)
		ref, lastModified, changed, err := f.fetchArchive(ctx, limiter, rawStore, url, string(cur))
		if err != nil {
			return result, fmt.Errorf("osv: fetch ecosystem %q: %w", eco, err)
		}
		if !changed {
			// Upstream reports this archive unchanged since the cursor; skip it
			// so only changed payloads are retrieved (Requirement 1.3).
			continue
		}
		result.RawRefs = append(result.RawRefs, ref)
		if lastModified != "" {
			nextCursor = fetch.Cursor(lastModified)
		}
	}

	result.Next = nextCursor
	return result, nil
}

// fetchArchive performs a single rate-limited, optionally conditional GET and
// streams the response body into rawStore without parsing or mutating it.
//
// It returns the stored reference, the response's Last-Modified header (for the
// next cursor), and whether the payload was fetched (changed). A 304 Not
// Modified response reports changed=false and stores nothing.
func (f *Fetcher) fetchArchive(ctx context.Context, limiter *rate.Limiter, rawStore raw.RawStore, url, cursor string) (raw.RawRef, string, bool, error) {
	// Apply rate limiting to every upstream request (Requirement 1.5).
	if err := limiter.Wait(ctx); err != nil {
		return raw.RawRef{}, "", false, fmt.Errorf("rate limit wait: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return raw.RawRef{}, "", false, fmt.Errorf("build request: %w", err)
	}
	// A non-empty cursor turns this into a conditional request so unchanged
	// archives are cheap no-ops (Requirement 1.3).
	if cursor != "" {
		req.Header.Set("If-Modified-Since", cursor)
	}

	resp, err := f.httpClient().Do(req)
	if err != nil {
		return raw.RawRef{}, "", false, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		// Nothing changed since the cursor.
		return raw.RawRef{}, resp.Header.Get("Last-Modified"), false, nil
	case http.StatusOK:
		// proceed
	default:
		// Drain a bounded amount so the connection can be reused, then fail.
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return raw.RawRef{}, "", false, fmt.Errorf("unexpected status %s", resp.Status)
	}

	// Read the archive as opaque bytes. No parsing or mutation (Requirement
	// 1.6); the payload is treated as untrusted and never executed.
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return raw.RawRef{}, "", false, fmt.Errorf("read body: %w", err)
	}

	ref, err := rawStore.Put(ctx, source, payload)
	if err != nil {
		return raw.RawRef{}, "", false, fmt.Errorf("store payload: %w", err)
	}
	return ref, resp.Header.Get("Last-Modified"), true, nil
}

// baseURL returns the configured upstream root or the default.
func (f *Fetcher) baseURL() string {
	if f.BaseURL != "" {
		return f.BaseURL
	}
	return DefaultBaseURL
}

// ecosystems returns the configured ecosystems or the default (PyPI first).
func (f *Fetcher) ecosystems() []string {
	if len(f.Ecosystems) > 0 {
		return f.Ecosystems
	}
	return DefaultEcosystems
}

// httpClient returns the injected HTTP client or a default with a bounded
// timeout.
func (f *Fetcher) httpClient() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return &http.Client{Timeout: httpTimeout}
}

// limiter returns the injected limiter or builds one from RateLimit/Burst
// (falling back to the defaults).
func (f *Fetcher) limiter() *rate.Limiter {
	if f.Limiter != nil {
		return f.Limiter
	}
	limit := f.RateLimit
	if limit <= 0 {
		limit = DefaultRateLimit
	}
	burst := f.Burst
	if burst <= 0 {
		burst = DefaultBurst
	}
	return rate.NewLimiter(limit, burst)
}
