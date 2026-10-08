package atlas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"golang.org/x/time/rate"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/fetch"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// memStore is a tiny in-memory RawStore for offline fetcher tests.
type memStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemStore() *memStore { return &memStore{data: map[string][]byte{}} }

func (m *memStore) Put(_ context.Context, source string, payload []byte) (raw.RawRef, error) {
	sum := sha256.Sum256(payload)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	m.mu.Lock()
	m.data[hash] = append([]byte(nil), payload...)
	m.mu.Unlock()
	return raw.RawRef{Source: source, Hash: hash, Size: int64(len(payload))}, nil
}

func (m *memStore) Get(_ context.Context, ref raw.RawRef) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[ref.Hash]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return b, nil
}

// TestFetchFullThenConditional confirms a full fetch stores the document and
// returns an ETag cursor, and a conditional re-fetch with that cursor that the
// server reports unchanged (304) stores nothing.
func TestFetchFullThenConditional(t *testing.T) {
	const etag = `"abc123"`
	body := []byte("techniques:\n  - id: AML.T0051\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	store := newMemStore()
	f := New(
		WithHTTPClient(srv.Client()),
		WithDocumentURL(srv.URL),
		WithRateLimiter(rate.NewLimiter(rate.Inf, 1)),
	)

	// Full fetch (empty cursor).
	res, err := f.Fetch(context.Background(), store, "")
	if err != nil {
		t.Fatalf("full fetch: %v", err)
	}
	if len(res.RawRefs) != 1 {
		t.Fatalf("expected 1 stored ref, got %d", len(res.RawRefs))
	}
	if res.Next != fetch.Cursor(etag) {
		t.Fatalf("expected ETag cursor %q, got %q", etag, res.Next)
	}

	// Conditional fetch with the ETag: server returns 304, nothing stored.
	res2, err := f.Fetch(context.Background(), store, res.Next)
	if err != nil {
		t.Fatalf("conditional fetch: %v", err)
	}
	if len(res2.RawRefs) != 0 {
		t.Fatalf("expected no stored refs on 304, got %d", len(res2.RawRefs))
	}
	if res2.Next != res.Next {
		t.Fatalf("expected cursor unchanged on 304, got %q", res2.Next)
	}
}

// TestFetchNilStore confirms a nil RawStore is rejected.
func TestFetchNilStore(t *testing.T) {
	if _, err := New().Fetch(context.Background(), nil, ""); err == nil {
		t.Fatal("expected error for nil raw store")
	}
}
