// Package graphquery reads the Bellu graph for the Test_Runner's comparator. It
// queries the control-panel HTTP API first and falls back to the customer
// Postgres database only when the API is unreachable (Requirement 10.3). It
// never queries Memgraph directly and never touches the Global Intelligence
// plane (Requirement 18.2).
package graphquery

import (
	"context"
)

// Node is a graph node as the comparator needs it: its Kind and human name.
type Node struct {
	CanonicalID string
	Kind        string
	Name        string
}

// Edge is a graph edge: its relation, endpoint canonical ids, and confidence.
type Edge struct {
	SrcID      string
	Relation   string
	DstID      string
	Confidence float64
}

// Client is the read surface the comparator consumes. Implementations are the
// API client (preferred) and the Postgres fallback reader.
type Client interface {
	// Healthy reports whether this backend is reachable.
	Healthy(ctx context.Context) bool
	// Nodes returns all graph nodes (optionally filtered by kind; empty = all).
	Nodes(ctx context.Context, kind string) ([]Node, error)
	// Edges returns all graph edges (optionally filtered by relation; empty = all).
	Edges(ctx context.Context, relation string) ([]Edge, error)
}

// Selecting returns the first healthy client from the given preference order
// (API first, Postgres fallback). It returns nil when none are reachable.
func Selecting(ctx context.Context, clients ...Client) Client {
	for _, c := range clients {
		if c != nil && c.Healthy(ctx) {
			return c
		}
	}
	return nil
}
