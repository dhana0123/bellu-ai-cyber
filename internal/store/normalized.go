package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// LoadRawEvents returns all raw events for a run, ordered deterministically.
// If runID is empty, it loads every event (used for full re-normalization).
func (s *Store) LoadRawEvents(ctx context.Context, runID string) ([]rawevent.RawEvent, error) {
	var (
		rows pgx.Rows
		err  error
	)
	const cols = `id, run_id, source, collector, provider, scope, region,
		resource_type, resource_id, observed_at, payload, payload_hash`
	if runID == "" {
		rows, err = s.pool.Query(ctx, `SELECT `+cols+` FROM raw_events ORDER BY source, resource_type, resource_id`)
	} else {
		rows, err = s.pool.Query(ctx, `SELECT `+cols+` FROM raw_events WHERE run_id = $1 ORDER BY source, resource_type, resource_id`, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("store: load raw events: %w", err)
	}
	defer rows.Close()

	var out []rawevent.RawEvent
	for rows.Next() {
		var e rawevent.RawEvent
		var payload []byte
		if err := rows.Scan(&e.ID, &e.RunID, &e.Source, &e.Collector, &e.Provider,
			&e.Scope, &e.Region, &e.ResourceType, &e.ResourceID, &e.ObservedAt,
			&payload, &e.PayloadHash); err != nil {
			return nil, err
		}
		e.Payload = payload
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveEntity upserts a normalized entity, bumping last_seen and merging
// attributes. first_seen is preserved on update.
func (s *Store) SaveEntity(ctx context.Context, tx pgx.Tx, e model.Entity, observed string) error {
	attrs, err := json.Marshal(e.Attributes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO normalized_entities (canonical_id, kind, provider, name, attributes, first_seen, last_seen, stale)
		 VALUES ($1,$2,$3,$4,$5, now(), now(), false)
		 ON CONFLICT (canonical_id) DO UPDATE SET
		   last_seen = now(),
		   stale = false,
		   name = CASE WHEN normalized_entities.name = '' THEN EXCLUDED.name ELSE normalized_entities.name END,
		   attributes = normalized_entities.attributes || EXCLUDED.attributes`,
		e.CanonicalID, string(e.Kind), e.Provider, e.Name, attrs)
	return err
}

// SaveEdge upserts a normalized edge and returns its id. Evidence is merged by
// the caller via SaveEvidence.
func (s *Store) SaveEdge(ctx context.Context, tx pgx.Tx, e model.Edge) (int64, error) {
	attrs, err := json.Marshal(e.Attributes)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO normalized_edges (src_id, relation, dst_id, attributes, confidence, first_seen, last_seen, stale)
		 VALUES ($1,$2,$3,$4,$5, now(), now(), false)
		 ON CONFLICT (src_id, relation, dst_id) DO UPDATE SET
		   last_seen = now(),
		   stale = false,
		   confidence = GREATEST(normalized_edges.confidence, EXCLUDED.confidence),
		   attributes = normalized_edges.attributes || EXCLUDED.attributes
		 RETURNING id`,
		e.SrcCanonicalID, string(e.Relation), e.DstCanonicalID, attrs, e.Confidence).Scan(&id)
	return id, err
}

// SaveEvidence records one evidence row linking an element to a raw event.
func (s *Store) SaveEvidence(ctx context.Context, tx pgx.Tx, elementKind, elementID string, ev model.Evidence) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO evidence (element_kind, element_id, raw_event_id, source, collector, run_id, observed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (element_kind, element_id, raw_event_id) DO NOTHING`,
		elementKind, elementID, ev.RawEventID, ev.Source, ev.Collector, ev.RunID, ev.ObservedAt)
	return err
}

// PersistNormalized writes entities, edges and their evidence in one
// transaction.
func (s *Store) PersistNormalized(ctx context.Context, entities []model.Entity, edges []model.Edge) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, e := range entities {
		if err := s.SaveEntity(ctx, tx, e, ""); err != nil {
			return fmt.Errorf("save entity %s: %w", e.CanonicalID, err)
		}
		for _, ev := range e.Evidence {
			if err := s.SaveEvidence(ctx, tx, "entity", e.CanonicalID, ev); err != nil {
				return fmt.Errorf("save entity evidence %s: %w", e.CanonicalID, err)
			}
		}
	}

	for _, e := range edges {
		id, err := s.SaveEdge(ctx, tx, e)
		if err != nil {
			return fmt.Errorf("save edge %s-%s->%s: %w", e.SrcCanonicalID, e.Relation, e.DstCanonicalID, err)
		}
		edgeID := fmt.Sprintf("%d", id)
		for _, ev := range e.Evidence {
			if err := s.SaveEvidence(ctx, tx, "edge", edgeID, ev); err != nil {
				return fmt.Errorf("save edge evidence %d: %w", id, err)
			}
		}
	}

	return tx.Commit(ctx)
}
