// Package source defines the Source collector contract and a registry of
// named source factories.
package source

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// Run carries the identifying context of a single collection run into a
// source so it can stamp emitted events.
type Run struct {
	RunID string
	// Reporter optionally receives per-table progress signals as collection
	// proceeds. It is nil-safe: a nil Reporter means "no progress reporting"
	// and preserves the collector's behavior exactly. Reporters must be safe
	// for concurrent use is NOT required — the collector calls them from a
	// single goroutine in table order.
	Reporter ProgressReporter
}

// ProgressReporter receives per-table progress signals from a Source during a
// collection run. Implementations surface this as run-level progress (counts,
// current table, per-table state). The collector only emits counts and states
// it already computes; a reporter adds no new provider API calls.
//
// A nil ProgressReporter is never called — callers must guard with a nil check
// (see report* helpers in the steampipe source), so every existing call site
// that leaves Run.Reporter nil keeps working unchanged.
type ProgressReporter interface {
	// TablesPlanned reports the total number of tables that will be collected
	// for this run, after any gating. Called once before the first table.
	TablesPlanned(total int)
	// TableStarted reports that collection of a table has begun. index is the
	// zero-based position in the planned order.
	TableStarted(table string, index int)
	// TableDone reports that a table finished successfully with rows rows.
	TableDone(table string, rows int)
	// TableError reports that a table finished with an error. Collection
	// continues with the next table (error isolation).
	TableError(table string, err error)
}

// Source is a collector. Collect streams RawEvents on the returned channel and
// surfaces non-fatal errors on the error channel. Both channels are closed
// when collection finishes. The implementation must stop promptly when ctx is
// cancelled.
//
// Collect is responsible for setting RunID on each event and calling
// Finalize so the event is content-addressed and validatable. Collect should
// not call Validate itself; the pipeline validates.
type Source interface {
	Name() string
	Collect(ctx context.Context, run Run) (<-chan rawevent.RawEvent, <-chan error)
}

// Factory builds a configured Source from an opaque config map. Factories are
// registered by name and resolved by the CLI.
type Factory func(cfg map[string]any) (Source, error)

// Registry is a concurrency-safe map of source name to factory.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register adds a factory under name. It panics on duplicate registration,
// which only happens at init time and indicates a programming error.
func (r *Registry) Register(name string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[name]; ok {
		panic(fmt.Sprintf("source: duplicate registration for %q", name))
	}
	r.factories[name] = f
}

// Build resolves name and constructs the source with cfg.
func (r *Registry) Build(name string, cfg map[string]any) (Source, error) {
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown source %q (known: %v)", name, r.Names())
	}
	return f(cfg)
}

// Names returns the sorted list of registered source names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
