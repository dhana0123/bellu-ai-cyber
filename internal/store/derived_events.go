package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DerivedEventRow is one append-only derived_events row (migration 00016). It
// carries either an inferred edge (Kind == "inferred_edge", Relation and DstID
// set) or a datastore classification (Kind == "classification", Relation and
// DstID empty). The caller sets the lane-specific change_type (e.g.
// "inferred.added" / "classified"); the derived_events CHECK constraints reject
// any observed-lane change type, keeping the facts and derived lanes separate.
type DerivedEventRow struct {
	Kind        string         // 'inferred_edge' | 'classification'
	ChangeType  string         // derived-lane change_type (CHECK-constrained)
	Origin      string         // 'rule' | 'model'
	Producer    string         // rule name or model id
	CanonicalID string         // classified entity, or inferred-edge src
	Relation    string         // inferred_edge only
	DstID       string         // inferred_edge only
	Attributes  map[string]any // classification payload / optional edge attrs
	Confidence  float64
	EvidenceIDs []string
	EventTime   time.Time // when the derivation became true
}

// InsertDerivedEvents writes a batch of derived_events rows for a run in a
// single transaction: either all rows persist or none do. recorded_at is set to
// the ingest time (now) for every row so the derived lane shares the same
// bi-temporal shape as the observed lane. An empty batch is a no-op.
//
// Relation/DstID are written as SQL NULL for classification rows (Relation ==
// "") so the derived_events shape CHECK is satisfied; inferred-edge rows carry
// both. The transaction rolls back on the first failing insert and returns the
// error, so a partial derived set never lands.
func (s *Store) InsertDerivedEvents(ctx context.Context, org int64, runID string, rows []DerivedEventRow) error {
	if len(rows) == 0 {
		return nil
	}
	recordedAt := time.Now().UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin derived tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	for _, r := range rows {
		attrs, err := json.Marshal(nonNilAttrs(r.Attributes))
		if err != nil {
			return fmt.Errorf("store: marshal derived attributes: %w", err)
		}
		evidence := r.EvidenceIDs
		if evidence == nil {
			evidence = []string{}
		}
		var relation, dstID any
		if r.Kind == "inferred_edge" {
			relation = r.Relation
			dstID = r.DstID
		}
		eventTime := r.EventTime
		if eventTime.IsZero() {
			eventTime = recordedAt
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO derived_events
			   (org_id, run_id, kind, change_type, origin, producer, canonical_id,
			    relation, dst_id, attributes, confidence, evidence_ids,
			    event_time, recorded_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			org, runID, r.Kind, r.ChangeType, r.Origin, r.Producer, r.CanonicalID,
			relation, dstID, attrs, r.Confidence, evidence,
			eventTime, recordedAt); err != nil {
			return fmt.Errorf("store: insert derived event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit derived tx: %w", err)
	}
	committed = true
	return nil
}

// nonNilAttrs returns a non-nil attribute map so JSONB columns never receive a
// SQL NULL where the schema defaults to '{}'.
func nonNilAttrs(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
