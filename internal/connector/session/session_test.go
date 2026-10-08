package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

func creds(expiry time.Time) *provider.Credentials {
	return &provider.Credentials{
		AccessKeyID:     "AKIA_TEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Expiry:          expiry,
	}
}

// TestObtainCachesUnexpired asserts Obtain returns cached unexpired credentials
// without calling refresh again.
func TestObtainCachesUnexpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewWithClock(func() time.Time { return now })

	calls := 0
	refresh := func(context.Context) (*provider.Credentials, error) {
		calls++
		return creds(now.Add(time.Hour)), nil
	}

	if _, err := m.Obtain(context.Background(), 1, refresh); err != nil {
		t.Fatalf("first obtain: %v", err)
	}
	if _, err := m.Obtain(context.Background(), 1, refresh); err != nil {
		t.Fatalf("second obtain: %v", err)
	}
	if calls != 1 {
		t.Errorf("refresh called %d times, want 1 (second should hit cache)", calls)
	}
}

// TestObtainRefreshesExpired asserts expired cached credentials are discarded
// and refreshed.
func TestObtainRefreshesExpired(t *testing.T) {
	cur := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewWithClock(func() time.Time { return cur })

	calls := 0
	refresh := func(context.Context) (*provider.Credentials, error) {
		calls++
		return creds(cur.Add(time.Minute)), nil
	}

	if _, err := m.Obtain(context.Background(), 1, refresh); err != nil {
		t.Fatalf("obtain: %v", err)
	}
	// Advance past expiry.
	cur = cur.Add(2 * time.Minute)
	if _, err := m.Obtain(context.Background(), 1, refresh); err != nil {
		t.Fatalf("obtain after expiry: %v", err)
	}
	if calls != 2 {
		t.Errorf("refresh called %d times, want 2 (expired should re-obtain)", calls)
	}
}

// TestGetRespectsExpiry asserts Get returns nothing for an expired entry.
func TestGetRespectsExpiry(t *testing.T) {
	cur := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewWithClock(func() time.Time { return cur })
	refresh := func(context.Context) (*provider.Credentials, error) { return creds(cur.Add(time.Minute)), nil }
	if _, err := m.Obtain(context.Background(), 7, refresh); err != nil {
		t.Fatalf("obtain: %v", err)
	}
	if _, ok := m.Get(7); !ok {
		t.Error("Get should find unexpired entry")
	}
	cur = cur.Add(2 * time.Minute)
	if _, ok := m.Get(7); ok {
		t.Error("Get should not return expired entry")
	}
}

// TestRefreshReplaces asserts Refresh replaces the cached entry.
func TestRefreshReplaces(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewWithClock(func() time.Time { return now })

	first := creds(now.Add(time.Hour))
	first.AccessKeyID = "FIRST"
	second := creds(now.Add(time.Hour))
	second.AccessKeyID = "SECOND"

	seq := []*provider.Credentials{first, second}
	i := 0
	refresh := func(context.Context) (*provider.Credentials, error) {
		c := seq[i]
		i++
		return c, nil
	}

	if _, err := m.Obtain(context.Background(), 1, refresh); err != nil {
		t.Fatalf("obtain: %v", err)
	}
	if _, err := m.Refresh(context.Background(), 1, refresh); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, ok := m.Get(1)
	if !ok || got.AccessKeyID != "SECOND" {
		t.Errorf("after refresh got %+v ok=%v, want AccessKeyID SECOND", got, ok)
	}
}

// TestRevokeClears asserts Revoke discards cached credentials.
func TestRevokeClears(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewWithClock(func() time.Time { return now })
	refresh := func(context.Context) (*provider.Credentials, error) { return creds(now.Add(time.Hour)), nil }
	if _, err := m.Obtain(context.Background(), 1, refresh); err != nil {
		t.Fatalf("obtain: %v", err)
	}
	m.Revoke(1)
	if _, ok := m.Get(1); ok {
		t.Error("Get should return nothing after Revoke")
	}
}

// TestObtainPropagatesRefreshError asserts a refresh error is returned and
// nothing is cached.
func TestObtainPropagatesRefreshError(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewWithClock(func() time.Time { return now })
	wantErr := errors.New("sts failed")
	refresh := func(context.Context) (*provider.Credentials, error) { return nil, wantErr }
	if _, err := m.Obtain(context.Background(), 1, refresh); !errors.Is(err, wantErr) {
		t.Errorf("want refresh error, got %v", err)
	}
	if _, ok := m.Get(1); ok {
		t.Error("nothing should be cached after a failed refresh")
	}
}
