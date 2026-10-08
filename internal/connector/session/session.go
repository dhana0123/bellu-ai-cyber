// Package session implements the Connector's Credential Session Manager. It
// caches short-lived provider credentials in process memory for the lifetime of
// a session and discards them on expiry, refresh, revoke, and session end.
// Credentials are never written to the database, logs, or API responses.
package session

import (
	"context"
	"sync"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// RefreshFunc obtains fresh credentials for a connection, typically by calling
// the provider's STS AssumeRole path.
type RefreshFunc func(context.Context) (*provider.Credentials, error)

// Manager caches credentials keyed by connection id, in memory only.
type Manager struct {
	mu    sync.Mutex
	cache map[int64]*provider.Credentials
	now   func() time.Time
}

// New returns a Manager using the real clock.
func New() *Manager {
	return &Manager{cache: map[int64]*provider.Credentials{}, now: time.Now}
}

// NewWithClock returns a Manager using the given clock, for deterministic tests.
// A nil clock falls back to time.Now.
func NewWithClock(now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{cache: map[int64]*provider.Credentials{}, now: now}
}

// Obtain returns cached, unexpired credentials for connID. If none are cached
// or the cached credentials have expired, it discards any stale entry, calls
// refresh to obtain fresh credentials, caches them, and returns them.
func (m *Manager) Obtain(ctx context.Context, connID int64, refresh RefreshFunc) (*provider.Credentials, error) {
	m.mu.Lock()
	if c, ok := m.cache[connID]; ok && !c.Expired(m.now()) {
		m.mu.Unlock()
		return c, nil
	}
	// Drop any stale entry before refreshing.
	delete(m.cache, connID)
	m.mu.Unlock()

	c, err := refresh(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[connID] = c
	m.mu.Unlock()
	return c, nil
}

// Get returns the cached credentials for connID if present and unexpired.
func (m *Manager) Get(connID int64) (*provider.Credentials, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cache[connID]
	if !ok || c.Expired(m.now()) {
		return nil, false
	}
	return c, true
}

// Refresh unconditionally obtains fresh credentials and replaces any cached
// entry for connID.
func (m *Manager) Refresh(ctx context.Context, connID int64, refresh RefreshFunc) (*provider.Credentials, error) {
	c, err := refresh(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[connID] = c
	m.mu.Unlock()
	return c, nil
}

// Revoke discards all cached credentials for connID. It is called on explicit
// revocation and at the end of a discovery session.
func (m *Manager) Revoke(connID int64) {
	m.mu.Lock()
	delete(m.cache, connID)
	m.mu.Unlock()
}
