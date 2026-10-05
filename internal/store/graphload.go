package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// GraphEntity is a normalized entity enriched with the evidence ids and source
// list needed by the graph writer.
type GraphEntity struct {
	CanonicalID string
	Kind        model.Kind
	Provider    string
	Name        string
	Attributes  map[string]any
	EvidenceIDs []string
	Sources     []string
}

// GraphEdge is a normalized edge enriched with evidence ids and source list.
type GraphEdge struct {
	ID          int64
	SrcID       string
	Relation    model.Relation
	DstID       string
	Attributes  map[string]any
	Confidence  float64
	EvidenceIDs []string
	Sources     []string
}

// LoadGraphEntities returns all non-stale entities with their evidence ids and
// distinct sources, for syncing to Memgraph.
func (s *Store) LoadGraphEntities(ctx context.Context) ([]GraphEntity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.canonical_id, e.kind, e.provider, e.name, e.attributes,
		       COALESCE(array_agg(DISTINCT ev.raw_event_id) FILTER (WHERE ev.raw_event_id IS NOT NULL), '{}'),
		       COALESCE(array_agg(DISTINCT ev.source) FILTER (WHERE ev.source IS NOT NULL), '{}')
		FROM normalized_entities e
		LEFT JOIN evidence ev ON ev.element_kind='entity' AND ev.element_id = e.canonical_id
		GROUP BY e.canonical_id, e.kind, e.provider, e.name, e.attributes`)
	if err != nil {
		return nil, fmt.Errorf("store: load graph entities: %w", err)
	}
	defer rows.Close()

	var out []GraphEntity
	for rows.Next() {
		var ge GraphEntity
		var kind string
		var attrs []byte
		if err := rows.Scan(&ge.CanonicalID, &kind, &ge.Provider, &ge.Name, &attrs, &ge.EvidenceIDs, &ge.Sources); err != nil {
			return nil, err
		}
		ge.Kind = model.Kind(kind)
		if len(attrs) > 0 {
			_ = json.Unmarshal(attrs, &ge.Attributes)
		}
		out = append(out, ge)
	}
	return out, rows.Err()
}

// LoadGraphEdges returns all non-stale edges with evidence ids and sources.
func (s *Store) LoadGraphEdges(ctx context.Context) ([]GraphEdge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.src_id, e.relation, e.dst_id, e.attributes, e.confidence,
		       COALESCE(array_agg(DISTINCT ev.raw_event_id) FILTER (WHERE ev.raw_event_id IS NOT NULL), '{}'),
		       COALESCE(array_agg(DISTINCT ev.source) FILTER (WHERE ev.source IS NOT NULL), '{}')
		FROM normalized_edges e
		LEFT JOIN evidence ev ON ev.element_kind='edge' AND ev.element_id = e.id::text
		GROUP BY e.id, e.src_id, e.relation, e.dst_id, e.attributes, e.confidence`)
	if err != nil {
		return nil, fmt.Errorf("store: load graph edges: %w", err)
	}
	defer rows.Close()

	var out []GraphEdge
	for rows.Next() {
		var ge GraphEdge
		var rel string
		var attrs []byte
		if err := rows.Scan(&ge.ID, &ge.SrcID, &rel, &ge.DstID, &attrs, &ge.Confidence, &ge.EvidenceIDs, &ge.Sources); err != nil {
			return nil, err
		}
		ge.Relation = model.Relation(rel)
		if len(attrs) > 0 {
			_ = json.Unmarshal(attrs, &ge.Attributes)
		}
		out = append(out, ge)
	}
	return out, rows.Err()
}

// MarkStaleEntities sets stale=true on entities whose last_seen predates the
// cutoff for a given source. Nothing is deleted. (Used by Task 5 stale demo and
// re-sync lifecycle.)
func (s *Store) MarkStaleEntitiesBySource(ctx context.Context, source, latestRunID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE normalized_entities ne SET stale = true
		WHERE EXISTS (
			SELECT 1 FROM evidence ev
			WHERE ev.element_kind='entity' AND ev.element_id = ne.canonical_id AND ev.source = $1
		)
		AND NOT EXISTS (
			SELECT 1 FROM evidence ev2
			WHERE ev2.element_kind='entity' AND ev2.element_id = ne.canonical_id
			  AND ev2.source = $1 AND ev2.run_id = $2
		)`, source, latestRunID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
