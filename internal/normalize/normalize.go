// Package normalize turns RawEvents into canonical model.Entity and model.Edge
// values. Each source family has a Normalizer; a registry dispatches events to
// the right one.
package normalize

import (
	"fmt"
	"sort"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// Result is the output of normalizing a batch of events.
type Result struct {
	Entities []model.Entity
	Edges    []model.Edge
}

// Add appends entities and edges from another result.
func (r *Result) Add(other Result) {
	r.Entities = append(r.Entities, other.Entities...)
	r.Edges = append(r.Edges, other.Edges...)
}

// Normalizer maps a slice of RawEvents (already filtered to this normalizer's
// source/provider) into canonical entities and edges.
//
// Normalizers see a batch because some mappings (dependencies, vuln->package)
// need cross-event context within a run.
type Normalizer interface {
	// Sources returns the rawevent.Source values this normalizer handles.
	Sources() []string
	Normalize(events []rawevent.RawEvent) (Result, error)
}

// Registry dispatches events to normalizers by their Source field. Multiple
// normalizers may claim the same source (e.g. AWS, Azure and GCP all consume
// "steampipe"); each filters internally by provider.
type Registry struct {
	bySource map[string][]Normalizer
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{bySource: make(map[string][]Normalizer)}
}

// Register adds a normalizer for each of its declared sources.
func (r *Registry) Register(n Normalizer) {
	for _, s := range n.Sources() {
		r.bySource[s] = append(r.bySource[s], n)
	}
}

// Normalize groups events by source, dispatches each group to every normalizer
// registered for that source, and merges the results. Events from unknown
// sources are skipped.
func (r *Registry) Normalize(events []rawevent.RawEvent) (Result, error) {
	groups := make(map[string][]rawevent.RawEvent)
	for _, e := range events {
		groups[e.Source] = append(groups[e.Source], e)
	}

	// Deterministic order for stable output.
	srcs := make([]string, 0, len(groups))
	for s := range groups {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)

	var out Result
	for _, s := range srcs {
		for _, n := range r.bySource[s] {
			res, err := n.Normalize(groups[s])
			if err != nil {
				return out, fmt.Errorf("normalize %s: %w", s, err)
			}
			out.Add(res)
		}
	}
	return out, nil
}

// EvidenceFrom builds a one-element evidence slice from a raw event.
func EvidenceFrom(e rawevent.RawEvent) []model.Evidence {
	return []model.Evidence{{
		RawEventID: e.ID,
		Source:     e.Source,
		Collector:  e.Collector,
		ObservedAt: e.ObservedAt,
		RunID:      e.RunID,
	}}
}
