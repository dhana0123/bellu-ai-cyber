package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UnresolvedEdgeRow is one edge the resolver could not resolve, recorded for
// visibility (the edge is still excluded from the graph).
type UnresolvedEdgeRow struct {
	Relation      string
	SrcKey        string
	DstKey        string
	UnmatchedSide string // "src" | "dst" | "both"
	EvidenceID    string
	RunID         string
}

// DroppedEntityRow is one entity a normalizer produced with an empty Key.
type DroppedEntityRow struct {
	Kind       string
	Provider   string
	EvidenceID string
	RunID      string
}

// GraphWriteGapRow is one edge batch for which Memgraph created or matched
// fewer relationships than were submitted.
type GraphWriteGapRow struct {
	Relation  string
	SyncRunID string
	BatchSize int
	Observed  int
}

// SaveUnresolvedEdges appends unresolved-edge records.
func (s *Store) SaveUnresolvedEdges(ctx context.Context, rows []UnresolvedEdgeRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(
			`INSERT INTO unresolved_edges (relation, src_key, dst_key, unmatched_side, evidence_id, run_id)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			r.Relation, r.SrcKey, r.DstKey, r.UnmatchedSide, r.EvidenceID, r.RunID)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store: save unresolved edge: %w", err)
		}
	}
	return nil
}

// SaveDroppedEntities appends dropped-entity records.
func (s *Store) SaveDroppedEntities(ctx context.Context, rows []DroppedEntityRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(
			`INSERT INTO dropped_entities (kind, provider, evidence_id, run_id)
			 VALUES ($1,$2,$3,$4)`,
			r.Kind, r.Provider, r.EvidenceID, r.RunID)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store: save dropped entity: %w", err)
		}
	}
	return nil
}

// SaveGraphWriteGaps appends graph-write-gap records.
func (s *Store) SaveGraphWriteGaps(ctx context.Context, rows []GraphWriteGapRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(
			`INSERT INTO graph_write_gaps (relation, sync_run_id, batch_size, observed)
			 VALUES ($1,$2,$3,$4)`,
			r.Relation, r.SyncRunID, r.BatchSize, r.Observed)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store: save graph write gap: %w", err)
		}
	}
	return nil
}

// UnresolvedEdgeFilter narrows an unresolved-edge query. Empty fields are
// ignored.
type UnresolvedEdgeFilter struct {
	Relation string
	Key      string // matches either src_key or dst_key
	RunID    string
}

// QueryUnresolvedEdges returns unresolved-edge records matching the filter.
func (s *Store) QueryUnresolvedEdges(ctx context.Context, f UnresolvedEdgeFilter, limit int) ([]UnresolvedEdgeRow, error) {
	q := `SELECT relation, src_key, dst_key, unmatched_side, evidence_id, run_id FROM unresolved_edges`
	var args []any
	var conds []string
	if f.Relation != "" {
		args = append(args, f.Relation)
		conds = append(conds, `relation = $`+itoa(len(args)))
	}
	if f.Key != "" {
		args = append(args, f.Key)
		conds = append(conds, `(src_key = $`+itoa(len(args))+` OR dst_key = $`+itoa(len(args))+`)`)
	}
	if f.RunID != "" {
		args = append(args, f.RunID)
		conds = append(conds, `run_id = $`+itoa(len(args)))
	}
	q = appendWhere(q, conds)
	args = append(args, limit)
	q += ` ORDER BY created_at DESC LIMIT $` + itoa(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnresolvedEdgeRow
	for rows.Next() {
		var r UnresolvedEdgeRow
		if err := rows.Scan(&r.Relation, &r.SrcKey, &r.DstKey, &r.UnmatchedSide, &r.EvidenceID, &r.RunID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DroppedEntityFilter narrows a dropped-entity query. Empty fields are ignored.
type DroppedEntityFilter struct {
	Kind     string
	Provider string
	RunID    string
}

// QueryDroppedEntities returns dropped-entity records matching the filter.
func (s *Store) QueryDroppedEntities(ctx context.Context, f DroppedEntityFilter, limit int) ([]DroppedEntityRow, error) {
	q := `SELECT kind, provider, evidence_id, run_id FROM dropped_entities`
	var args []any
	var conds []string
	if f.Kind != "" {
		args = append(args, f.Kind)
		conds = append(conds, `kind = $`+itoa(len(args)))
	}
	if f.Provider != "" {
		args = append(args, f.Provider)
		conds = append(conds, `provider = $`+itoa(len(args)))
	}
	if f.RunID != "" {
		args = append(args, f.RunID)
		conds = append(conds, `run_id = $`+itoa(len(args)))
	}
	q = appendWhere(q, conds)
	args = append(args, limit)
	q += ` ORDER BY created_at DESC LIMIT $` + itoa(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DroppedEntityRow
	for rows.Next() {
		var r DroppedEntityRow
		if err := rows.Scan(&r.Kind, &r.Provider, &r.EvidenceID, &r.RunID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GraphWriteGapFilter narrows a graph-write-gap query. Empty fields are ignored.
type GraphWriteGapFilter struct {
	Relation  string
	SyncRunID string
}

// QueryGraphWriteGaps returns graph-write-gap records matching the filter.
func (s *Store) QueryGraphWriteGaps(ctx context.Context, f GraphWriteGapFilter, limit int) ([]GraphWriteGapRow, error) {
	q := `SELECT relation, sync_run_id, batch_size, observed FROM graph_write_gaps`
	var args []any
	var conds []string
	if f.Relation != "" {
		args = append(args, f.Relation)
		conds = append(conds, `relation = $`+itoa(len(args)))
	}
	if f.SyncRunID != "" {
		args = append(args, f.SyncRunID)
		conds = append(conds, `sync_run_id = $`+itoa(len(args)))
	}
	q = appendWhere(q, conds)
	args = append(args, limit)
	q += ` ORDER BY created_at DESC LIMIT $` + itoa(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GraphWriteGapRow
	for rows.Next() {
		var r GraphWriteGapRow
		if err := rows.Scan(&r.Relation, &r.SyncRunID, &r.BatchSize, &r.Observed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// appendWhere joins conditions into a WHERE clause, returning q unchanged when
// there are none.
func appendWhere(q string, conds []string) string {
	if len(conds) == 0 {
		return q
	}
	out := q + " WHERE " + conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out
}
