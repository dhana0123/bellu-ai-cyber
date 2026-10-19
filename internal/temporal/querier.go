package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// Querier answers bi-temporal (as-of / asKnownAt) questions over the two
// append-only logs. A point-in-time state for an element is the fold of its
// event stream up to a cutoff; two independent cutoffs give the four views
// (Req 6):
//
//	current (reality)  all events, event_time <= now()
//	as-of T            event_time <= T
//	audit asKnownAt    event_time <= T_event AND recorded_at <= T_known
//	history            the ordered event stream itself (no fold)
//
// StateAt (this file, task 12.1) folds observed events to a single
// EntityState. History, RecentChanges, and the between-times diff land in task
// 12.2 as further methods on this same Querier; the struct and constructor are
// kept small so those compose without touching StateAt.
//
// Every method is tenant-scoped by org_id (Req 16.2): the org is always a query
// predicate, never trusted from a caller-supplied value embedded in the data.
type Querier struct {
	store *store.Store
}

// NewQuerier constructs a Querier over the facts store (the two logs live in
// the same customer PostgreSQL as the rest of the temporal store).
func NewQuerier(st *store.Store) *Querier {
	return &Querier{store: st}
}

// EntityState is the folded point-in-time state of a single entity: its full
// attribute snapshot at the cutoff plus whether it is present (live) at that
// cutoff. A terminal entity.deleted folds to Present=false with empty
// attributes; an entity with no events at or before the cutoff also folds to
// Present=false (it did not yet exist / was not yet known).
type EntityState struct {
	// CanonicalID is the entity the state was reconstructed for.
	CanonicalID string
	// Present reports whether the entity is live at the cutoff. False means the
	// entity was deleted at or before the cutoff, or had no events yet.
	Present bool
	// Attributes is the full attribute snapshot of the latest non-deleted
	// change at the cutoff. Empty when Present is false.
	Attributes map[string]any
	// ChangeType is the change_type of the terminal event that produced this
	// state (e.g. entity.created / entity.attribute_updated / entity.deleted).
	// Empty when the entity had no events at or before the cutoff.
	ChangeType string
	// EventTime is the event_time of that terminal event. Zero when absent.
	EventTime time.Time
}

// StateAt reconstructs an entity's attributes/liveness as of eventTime, as
// Bellu knew it at knownAt. Pass knownAt = time.Now() for the reality view
// (Req 6.1); pass only eventTime (knownAt = now) for a plain as-of view
// (Req 6.2); pass both for the audit asKnownAt view (Req 6.3).
//
// The fold keeps the latest observed row for the entity under the ordering
// (event_time DESC, recorded_at DESC, seq DESC) — i.e. events applied in
// (event_time, recorded_at, seq) order, latest wins (Req 6.4) — within the
// bi-temporal window event_time <= eventTime AND recorded_at <= knownAt. A
// terminal entity.deleted means the entity is absent at the cutoff; an entity
// with no rows in the window is likewise absent. The query is scoped to org_id
// so no other tenant's events can enter the fold (Req 16.2).
//
// StateAt never returns a nil *EntityState on success: an absent entity is
// returned as an EntityState with Present=false so callers can distinguish
// "known to be gone / not yet seen" from an error.
func (q *Querier) StateAt(ctx context.Context, org int64, canonicalID string, eventTime, knownAt time.Time) (*EntityState, error) {
	if canonicalID == "" {
		return nil, fmt.Errorf("temporal: StateAt requires a canonical_id")
	}
	if eventTime.IsZero() {
		eventTime = q.now()
	}
	if knownAt.IsZero() {
		knownAt = q.now()
	}

	// DISTINCT ON (canonical_id) with the fold ordering keeps exactly the latest
	// observed row for the entity within the bi-temporal window. seq is the
	// BIGSERIAL total-order tiebreak within a (event_time, recorded_at) pair.
	var (
		changeType string
		attrsJSON  []byte
		evTime     time.Time
	)
	err := q.store.Pool().QueryRow(ctx,
		`SELECT DISTINCT ON (canonical_id) change_type, attributes, event_time
		   FROM observed_events
		  WHERE org_id = $1
		    AND element_kind = 'entity'
		    AND canonical_id = $2
		    AND event_time  <= $3
		    AND recorded_at <= $4
		  ORDER BY canonical_id, event_time DESC, recorded_at DESC, seq DESC`,
		org, canonicalID, eventTime.UTC(), knownAt.UTC()).
		Scan(&changeType, &attrsJSON, &evTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No events at or before the cutoff: the entity is absent (not yet
			// created / not yet known at this bi-temporal point).
			return &EntityState{CanonicalID: canonicalID, Present: false, Attributes: map[string]any{}}, nil
		}
		return nil, fmt.Errorf("temporal: StateAt fold %s: %w", canonicalID, err)
	}

	// A terminal entity.deleted folds to absence: empty attributes, Present=false.
	if changeType == ChangeEntityDeleted {
		return &EntityState{
			CanonicalID: canonicalID,
			Present:     false,
			Attributes:  map[string]any{},
			ChangeType:  changeType,
			EventTime:   evTime.UTC(),
		}, nil
	}

	return &EntityState{
		CanonicalID: canonicalID,
		Present:     true,
		Attributes:  decodeAttrs(attrsJSON),
		ChangeType:  changeType,
		EventTime:   evTime.UTC(),
	}, nil
}

// ChangeRow is one row of an entity's merged change stream, drawn from either
// the observed lane (asserted facts) or the derived lane (inference /
// classification). It is the unit returned by History and RecentChanges and
// mirrors the columns the MCP entity_history / recent_changes tools surface.
type ChangeRow struct {
	// Seq is the row's BIGSERIAL total-order position within its lane. It is the
	// final tiebreak when event_time and recorded_at coincide.
	Seq int64
	// Lane is "observed" or "derived" — which log the row came from.
	Lane string
	// RunID is the collection run that produced the change.
	RunID string
	// Source is the producing collector family (observed) or empty for derived.
	Source string
	// Producer is the derived producer (rule name / model id); empty for observed.
	Producer string
	// ElementKind is "entity" or "edge" for observed rows; derived rows carry
	// "inferred_edge" or "classification" (the derived kind).
	ElementKind string
	// ChangeType is the lane-specific change_type value.
	ChangeType string
	// CanonicalID is the entity, or the src of an edge/inferred-edge change.
	CanonicalID string
	// Relation / DstID are set for edge and inferred-edge rows only.
	Relation string
	DstID    string
	// Attributes is the change payload (full snapshot for observed updates,
	// derived payload for derived rows). Never nil.
	Attributes map[string]any
	// Diff is the added/removed/changed attribute keys recorded on an observed
	// row. Empty for derived rows (the derived lane carries no diff column).
	Diff Diff
	// Confidence is the row's confidence (1.0 for asserted facts).
	Confidence float64
	// EvidenceIDs are the raw-event / observed-event ids that justify the change.
	EvidenceIDs []string
	// EventTime is when the change became true in the world.
	EventTime time.Time
	// RecordedAt is when Bellu learned / produced the change.
	RecordedAt time.Time
}

// HistoryOpts tunes a History query. The zero value returns the entity's full
// change stream oldest-first.
type HistoryOpts struct {
	// Newest reverses the default oldest-first ordering so the most recent
	// change is returned first.
	Newest bool
	// Limit caps the number of rows returned. Zero or negative means no cap.
	Limit int
	// IncludeDerived, when true, merges the derived lane (inferred edges and
	// classifications) into the stream alongside the observed facts. When false
	// only observed rows are returned.
	IncludeDerived bool
}

// History returns the ordered change stream for a single entity, merging the
// observed lane (and, when opts.IncludeDerived is set, the derived lane) by
// time. Rows are ordered by (event_time, recorded_at, seq) — oldest first by
// default, newest first when opts.Newest is set — and the result is capped to
// opts.Limit when positive (Req 6.6). The query is scoped to org_id so no other
// tenant's changes can enter the stream (Req 16.2).
//
// History never folds: it returns every recorded change for the entity, which
// is what the MCP entity_history tool surfaces. A missing entity yields an
// empty slice, not an error.
func (q *Querier) History(ctx context.Context, org int64, canonicalID string, opts HistoryOpts) ([]ChangeRow, error) {
	if canonicalID == "" {
		return nil, fmt.Errorf("temporal: History requires a canonical_id")
	}

	rows := make([]ChangeRow, 0, 16)

	observed, err := q.observedRowsForEntity(ctx, org, canonicalID)
	if err != nil {
		return nil, fmt.Errorf("temporal: History observed %s: %w", canonicalID, err)
	}
	rows = append(rows, observed...)

	if opts.IncludeDerived {
		derived, err := q.derivedRowsForEntity(ctx, org, canonicalID)
		if err != nil {
			return nil, fmt.Errorf("temporal: History derived %s: %w", canonicalID, err)
		}
		rows = append(rows, derived...)
	}

	sortChangeRows(rows, opts.Newest)
	rows = applyLimit(rows, opts.Limit)
	return rows, nil
}

// RecentChanges returns the tenant's observed changes recorded strictly after
// the since cutoff, newest first, capped to limit (Req 6.7). The cutoff ranges
// over recorded_at (when Bellu learned of the change) so a consumer can poll for
// "what changed since I last looked". The query is scoped to org_id (Req 16.2).
//
// limit must be positive; a non-positive limit is a plain input error. A zero
// since cutoff returns the most recent limit changes for the tenant.
func (q *Querier) RecentChanges(ctx context.Context, org int64, since time.Time, limit int) ([]ChangeRow, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("temporal: RecentChanges requires a positive limit, got %d", limit)
	}

	sqlRows, err := q.store.Pool().Query(ctx,
		`SELECT seq, run_id, source, element_kind, change_type, canonical_id,
		        relation, dst_id, attributes, diff, confidence, evidence_ids,
		        event_time, recorded_at
		   FROM observed_events
		  WHERE org_id = $1
		    AND recorded_at > $2
		  ORDER BY recorded_at DESC, event_time DESC, seq DESC
		  LIMIT $3`,
		org, since.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("temporal: RecentChanges query: %w", err)
	}
	defer sqlRows.Close()

	rows := make([]ChangeRow, 0, limit)
	for sqlRows.Next() {
		r, err := scanObservedRow(sqlRows)
		if err != nil {
			return nil, fmt.Errorf("temporal: RecentChanges scan: %w", err)
		}
		rows = append(rows, r)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, fmt.Errorf("temporal: RecentChanges iterate: %w", err)
	}
	return rows, nil
}

// DiffBetween returns the attribute changes between an entity's folded state at
// t1 and its folded state at t2 (Req 6.8). It reuses StateAt to fold each
// endpoint (reality view at each event_time, i.e. knownAt=now) and diffs the two
// attribute maps into added / removed / changed keys. The result is oriented
// t1 -> t2: a key only in t2 is Added, a key only in t1 is Removed, a key in
// both with a different value is Changed.
//
// The query is tenant-scoped through StateAt (Req 16.2). An empty canonical_id
// is a plain input error. When either endpoint folds to an absent entity its
// attribute map is empty, so the diff naturally reports the full set of the
// present endpoint's keys as added or removed.
func (q *Querier) DiffBetween(ctx context.Context, org int64, canonicalID string, t1, t2 time.Time) (Diff, error) {
	if canonicalID == "" {
		return Diff{}, fmt.Errorf("temporal: DiffBetween requires a canonical_id")
	}

	known := q.now()
	before, err := q.StateAt(ctx, org, canonicalID, t1, known)
	if err != nil {
		return Diff{}, fmt.Errorf("temporal: DiffBetween fold t1 %s: %w", canonicalID, err)
	}
	after, err := q.StateAt(ctx, org, canonicalID, t2, known)
	if err != nil {
		return Diff{}, fmt.Errorf("temporal: DiffBetween fold t2 %s: %w", canonicalID, err)
	}

	return diffAttrs(before.Attributes, after.Attributes), nil
}

// observedRowsForEntity reads every observed_events row for one entity, tagged
// as the "observed" lane. Scoped to org_id.
func (q *Querier) observedRowsForEntity(ctx context.Context, org int64, canonicalID string) ([]ChangeRow, error) {
	sqlRows, err := q.store.Pool().Query(ctx,
		`SELECT seq, run_id, source, element_kind, change_type, canonical_id,
		        relation, dst_id, attributes, diff, confidence, evidence_ids,
		        event_time, recorded_at
		   FROM observed_events
		  WHERE org_id = $1
		    AND canonical_id = $2
		  ORDER BY event_time, recorded_at, seq`,
		org, canonicalID)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()

	var rows []ChangeRow
	for sqlRows.Next() {
		r, err := scanObservedRow(sqlRows)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, sqlRows.Err()
}

// derivedRowsForEntity reads every derived_events row targeting one entity,
// tagged as the "derived" lane. The derived lane has no diff column, so Diff is
// left empty. Scoped to org_id.
func (q *Querier) derivedRowsForEntity(ctx context.Context, org int64, canonicalID string) ([]ChangeRow, error) {
	sqlRows, err := q.store.Pool().Query(ctx,
		`SELECT seq, run_id, kind, change_type, producer, canonical_id,
		        relation, dst_id, attributes, confidence, evidence_ids,
		        event_time, recorded_at
		   FROM derived_events
		  WHERE org_id = $1
		    AND canonical_id = $2
		  ORDER BY event_time, recorded_at, seq`,
		org, canonicalID)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()

	var rows []ChangeRow
	for sqlRows.Next() {
		var (
			r               ChangeRow
			relation, dstID *string
			attrsJSON       []byte
			evidenceIDs     []string
		)
		if err := sqlRows.Scan(
			&r.Seq, &r.RunID, &r.ElementKind, &r.ChangeType, &r.Producer, &r.CanonicalID,
			&relation, &dstID, &attrsJSON, &r.Confidence, &evidenceIDs,
			&r.EventTime, &r.RecordedAt,
		); err != nil {
			return nil, err
		}
		r.Lane = "derived"
		r.Relation = derefString(relation)
		r.DstID = derefString(dstID)
		r.Attributes = decodeAttrs(attrsJSON)
		r.EvidenceIDs = nonNilStrings(evidenceIDs)
		r.EventTime = r.EventTime.UTC()
		r.RecordedAt = r.RecordedAt.UTC()
		rows = append(rows, r)
	}
	return rows, sqlRows.Err()
}

// rowScanner is the subset of pgx.Rows needed to scan a single observed row,
// so scanObservedRow works for both single-entity and tenant-wide queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanObservedRow scans one observed_events row (in the column order used by
// observedRowsForEntity and RecentChanges) into a ChangeRow tagged "observed".
func scanObservedRow(s rowScanner) (ChangeRow, error) {
	var (
		r               ChangeRow
		source          string
		relation, dstID *string
		attrsJSON       []byte
		diffJSON        []byte
		evidenceIDs     []string
	)
	if err := s.Scan(
		&r.Seq, &r.RunID, &source, &r.ElementKind, &r.ChangeType, &r.CanonicalID,
		&relation, &dstID, &attrsJSON, &diffJSON, &r.Confidence, &evidenceIDs,
		&r.EventTime, &r.RecordedAt,
	); err != nil {
		return ChangeRow{}, err
	}
	r.Lane = "observed"
	r.Source = source
	r.Relation = derefString(relation)
	r.DstID = derefString(dstID)
	r.Attributes = decodeAttrs(attrsJSON)
	r.Diff = decodeDiff(diffJSON)
	r.EvidenceIDs = nonNilStrings(evidenceIDs)
	r.EventTime = r.EventTime.UTC()
	r.RecordedAt = r.RecordedAt.UTC()
	return r, nil
}

// sortChangeRows orders rows by (event_time, recorded_at, seq). newest reverses
// to most-recent-first. The sort is stable on the total order so merged lanes
// interleave deterministically.
func sortChangeRows(rows []ChangeRow, newest bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.EventTime.Equal(b.EventTime) {
			if newest {
				return a.EventTime.After(b.EventTime)
			}
			return a.EventTime.Before(b.EventTime)
		}
		if !a.RecordedAt.Equal(b.RecordedAt) {
			if newest {
				return a.RecordedAt.After(b.RecordedAt)
			}
			return a.RecordedAt.Before(b.RecordedAt)
		}
		if newest {
			return a.Seq > b.Seq
		}
		return a.Seq < b.Seq
	})
}

// applyLimit truncates rows to limit when limit is positive; a non-positive
// limit means "no cap".
func applyLimit(rows []ChangeRow, limit int) []ChangeRow {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

// decodeDiff unmarshals the observed diff column, tolerating null/empty.
func decodeDiff(b []byte) Diff {
	if len(b) == 0 {
		return Diff{}
	}
	var d Diff
	if err := json.Unmarshal(b, &d); err != nil {
		return Diff{}
	}
	return d
}

// derefString returns the pointed-to string, or "" for a nil pointer (NULL
// relation/dst_id on entity rows).
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nonNilStrings normalizes a nil slice to a non-nil empty slice so callers never
// see nil evidence.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// now returns the current time for defaulting zero cutoffs to the reality view.
func (q *Querier) now() time.Time { return time.Now().UTC() }
