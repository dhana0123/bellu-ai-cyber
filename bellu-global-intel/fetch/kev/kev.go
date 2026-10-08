// Package kev implements the CISA Known Exploited Vulnerabilities (KEV)
// Fetcher. KEV is published as a single JSON catalog carrying a
// "catalogVersion" string; this Fetcher streams the catalog's unmodified bytes
// into the content-addressed raw store and uses catalogVersion as the
// incremental cursor so an unchanged catalog is a cheap no-op.
//
// This Fetcher never interprets the catalog's vulnerability records: decoding
// the catalog into typed source records is the parser's job (see
// bellu-global-intelligence/parser/kev). The only field inspected here is the
// top-level catalogVersion, used purely as the transport-level cursor.
package kev

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/fetch"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/raw"
)

// source is the stable source key for the KEV Fetcher.
const source = "kev"

// DefaultCatalogURL is the public CISA KEV catalog endpoint. It is overridable
// via Option so tests can point at a local test server and never touch the
// live network.
const DefaultCatalogURL = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"

// defaultRateLimit is the steady-state request rate applied to upstream
// requests. KEV is a single small catalog fetched infrequently, so a
// conservative one-request-per-second limit with a small burst is ample.
const defaultRateLimit = rate.Limit(1)

// defaultBurst is the token-bucket burst size for the rate limiter.
const defaultBurst = 1

// Fetcher pulls the CISA KEV catalog and streams its raw bytes to a RawStore.
// It satisfies fetch.Fetcher. The zero value is not usable; construct one with
// New.
type Fetcher struct {
	client     *http.Client
	catalogURL string
	limiter    *rate.Limiter
	now        func() time.Time
}

// Option customizes a Fetcher. All upstream-facing dependencies (HTTP client,
// endpoint, rate limiter, clock) are injectable so the Fetcher is testable
// without live network access.
type Option func(*Fetcher)

// WithHTTPClient sets the HTTP client used for upstream requests. A nil client
// is ignored.
func WithHTTPClient(c *http.Client) Option {
	return func(f *Fetcher) {
		if c != nil {
			f.client = c
		}
	}
}

// WithCatalogURL overrides the KEV catalog endpoint, e.g. to target a local
// test server. An empty URL is ignored.
func WithCatalogURL(url string) Option {
	return func(f *Fetcher) {
		if url != "" {
			f.catalogURL = url
		}
	}
}

// WithRateLimiter overrides the rate limiter applied to upstream requests. A
// nil limiter is ignored.
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

// New constructs a KEV Fetcher with sane defaults, applying any options. By
// default it uses http.DefaultClient, the public catalog URL, a conservative
// rate limiter, and the wall clock.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		client:     http.DefaultClient,
		catalogURL: DefaultCatalogURL,
		limiter:    rate.NewLimiter(defaultRateLimit, defaultBurst),
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Source returns the stable source key "kev".
func (f *Fetcher) Source() string { return source }

// Fetch retrieves the KEV catalog and streams its unmodified bytes to
// rawStore. The catalog's catalogVersion is used as the incremental cursor:
//
//   - An empty cursor performs a full fetch and returns the fetched catalog's
//     catalogVersion as the next cursor.
//   - A non-empty cursor that matches the freshly fetched catalogVersion means
//     the catalog is unchanged, so Fetch stores nothing and returns the same
//     cursor (a cheap no-op). A non-empty cursor that differs stores the new
//     catalog and returns the new catalogVersion.
//
// Fetch never parses or mutates the catalog's records: only the top-level
// catalogVersion is inspected, and the exact original bytes are what get
// stored.
func (f *Fetcher) Fetch(ctx context.Context, rawStore raw.RawStore, cur fetch.Cursor) (fetch.FetchResult, error) {
	if rawStore == nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("kev: rawStore must not be nil")
	}

	// Rate-limit the upstream request (Requirement 1.5).
	if f.limiter != nil {
		if err := f.limiter.Wait(ctx); err != nil {
			return fetch.FetchResult{Source: source}, fmt.Errorf("kev: rate limiter: %w", err)
		}
	}

	payload, err := f.get(ctx, f.catalogURL)
	if err != nil {
		return fetch.FetchResult{Source: source}, err
	}

	// Read only the catalogVersion for the cursor decision. Treat upstream data
	// as untrusted: a malformed or missing version is reported rather than
	// trusted or silently defaulted.
	version, err := catalogVersion(payload)
	if err != nil {
		return fetch.FetchResult{Source: source}, err
	}

	result := fetch.FetchResult{
		Source:    source,
		Next:      fetch.Cursor(version),
		FetchedAt: f.now(),
	}

	// Non-empty cursor matching the current catalog version: the catalog is
	// unchanged, so this is a no-op and nothing is stored (Requirements
	// 1.3/1.4).
	if cur != "" && string(cur) == version {
		return result, nil
	}

	// Stream the exact, unmodified bytes to the raw store (Requirement 1.6).
	ref, err := rawStore.Put(ctx, source, payload)
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("kev: store raw payload: %w", err)
	}
	result.RawRefs = []raw.RawRef{ref}
	return result, nil
}

// get performs a single rate-limited GET and returns the response body bytes
// unmodified. It bounds the read so an unexpectedly large or hostile upstream
// response cannot exhaust memory (upstream data is untrusted).
func (f *Fetcher) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("kev: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kev: fetch catalog: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kev: unexpected status %d fetching catalog", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	if err != nil {
		return nil, fmt.Errorf("kev: read catalog body: %w", err)
	}
	return body, nil
}

// maxCatalogBytes bounds the KEV catalog read. The real catalog is a few MB;
// 256 MiB is a generous ceiling that still protects against a hostile or
// runaway response.
const maxCatalogBytes = 256 << 20

// catalogVersion extracts only the top-level "catalogVersion" string from the
// raw KEV JSON for use as the incremental cursor. It intentionally does not
// decode the vulnerability records (that is the parser's responsibility) and
// treats the payload as untrusted input.
func catalogVersion(payload []byte) (string, error) {
	var head struct {
		CatalogVersion string `json:"catalogVersion"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return "", fmt.Errorf("kev: decode catalogVersion: %w", err)
	}
	if head.CatalogVersion == "" {
		return "", fmt.Errorf("kev: catalog missing catalogVersion")
	}
	return head.CatalogVersion, nil
}

// Ensure Fetcher satisfies fetch.Fetcher.
var _ fetch.Fetcher = (*Fetcher)(nil)
