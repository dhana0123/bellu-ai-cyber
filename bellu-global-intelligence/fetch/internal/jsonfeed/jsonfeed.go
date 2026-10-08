// Package jsonfeed provides a small shared helper for AI-source fetchers that
// pull a single document from a configurable URL and stream its unmodified
// bytes into the content-addressed raw store.
//
// Several Phase-3 AI intelligence sources (OWASP GenAI, AVID, MCP) have no
// native per-record cursor and are published as a single JSON document, so they
// share one conditional-fetch shape: a non-empty cursor becomes a conditional
// request (If-None-Match on the opaque validator) and an unchanged document
// (304 Not Modified) is a cheap no-op that stores nothing. This package factors
// that shape out so each source package stays a thin, well-documented wrapper
// rather than copying the same request/stream/cursor logic.
//
// It never parses or mutates the payload (Requirement 1.6): the body is read as
// bounded, untrusted bytes and handed to the RawStore as-is.
package jsonfeed

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

// Fetch performs a single rate-limited, optionally conditional GET of url and
// streams the response body (bounded by maxBytes) into rawStore under the given
// source key. It implements the standard AI-feed cursor semantics documented on
// the package: the HTTP ETag (falling back to Last-Modified) is the incremental
// cursor; a 304 Not Modified stores nothing and returns the incoming cursor.
//
// now stamps FetchResult.FetchedAt (injectable for deterministic tests). A nil
// client defaults to http.DefaultClient; a nil limiter skips rate limiting.
func Fetch(
	ctx context.Context,
	source string,
	client *http.Client,
	url string,
	limiter *rate.Limiter,
	now func() time.Time,
	maxBytes int64,
	rawStore raw.RawStore,
	cur fetch.Cursor,
) (fetch.FetchResult, error) {
	if rawStore == nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("%s: rawStore must not be nil", source)
	}
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}

	// Rate-limit the upstream request (Requirement 1.5).
	if limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			return fetch.FetchResult{Source: source}, fmt.Errorf("%s: rate limiter: %w", source, err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("%s: build request: %w", source, err)
	}
	req.Header.Set("Accept", "application/json")
	// A non-empty cursor becomes a conditional request so an unchanged document
	// is a cheap no-op (Requirement 1.3).
	if cur != "" {
		req.Header.Set("If-None-Match", string(cur))
	}

	resp, err := client.Do(req)
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("%s: fetch feed: %w", source, err)
	}
	defer resp.Body.Close()

	result := fetch.FetchResult{Source: source, FetchedAt: now(), Next: cur}

	switch resp.StatusCode {
	case http.StatusNotModified:
		return result, nil
	case http.StatusOK:
		// proceed
	default:
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return fetch.FetchResult{Source: source}, fmt.Errorf("%s: unexpected status %d fetching feed", source, resp.StatusCode)
	}

	// Read the feed as opaque, untrusted bytes (Requirement 1.6).
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("%s: read feed body: %w", source, err)
	}

	ref, err := rawStore.Put(ctx, source, payload)
	if err != nil {
		return fetch.FetchResult{Source: source}, fmt.Errorf("%s: store raw payload: %w", source, err)
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
