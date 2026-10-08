// Package fetch defines the Fetcher abstraction that pulls raw intelligence
// payloads from one upstream source and streams them, unmodified, into the
// content-addressed raw store.
//
// Phase 1 implements OSV (purl ecosystems, PyPI first), CISA KEV, and FIRST
// EPSS. NVD, GHSA, ATLAS, OWASP GenAI, AVID, MCP, and vendor advisories are
// exposed as interface stubs reserved for later phases (see stubs.go).
package fetch

import (
	"context"
	"time"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/raw"
)

// Fetcher pulls raw payloads from one upstream intelligence source.
type Fetcher interface {
	// Source returns a stable source key, e.g. "osv", "kev", "epss".
	Source() string
	// Fetch retrieves payloads since the given cursor. It streams raw bytes to
	// the RawStore and returns a result describing what was fetched plus the
	// next cursor for incremental runs. Fetch MUST NOT parse or mutate
	// payloads.
	//
	// A non-empty cursor retrieves only payloads changed since that cursor and
	// returns the next cursor; an empty cursor performs a full fetch.
	Fetch(ctx context.Context, rawStore raw.RawStore, cur Cursor) (FetchResult, error)
}

// Cursor is an opaque incremental marker (e.g. an OSV page token, an EPSS
// date, a KEV catalogVersion). An empty cursor means a full fetch.
type Cursor string

// FetchResult describes a single raw fetch: the content-addressed references
// just stored, the next cursor for incremental runs, and when the fetch
// completed. The fuller per-run accounting (records added/updated/deleted,
// cursor before/after, status/error) is recorded into intel_fetch_runs by the
// pipeline and is intentionally not part of this interface.
type FetchResult struct {
	Source    string
	RawRefs   []raw.RawRef // content-addressed references just stored
	Next      Cursor
	FetchedAt time.Time
}
