// Package epss implements the FIRST Exploit Prediction Scoring System (EPSS)
// Fetcher. EPSS publishes one gzipped CSV per day at a date-stamped URL
// (e.g. epss_scores-YYYY-MM-DD.csv.gz); this Fetcher streams each day's
// unmodified bytes into the content-addressed raw store and uses the scored
// date (YYYY-MM-DD) as the incremental cursor.
//
// This Fetcher never interprets the CSV's score rows: decoding a day's CSV
// into typed source records is the parser's job (see
// bellu-global-intelligence/parser/epss). Only the scored date is used here,
// and purely as the transport-level cursor.
package epss

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/fetch"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// source is the stable source key for the EPSS Fetcher.
const source = "epss"

// DefaultBaseURL is the public FIRST EPSS daily-scores base URL. The Fetcher
// appends "epss_scores-YYYY-MM-DD.csv.gz" to this base. It is overridable via
// Option so tests can point at a local test server and never touch the live
// network.
const DefaultBaseURL = "https://epss.cyentia.com/"

// dateLayout is the scored-date layout EPSS uses in its daily file names and
// that this Fetcher uses for its cursor.
const dateLayout = "2006-01-02"

// defaultRateLimit is the steady-state request rate applied to upstream
// requests. A conservative one-request-per-second limit with a small burst is
// ample for backfilling a handful of daily files.
const defaultRateLimit = rate.Limit(1)

// defaultBurst is the token-bucket burst size for the rate limiter.
const defaultBurst = 1

// maxBackfillDays bounds how many daily files a single incremental Fetch will
// retrieve when catching up from an old cursor, so one run cannot issue an
// unbounded number of upstream requests.
const maxBackfillDays = 30

// maxFileBytes bounds each daily CSV read. A day's gzipped CSV is a few MB;
// 256 MiB is a generous ceiling that still protects against a hostile or
// runaway response (upstream data is untrusted).
const maxFileBytes = 256 << 20

// Fetcher pulls FIRST EPSS daily score files and streams their raw bytes to a
// RawStore. It satisfies fetch.Fetcher. The zero value is not usable;
// construct one with New.
type Fetcher struct {
	client  *http.Client
	baseURL string
	limiter *rate.Limiter
	now     func() time.Time
}

// Option customizes a Fetcher. All upstream-facing dependencies (HTTP client,
// base URL, rate limiter, clock) are injectable so the Fetcher is testable
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

// WithBaseURL overrides the EPSS daily-scores base URL, e.g. to target a local
// test server. An empty URL is ignored.
func WithBaseURL(url string) Option {
	return func(f *Fetcher) {
		if url != "" {
			f.baseURL = url
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

// WithClock overrides the clock used to pick the latest scored date and stamp
// FetchResult.FetchedAt, for deterministic tests. A nil clock is ignored.
func WithClock(now func() time.Time) Option {
	return func(f *Fetcher) {
		if now != nil {
			f.now = now
		}
	}
}

// New constructs an EPSS Fetcher with sane defaults, applying any options. By
// default it uses http.DefaultClient, the public base URL, a conservative rate
// limiter, and the wall clock.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		client:  http.DefaultClient,
		baseURL: DefaultBaseURL,
		limiter: rate.NewLimiter(defaultRateLimit, defaultBurst),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Source returns the stable source key "epss".
func (f *Fetcher) Source() string { return source }

// Fetch retrieves EPSS daily score files and streams their unmodified bytes to
// rawStore. The scored date (YYYY-MM-DD) is the incremental cursor:
//
//   - An empty cursor performs a full fetch of the latest available day and
//     returns that date as the next cursor (Requirement 1.4).
//   - A non-empty cursor fetches each day strictly after that date up to the
//     latest available day, and returns the newest fetched date as the next
//     cursor. If no newer day exists, nothing is stored and the same cursor is
//     returned (a cheap no-op) (Requirement 1.3).
//
// Fetch never parses or mutates a day's CSV contents: it streams the exact
// original bytes to the raw store (Requirement 1.6).
func (f *Fetcher) Fetch(ctx context.Context, rawStore raw.RawStore, cur fetch.Cursor) (fetch.FetchResult, error) {
	if rawStore == nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("epss: rawStore must not be nil")
	}

	latest := f.now().UTC().Truncate(24 * time.Hour)

	// Determine which scored dates to fetch, oldest first.
	dates, err := f.datesToFetch(cur, latest)
	if err != nil {
		return fetch.FetchResult{Source: source}, err
	}

	result := fetch.FetchResult{
		Source:    source,
		Next:      cur,
		FetchedAt: f.now(),
	}

	// No newer day than the cursor: nothing to do (no-op).
	if len(dates) == 0 {
		return result, nil
	}

	refs := make([]raw.RawRef, 0, len(dates))
	var newest time.Time
	for _, d := range dates {
		// Rate-limit each upstream request (Requirement 1.5).
		if f.limiter != nil {
			if err := f.limiter.Wait(ctx); err != nil {
				return fetch.FetchResult{Source: source}, fmt.Errorf("epss: rate limiter: %w", err)
			}
		}

		payload, err := f.get(ctx, f.fileURL(d))
		if err != nil {
			return fetch.FetchResult{Source: source}, err
		}

		// Stream the exact, unmodified bytes to the raw store (Requirement 1.6).
		ref, err := rawStore.Put(ctx, source, payload)
		if err != nil {
			return fetch.FetchResult{Source: source}, fmt.Errorf("epss: store raw payload: %w", err)
		}
		refs = append(refs, ref)
		newest = d
	}

	result.RawRefs = refs
	result.Next = fetch.Cursor(newest.Format(dateLayout))
	return result, nil
}

// datesToFetch computes the ordered (oldest-first) list of scored dates to
// retrieve for the given cursor and latest available day.
//
//   - Empty cursor: just the latest day (full fetch of current state).
//   - Non-empty cursor: every day strictly after the cursor date, up to and
//     including latest, capped at maxBackfillDays most recent days so one run
//     cannot issue an unbounded number of requests. An invalid cursor date is
//     reported rather than silently reset.
func (f *Fetcher) datesToFetch(cur fetch.Cursor, latest time.Time) ([]time.Time, error) {
	if cur == "" {
		return []time.Time{latest}, nil
	}

	since, err := time.Parse(dateLayout, strings.TrimSpace(string(cur)))
	if err != nil {
		return nil, fmt.Errorf("epss: invalid cursor %q (want YYYY-MM-DD): %w", string(cur), err)
	}
	since = since.UTC().Truncate(24 * time.Hour)

	// Collect days strictly after the cursor, up to latest.
	var dates []time.Time
	for d := since.AddDate(0, 0, 1); !d.After(latest); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d)
	}

	// Cap to the most recent maxBackfillDays so a very old cursor cannot
	// trigger an unbounded backfill in a single run.
	if len(dates) > maxBackfillDays {
		dates = dates[len(dates)-maxBackfillDays:]
	}
	return dates, nil
}

// fileURL builds the daily-file URL for a scored date, joining the base URL
// with the date-stamped file name.
func (f *Fetcher) fileURL(d time.Time) string {
	base := strings.TrimSuffix(f.baseURL, "/")
	return fmt.Sprintf("%s/epss_scores-%s.csv.gz", base, d.Format(dateLayout))
}

// get performs a single rate-limited GET and returns the response body bytes
// unmodified (still gzip-compressed; decompression and CSV decoding are the
// parser's job). It bounds the read so an unexpectedly large or hostile
// upstream response cannot exhaust memory.
func (f *Fetcher) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("epss: build request: %w", err)
	}
	req.Header.Set("Accept", "application/gzip")
	// Preserve the gzip bytes exactly as served; do not let the transport
	// transparently decompress, so the stored payload is the unmodified file.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("epss: fetch daily file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epss: unexpected status %d fetching %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes))
	if err != nil {
		return nil, fmt.Errorf("epss: read daily file body: %w", err)
	}
	return body, nil
}

// Ensure Fetcher satisfies fetch.Fetcher.
var _ fetch.Fetcher = (*Fetcher)(nil)
