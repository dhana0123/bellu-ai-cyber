package store

import "context"

// Stats summarizes the current state of the evidence store and graph tables,
// for the control-panel dashboard.
type Stats struct {
	RawEvents    int64            `json:"raw_events"`
	Entities     int64            `json:"entities"`
	Edges        int64            `json:"edges"`
	Runs         int64            `json:"runs"`
	Conflicts    int64            `json:"conflicts"`
	UnresolvedEdges int64         `json:"unresolved_edges"`
	DroppedEntities int64         `json:"dropped_entities"`
	GraphWriteGaps  int64         `json:"graph_write_gaps"`
	EntitiesByKind map[string]int64 `json:"entities_by_kind"`
	EdgesByRelation map[string]int64 `json:"edges_by_relation"`
}

// GetStats returns dashboard counts.
func (s *Store) GetStats(ctx context.Context) (*Stats, error) {
	st := &Stats{
		EntitiesByKind:  map[string]int64{},
		EdgesByRelation: map[string]int64{},
	}
	scalar := func(q string, dst *int64) error {
		return s.pool.QueryRow(ctx, q).Scan(dst)
	}
	if err := scalar(`SELECT count(*) FROM raw_events`, &st.RawEvents); err != nil {
		return nil, err
	}
	if err := scalar(`SELECT count(*) FROM normalized_entities`, &st.Entities); err != nil {
		return nil, err
	}
	if err := scalar(`SELECT count(*) FROM normalized_edges`, &st.Edges); err != nil {
		return nil, err
	}
	if err := scalar(`SELECT count(*) FROM collection_runs`, &st.Runs); err != nil {
		return nil, err
	}
	if err := scalar(`SELECT count(*) FROM resolution_conflicts`, &st.Conflicts); err != nil {
		return nil, err
	}
	if err := scalar(`SELECT count(*) FROM unresolved_edges`, &st.UnresolvedEdges); err != nil {
		return nil, err
	}
	if err := scalar(`SELECT count(*) FROM dropped_entities`, &st.DroppedEntities); err != nil {
		return nil, err
	}
	if err := scalar(`SELECT count(*) FROM graph_write_gaps`, &st.GraphWriteGaps); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `SELECT kind, count(*) FROM normalized_entities GROUP BY kind ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st.EntitiesByKind[k] = n
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `SELECT relation, count(*) FROM normalized_edges GROUP BY relation ORDER BY relation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		var n int64
		if err := rows.Scan(&r, &n); err != nil {
			return nil, err
		}
		st.EdgesByRelation[r] = n
	}
	return st, rows.Err()
}

// EntityRow is a normalized entity for API listing.
type EntityRow struct {
	CanonicalID string         `json:"canonical_id"`
	Kind        string         `json:"kind"`
	Provider    string         `json:"provider"`
	Name        string         `json:"name"`
	Attributes  map[string]any `json:"attributes"`
	Stale       bool           `json:"stale"`
}

// ListEntities returns entities, optionally filtered by kind, with a limit.
func (s *Store) ListEntities(ctx context.Context, kind string, limit int) ([]EntityRow, error) {
	q := `SELECT canonical_id, kind, provider, name, attributes, stale FROM normalized_entities`
	var args []any
	if kind != "" {
		args = append(args, kind)
		q += ` WHERE kind = $1`
	}
	args = append(args, limit)
	q += ` ORDER BY kind, canonical_id LIMIT $` + itoa(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntityRow
	for rows.Next() {
		var e EntityRow
		var attrs []byte
		if err := rows.Scan(&e.CanonicalID, &e.Kind, &e.Provider, &e.Name, &attrs, &e.Stale); err != nil {
			return nil, err
		}
		_ = unmarshalInto(attrs, &e.Attributes)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RunRow is a collection run for API listing.
type RunRow struct {
	RunID      string `json:"run_id"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	EventCount int    `json:"event_count"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// ListRuns returns collection runs, newest first.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]RunRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT run_id, source, status, event_count,
		        to_char(started_at, 'YYYY-MM-DD"T"HH24:MI:SSZ'),
		        COALESCE(to_char(finished_at, 'YYYY-MM-DD"T"HH24:MI:SSZ'), '')
		 FROM collection_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		if err := rows.Scan(&r.RunID, &r.Source, &r.Status, &r.EventCount, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Conflicts returns recorded resolution conflicts.
func (s *Store) Conflicts(ctx context.Context, limit int) ([]ConflictRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, reason, detail FROM resolution_conflicts ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConflictRow
	for rows.Next() {
		var c ConflictRow
		if err := rows.Scan(&c.Key, &c.Reason, &c.Detail); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
