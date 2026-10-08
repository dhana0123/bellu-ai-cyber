package osv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/fetch"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// roundTripFunc adapts a function to an http.RoundTripper so tests can serve
// responses entirely in-memory with no live network access.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

// memStore is a minimal in-memory content-addressed RawStore for tests. It
// mirrors the content-addressed, dedup-on-identical-bytes contract of the real
// store without touching the filesystem.
type memStore struct {
	mu    sync.Mutex
	items map[string][]byte
	puts  int // total Put calls, including no-op dedup hits
}

func newMemStore() *memStore { return &memStore{items: map[string][]byte{}} }

func (m *memStore) Put(_ context.Context, source string, payload []byte) (raw.RawRef, error) {
	sum := sha256.Sum256(payload)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts++
	if _, ok := m.items[hash]; !ok {
		cp := append([]byte(nil), payload...)
		m.items[hash] = cp
	}
	return raw.RawRef{Source: source, Hash: hash, Size: int64(len(payload))}, nil
}

func (m *memStore) Get(_ context.Context, ref raw.RawRef) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.items[ref.Hash]
	if !ok {
		return nil, fmt.Errorf("not found: %s", ref.Hash)
	}
	return append([]byte(nil), b...), nil
}

func okResponse(body []byte, lastModified string) *http.Response {
	h := http.Header{}
	if lastModified != "" {
		h.Set("Last-Modified", lastModified)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func fastFetcher(client *http.Client) *Fetcher {
	return &Fetcher{
		HTTPClient: client,
		// A permissive limiter keeps the test fast while still exercising the
		// rate-limiting code path.
		Limiter: rate.NewLimiter(rate.Inf, 1),
	}
}

func TestSourceKey(t *testing.T) {
	if got := New().Source(); got != "osv" {
		t.Fatalf("Source() = %q, want %q", got, "osv")
	}
}

// TestFullFetchStoresRawBytes verifies an empty cursor performs a full fetch
// (Req 1.4), hits the PyPI ecosystem first (Req 1.2), and streams the exact
// upstream bytes into the raw store unmodified (Req 1.6).
func TestFullFetchStoresRawBytes(t *testing.T) {
	payload := []byte("PK\x03\x04 fake-osv-zip-bytes \x00\x01\x02")
	var gotURL string
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		if r.Header.Get("If-Modified-Since") != "" {
			t.Errorf("empty cursor must not send If-Modified-Since, got %q", r.Header.Get("If-Modified-Since"))
		}
		return okResponse(payload, "Wed, 01 Jan 2025 00:00:00 GMT"), nil
	})

	store := newMemStore()
	f := fastFetcher(client)

	res, err := f.Fetch(context.Background(), store, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	wantURL := DefaultBaseURL + "/PyPI/all.zip"
	if gotURL != wantURL {
		t.Fatalf("fetched URL = %q, want %q (PyPI first)", gotURL, wantURL)
	}
	if len(res.RawRefs) != 1 {
		t.Fatalf("RawRefs len = %d, want 1", len(res.RawRefs))
	}
	if res.Source != "osv" {
		t.Fatalf("result source = %q, want osv", res.Source)
	}
	if res.Next != "Wed, 01 Jan 2025 00:00:00 GMT" {
		t.Fatalf("Next cursor = %q, want the Last-Modified value", res.Next)
	}

	// Stored bytes must be byte-identical to the upstream payload (no mutation).
	stored, err := store.Get(context.Background(), res.RawRefs[0])
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(stored, payload) {
		t.Fatalf("stored bytes differ from upstream payload")
	}
}

// TestIncrementalCursorUnchanged verifies a non-empty cursor issues a
// conditional request and a 304 response is a no-op that returns the cursor
// unchanged (Req 1.3).
func TestIncrementalCursorUnchanged(t *testing.T) {
	const cursor = "Wed, 01 Jan 2025 00:00:00 GMT"
	var sawConditional bool
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("If-Modified-Since") == cursor {
			sawConditional = true
		}
		return &http.Response{
			StatusCode: http.StatusNotModified,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	})

	store := newMemStore()
	f := fastFetcher(client)

	res, err := f.Fetch(context.Background(), store, fetch.Cursor(cursor))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !sawConditional {
		t.Fatalf("non-empty cursor must send If-Modified-Since")
	}
	if len(res.RawRefs) != 0 {
		t.Fatalf("unchanged fetch stored %d refs, want 0", len(res.RawRefs))
	}
	if res.Next != fetch.Cursor(cursor) {
		t.Fatalf("Next = %q, want unchanged cursor %q", res.Next, cursor)
	}
	if store.puts != 0 {
		t.Fatalf("304 response must not Put anything, got %d puts", store.puts)
	}
}

// TestIncrementalCursorChanged verifies a changed archive is fetched and the
// cursor advances to the new Last-Modified value.
func TestIncrementalCursorChanged(t *testing.T) {
	const oldCursor = "Wed, 01 Jan 2025 00:00:00 GMT"
	const newLM = "Thu, 02 Jan 2025 00:00:00 GMT"
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		return okResponse([]byte("changed-bytes"), newLM), nil
	})

	store := newMemStore()
	f := fastFetcher(client)

	res, err := f.Fetch(context.Background(), store, fetch.Cursor(oldCursor))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.RawRefs) != 1 {
		t.Fatalf("RawRefs len = %d, want 1", len(res.RawRefs))
	}
	if res.Next != fetch.Cursor(newLM) {
		t.Fatalf("Next = %q, want advanced cursor %q", res.Next, newLM)
	}
}

// TestConfigurableEcosystemsOrdered verifies configured ecosystems are fetched
// in order and each archive URL is built correctly.
func TestConfigurableEcosystemsOrdered(t *testing.T) {
	var urls []string
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		return okResponse([]byte("data:"+r.URL.Path), ""), nil
	})

	f := &Fetcher{
		BaseURL:    "https://example.test/osv",
		Ecosystems: []string{"PyPI", "npm", "Go"},
		HTTPClient: client,
		Limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	res, err := f.Fetch(context.Background(), newMemStore(), "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := []string{
		"https://example.test/osv/PyPI/all.zip",
		"https://example.test/osv/npm/all.zip",
		"https://example.test/osv/Go/all.zip",
	}
	if len(urls) != len(want) {
		t.Fatalf("fetched %d urls, want %d: %v", len(urls), len(want), urls)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("url[%d] = %q, want %q", i, urls[i], want[i])
		}
	}
	if len(res.RawRefs) != 3 {
		t.Fatalf("RawRefs len = %d, want 3", len(res.RawRefs))
	}
}

// TestUnexpectedStatusErrors verifies a non-200/304 status is surfaced as an
// error and stores nothing.
func TestUnexpectedStatusErrors(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader([]byte("boom"))),
		}, nil
	})
	store := newMemStore()
	f := fastFetcher(client)

	if _, err := f.Fetch(context.Background(), store, ""); err == nil {
		t.Fatalf("expected error on 500 status")
	}
	if store.puts != 0 {
		t.Fatalf("failed fetch must not Put anything, got %d", store.puts)
	}
}

func TestNilRawStoreErrors(t *testing.T) {
	f := fastFetcher(newTestClient(func(*http.Request) (*http.Response, error) {
		t.Fatalf("must not issue a request when raw store is nil")
		return nil, nil
	}))
	if _, err := f.Fetch(context.Background(), nil, ""); err == nil {
		t.Fatalf("expected error for nil raw store")
	}
}

// TestFetchStreamsBytesUnmodified is a property-based test (Req 1.6): for any
// payload bytes, the fetcher stores exactly those bytes, unmodified, and the
// returned ref round-trips to the same content.
func TestFetchStreamsBytesUnmodified(t *testing.T) {
	prop := func(payload []byte) bool {
		client := newTestClient(func(*http.Request) (*http.Response, error) {
			// Serve a fresh reader each call over an independent copy.
			return okResponse(append([]byte(nil), payload...), "Wed, 01 Jan 2025 00:00:00 GMT"), nil
		})
		store := newMemStore()
		f := fastFetcher(client)

		res, err := f.Fetch(context.Background(), store, "")
		if err != nil || len(res.RawRefs) != 1 {
			return false
		}
		got, err := store.Get(context.Background(), res.RawRefs[0])
		if err != nil {
			return false
		}
		return bytes.Equal(got, payload)
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatalf("payload round-trip property failed: %v", err)
	}
}

// TestRateLimitingApplied verifies the fetcher waits on its limiter before
// each upstream request (Req 1.5): with a 0-burst, finite-rate limiter and a
// short-deadline context, Wait fails and no request is issued.
func TestRateLimitingApplied(t *testing.T) {
	var issued bool
	client := newTestClient(func(*http.Request) (*http.Response, error) {
		issued = true
		return okResponse([]byte("x"), ""), nil
	})
	f := &Fetcher{
		HTTPClient: client,
		// Rate of 1/hour with burst 1: the first token is available, but we
		// cancel the context so Wait returns before issuing the request.
		Limiter: rate.NewLimiter(rate.Every(time.Hour), 0),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // force limiter.Wait to fail immediately

	if _, err := f.Fetch(ctx, newMemStore(), ""); err == nil {
		t.Fatalf("expected error when limiter cannot grant a token")
	}
	if issued {
		t.Fatalf("request issued despite rate limiter blocking")
	}
}
