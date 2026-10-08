package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EdgeDetail describes one normalized edge and its backing evidence.
type EdgeDetail struct {
	ID         int64
	SrcID      string
	Relation   string
	DstID      string
	Confidence float64
	Evidence   []EvidenceDetail
}

// EvidenceDetail is one evidence row with the raw event it points to.
type EvidenceDetail struct {
	RawEventID   string
	Source       string
	Collector    string
	RunID        string
	ResourceType string
	ResourceID   string
}

// ExplainEdge returns the edge with the given id and its evidence chain,
// joined to the raw events that justify it.
func (s *Store) ExplainEdge(ctx context.Context, edgeID int64) (*EdgeDetail, error) {
	var d EdgeDetail
	err := s.pool.QueryRow(ctx,
		`SELECT id, src_id, relation, dst_id, confidence
		 FROM normalized_edges WHERE id = $1`, edgeID).
		Scan(&d.ID, &d.SrcID, &d.Relation, &d.DstID, &d.Confidence)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("store: edge %d not found", edgeID)
	}
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT ev.raw_event_id, ev.source, ev.collector, ev.run_id,
		        re.resource_type, re.resource_id
		 FROM evidence ev
		 JOIN raw_events re ON re.id = ev.raw_event_id
		 WHERE ev.element_kind = 'edge' AND ev.element_id = $1
		 ORDER BY ev.observed_at`, fmt.Sprintf("%d", edgeID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e EvidenceDetail
		if err := rows.Scan(&e.RawEventID, &e.Source, &e.Collector, &e.RunID, &e.ResourceType, &e.ResourceID); err != nil {
			return nil, err
		}
		d.Evidence = append(d.Evidence, e)
	}
	return &d, rows.Err()
}

// EvidenceForAsset returns the evidence trail backing an asset: every evidence
// row attached to an edge that touches the asset node (as either endpoint),
// joined to the raw event that justifies it. It is the read-only trail the
// finding-detail API (Requirement 13.2) attaches to a finding so a reviewer can
// see the customer raw_events that put the asset in the graph in the first
// place. The query is parameterized on the asset's canonical_id and never
// writes.
func (s *Store) EvidenceForAsset(ctx context.Context, assetID string, limit int) ([]EvidenceDetail, error) {
	if limit <= 0 {
		limit = 100
	}
	// The trail is the union of two evidence sets, both keyed on the asset's
	// canonical_id: evidence attached directly to the asset entity
	// (element_kind = 'entity', element_id = canonical_id) and evidence backing
	// any edge that touches the asset as a source or destination endpoint. Both
	// branches read through the same evidence -> raw_events join ExplainEdge
	// uses, so the trail cites the customer raw_events, never fabricated rows.
	rows, err := s.pool.Query(ctx,
		`SELECT raw_event_id, source, collector, run_id,
		        resource_type, resource_id
		   FROM (
		     SELECT ev.raw_event_id, ev.source, ev.collector, ev.run_id,
		            re.resource_type, re.resource_id, ev.observed_at
		       FROM evidence ev
		       JOIN raw_events re ON re.id = ev.raw_event_id
		      WHERE ev.element_kind = 'entity' AND ev.element_id = $1
		     UNION
		     SELECT ev.raw_event_id, ev.source, ev.collector, ev.run_id,
		            re.resource_type, re.resource_id, ev.observed_at
		       FROM evidence ev
		       JOIN raw_events re ON re.id = ev.raw_event_id
		       JOIN normalized_edges ne
		         ON ne.id = ev.element_id::bigint
		      WHERE ev.element_kind = 'edge'
		        AND (ne.src_id = $1 OR ne.dst_id = $1)
		   ) t
		  ORDER BY t.observed_at
		  LIMIT $2`, assetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvidenceDetail
	for rows.Next() {
		var e EvidenceDetail
		if err := rows.Scan(&e.RawEventID, &e.Source, &e.Collector, &e.RunID, &e.ResourceType, &e.ResourceID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FindEdges returns edge ids matching an optional relation and/or endpoint
// substring, to help the user locate an edge id for `explain`.
func (s *Store) FindEdges(ctx context.Context, relation, endpointLike string, limit int) ([]EdgeDetail, error) {
	q := `SELECT id, src_id, relation, dst_id, confidence FROM normalized_edges WHERE 1=1`
	var args []any
	if relation != "" {
		args = append(args, relation)
		q += fmt.Sprintf(" AND relation = $%d", len(args))
	}
	if endpointLike != "" {
		args = append(args, "%"+endpointLike+"%")
		q += fmt.Sprintf(" AND (src_id ILIKE $%d OR dst_id ILIKE $%d)", len(args), len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY id LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EdgeDetail
	for rows.Next() {
		var d EdgeDetail
		if err := rows.Scan(&d.ID, &d.SrcID, &d.Relation, &d.DstID, &d.Confidence); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
