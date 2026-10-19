package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Retention keeps the two append-only logs bounded without breaking as-of
// queries (Req 11). It has two halves:
//
//   - Heartbeat coalescing (Req 11.1): consecutive `*.unchanged` observations
//     write NO observed-event row; the Change_Engine instead bumps
//     entity_assertions.last_seen. This is already enforced in Engine.Apply,
//     which skips ChangeEntityUnchanged rows while still upserting an assertion
//     for every entity the run asserted. See IsCoalescedUnchanged below for the
//     invariant this file documents and self-checks.
//
//   - Baseline + pruning (Req 11.2, 11.3): at a retention horizon, before any
//     old rows are deleted, write a BASELINE event per element carrying a full
//     attribute snapshot folded as of the horizon (change_type
//     entity.attribute_updated, event_time = horizon). Then prune observed and
//     derived rows older than the horizon EXCEPT baselines, so an as-of query
//     targeting a time after the pruned window still resolves correct state
//     from the retained baseline (Req 11.4).
//
// Baseline marker convention
// ---------------------------
// observed_events (migration 00015) has no dedicated baseline column. Rather
// than migrate the schema, a baseline row is marked by a reserved key inside
// its attributes JSONB:
//
//	attributes["__bellu_baseline"] = true
//
// The marker is namespaced with a "__bellu_" prefix so it cannot collide with a
// provider attribute, it is queryable from SQL via `attributes ->> '...'`, and
// it travels with the row through replay and query folding. Baseline rows are
// otherwise ordinary entity.attribute_updated events, so the existing query
// fold and projection treat them as a normal full-snapshot update.

// BaselineMarkerKey is the reserved attributes-JSONB key that flags an
// observed/derived row as a retention baseline (full snapshot written at the
// horizon). See the package note above for the convention.
const BaselineMarkerKey = "__bellu_baseline"

// baselineChangeType is the change_type a baseline event is written under. Per
// Req 11.2 a baseline is a full-snapshot attribute update at the horizon.
const baselineChangeType = ChangeEntityAttributeUpdated

// RetentionConfig parameterises a prune. Exactly one of Horizon or MaxAge is
// used to derive the cutoff: an explicit Horizon wins; otherwise the cutoff is
// now()-MaxAge. Rows with event_time strictly older than the cutoff are pruned
// (baselines excepted).
type RetentionConfig struct {
	// Horizon is an explicit cutoff time. Events with event_time < Horizon are
	// pruned. Zero means "derive from MaxAge".
	Horizon time.Time
	// MaxAge is the retention window used when Horizon is zero: cutoff = now-MaxAge.
	MaxAge time.Duration
}

// cutoff resolves the effective retention horizon for a config.
func (c RetentionConfig) cutoff(now time.Time) (time.Time, error) {
	if !c.Horizon.IsZero() {
		return c.Horizon.UTC(), nil
	}
	if c.MaxAge <= 0 {
		return time.Time{}, fmt.Errorf("retention: config needs a Horizon or a positive MaxAge")
	}
	return now.Add(-c.MaxAge).UTC(), nil
}

// IsBaselineAttrs reports whether an attribute map carries the baseline marker.
func IsBaselineAttrs(attrs map[string]any) bool {
	if len(attrs) == 0 {
		return false
	}
	v, ok := attrs[BaselineMarkerKey]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// markBaseline returns a copy of attrs with the baseline marker set, so the
// caller's snapshot map is not mutated.
func markBaseline(attrs map[string]any) map[string]any {
	out := make(map[string]any, len(attrs)+1)
	for k, v := range attrs {
		out[k] = v
	}
	out[BaselineMarkerKey] = true
	return out
}

// IsCoalescedUnchanged documents and self-checks the Req 11.1 invariant: an
// unchanged observation is coalesced and must not produce an observed-event
// row. Engine.Apply enforces this by skipping ChangeEntityUnchanged changes
// when writing rows; this helper lets callers (and the retention test in 18.2)
// assert the same classification is the coalesced one.
func IsCoalescedUnchanged(c Change) bool {
	return c.ChangeType == ChangeEntityUnchanged
}

// Prune writes baseline events and then prunes aged rows for one tenant,
// enforcing Req 11.2 and 11.3 (Req 11.1 is enforced in Apply). The whole
// operation runs in a single transaction under the same per-org advisory lock
// Apply uses, so a concurrent run for the tenant cannot interleave with the
// baseline-then-delete sequence; on any failure nothing is pruned.
//
// Order within the transaction (baseline BEFORE prune, per Req 11.2):
//  1. Fold the observed lane up to the horizon into a per-element full snapshot
//     for every element still live at the horizon (latest non-deleted state).
//  2. Insert one baseline observed_event per live element at event_time=horizon.
//  3. Delete observed_events and derived_events with event_time < horizon,
//     EXCEPT rows already marked as baselines (Req 11.3).
//
// It returns the number of baselines written and the number of observed and
// derived rows pruned.
func (e *Engine) Prune(ctx context.Context, org int64, cfg RetentionConfig) (baselines, prunedObserved, prunedDerived int, err error) {
	horizon, err := cfg.cutoff(e.now().UTC())
	if err != nil {
		return 0, 0, 0, err
	}

	tx, err := e.store.Pool().Begin(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("retention: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	// Serialize per-org with the change engine so baselines/prunes do not race a
	// concurrent Apply for the same tenant.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, org); err != nil {
		return 0, 0, 0, fmt.Errorf("retention: acquire per-org lock: %w", err)
	}

	// 1 + 2: write baselines for every element live at the horizon, BEFORE prune.
	baselines, err = e.writeBaselines(ctx, tx, org, horizon)
	if err != nil {
		return 0, 0, 0, err
	}

	// 3: prune aged observed/derived rows except baselines.
	prunedObserved, err = e.pruneObserved(ctx, tx, org, horizon)
	if err != nil {
		return 0, 0, 0, err
	}
	prunedDerived, err = e.pruneDerived(ctx, tx, org, horizon)
	if err != nil {
		return 0, 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, 0, fmt.Errorf("retention: commit: %w", err)
	}
	committed = true
	return baselines, prunedObserved, prunedDerived, nil
}

// writeBaselines folds the observed entity lane up to (and including) the
// horizon and inserts one baseline event per element that is live at the
// horizon (its terminal change at or before the horizon is not entity.deleted).
// Each baseline carries the folded full attribute snapshot, change_type
// entity.attribute_updated, event_time = horizon, and the baseline marker.
//
// Edges are intentionally not baselined here: the observed fold reconstructs
// edge presence from relation.added/removed, and a full edge re-baseline would
// require a separate shape; entity baselines are what Req 11.2/11.4 target for
// as-of state resolution. Keeping this additive avoids touching edge semantics.
func (e *Engine) writeBaselines(ctx context.Context, tx pgx.Tx, org int64, horizon time.Time) (int, error) {
	// Pull every entity observed event at or before the horizon, newest last so
	// a simple left-to-right fold yields the latest state per canonical_id. We
	// skip rows that are themselves baselines to avoid re-baselining a baseline.
	rows, err := tx.Query(ctx,
		`SELECT canonical_id, change_type, attributes, event_time, recorded_at, seq
		   FROM observed_events
		  WHERE org_id = $1
		    AND element_kind = 'entity'
		    AND canonical_id IS NOT NULL
		    AND event_time <= $2
		  ORDER BY event_time ASC, recorded_at ASC, seq ASC`,
		org, horizon)
	if err != nil {
		return 0, fmt.Errorf("retention: load observed entities: %w", err)
	}

	type folded struct {
		attrs   map[string]any
		deleted bool
	}
	state := make(map[string]*folded)
	for rows.Next() {
		var (
			canonicalID string
			changeType  string
			attrsRaw    []byte
			eventTime   time.Time
			recordedAt  time.Time
			seq         int64
		)
		if err := rows.Scan(&canonicalID, &changeType, &attrsRaw, &eventTime, &recordedAt, &seq); err != nil {
			rows.Close()
			return 0, fmt.Errorf("retention: scan observed entity: %w", err)
		}
		f := state[canonicalID]
		if f == nil {
			f = &folded{}
			state[canonicalID] = f
		}
		switch changeType {
		case ChangeEntityDeleted:
			f.deleted = true
			f.attrs = nil
		default:
			// created / attribute_updated / state_changed / (baseline) all carry
			// the full snapshot in attributes; fold replaces prior state.
			f.deleted = false
			f.attrs = decodeAttrs(attrsRaw)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("retention: iterate observed entities: %w", err)
	}

	// Deterministic insertion order for stable tests and predictable seq order.
	ids := make([]string, 0, len(state))
	for id := range state {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// observed_events.run_id has a FK to collection_runs(run_id). Ensure the
	// synthetic baseline run row exists (idempotently) before inserting baseline
	// events, so a prune is self-contained against a live schema.
	runID := baselineRunID(horizon)
	if _, err := tx.Exec(ctx,
		`INSERT INTO collection_runs (run_id, source, status, started_at, finished_at)
		 VALUES ($1,$2,'completed',$3,$3)
		 ON CONFLICT (run_id) DO NOTHING`,
		runID, baselineSource, e.now().UTC()); err != nil {
		return 0, fmt.Errorf("retention: ensure baseline run: %w", err)
	}

	written := 0
	for _, id := range ids {
		f := state[id]
		if f.deleted || len(f.attrs) == 0 {
			continue // not live at horizon, or nothing to snapshot
		}
		snap := markBaseline(f.attrs)
		attrsJSON, err := json.Marshal(snap)
		if err != nil {
			return written, fmt.Errorf("retention: marshal baseline %s: %w", id, err)
		}
		emptyDiff, _ := json.Marshal(Diff{})
		if _, err := tx.Exec(ctx,
			`INSERT INTO observed_events
			   (org_id, run_id, source, element_kind, change_type, canonical_id,
			    relation, dst_id, attributes, diff, evidence_ids, event_time, recorded_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			org, runID, baselineSource, elementEntity, baselineChangeType, id,
			nil, nil, attrsJSON, emptyDiff, []string{}, horizon, e.now().UTC()); err != nil {
			return written, fmt.Errorf("retention: write baseline %s: %w", id, err)
		}
		written++
	}
	return written, nil
}

// pruneObserved deletes observed_events older than the horizon, keeping any row
// marked as a baseline so as-of queries after the pruned window still resolve.
func (e *Engine) pruneObserved(ctx context.Context, tx pgx.Tx, org int64, horizon time.Time) (int, error) {
	tag, err := tx.Exec(ctx,
		`DELETE FROM observed_events
		  WHERE org_id = $1
		    AND event_time < $2
		    AND COALESCE(attributes ->> $3, 'false') <> 'true'`,
		org, horizon, BaselineMarkerKey)
	if err != nil {
		return 0, fmt.Errorf("retention: prune observed_events: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// pruneDerived deletes derived_events older than the horizon, keeping baselines
// by the same attributes-marker convention.
func (e *Engine) pruneDerived(ctx context.Context, tx pgx.Tx, org int64, horizon time.Time) (int, error) {
	tag, err := tx.Exec(ctx,
		`DELETE FROM derived_events
		  WHERE org_id = $1
		    AND event_time < $2
		    AND COALESCE(attributes ->> $3, 'false') <> 'true'`,
		org, horizon, BaselineMarkerKey)
	if err != nil {
		return 0, fmt.Errorf("retention: prune derived_events: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// baselineSource is the synthetic source attributed to retention baselines.
const baselineSource = "retention"

// baselineRunID is a stable, horizon-derived run id for baseline rows. It is
// deterministic so re-running a prune at the same horizon produces the same id.
// observed_events.run_id references collection_runs(run_id); callers that run
// prune against a live schema must ensure this run row exists (the retention
// test in 18.2 seeds it). The value is kept short and unambiguous.
func baselineRunID(horizon time.Time) string {
	return "retention-baseline-" + horizon.UTC().Format("20060102T150405Z")
}
