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
