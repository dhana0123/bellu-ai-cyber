//go:build integration

package temporal

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// TestRetentionAsOfAfterPruneResolvesFromBaseline proves Req 11.2 and 11.4: a
// retention prune writes a full-snapshot baseline at the horizon BEFORE deleting
// aged rows, so an as-of query targeting a time AFTER the pruned window still
// resolves correct state from the retained baseline — even though the original
// pre-horizon observed_events that produced that state have been pruned.
//
// The design's as-of fold (design.md, "Temporal query predicates" and Property
// 4) is: fold the observed events with event_time <= T ordered by
// (event_time, recorded_at, seq), where a non-deleted change replaces state and
// a terminal entity.deleted means absent. Task 12.1's Querier.StateAt is not yet
// present in this package, so this test folds the observed lane directly via SQL
// mirroring that design fold. When querier.go lands, this fold can be replaced
// by Querier.StateAt without changing the assertions.
func TestRetentionAsOfAfterPruneResolvesFromBaseline(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	eng := NewEngine(st)

	const (
		org    = int64(42)
		source = "fixture"
		runID  = "run-seed"
		entity = "entity:aws:bucket-retained"
	)

	// A real collection_runs row is required by observed_events.run_id's FK.
	if err := st.StartRun(ctx, runID, source); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Seed observed_events for one entity across several event_times. The entity
	// is created, then its attributes are updated twice. The last pre-horizon
	// state (public=true, tier=gold) is what the horizon baseline must capture.
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	seedObserved(ctx, t, st, org, runID, source, seedRow{
		changeType: ChangeEntityCreated,
		canonical:  entity,
		attrs:      map[string]any{"region": "us-east-1", "public": false, "tier": "bronze"},
		eventTime:  base,
	})
	seedObserved(ctx, t, st, org, runID, source, seedRow{
		changeType: ChangeEntityAttributeUpdated,
		canonical:  entity,
		attrs:      map[string]any{"region": "us-east-1", "public": false, "tier": "silver"},
		eventTime:  base.Add(24 * time.Hour),
	})
	seedObserved(ctx, t, st, org, runID, source, seedRow{
		changeType: ChangeEntityAttributeUpdated,
		canonical:  entity,
		attrs:      map[string]any{"region": "us-east-1", "public": true, "tier": "gold"},
		eventTime:  base.Add(48 * time.Hour),
	})

	// Precondition: three non-baseline observed rows exist for this entity.
	if n := countEntityRows(ctx, t, st, org, entity); n != 3 {
		t.Fatalf("precondition: expected 3 seeded observed rows, got %d", n)
	}

	// Prune at a horizon AFTER all three seeded events, so every seeded row is
	// strictly older than the horizon and gets pruned, while a baseline folding
	// the state as of the horizon is written first.
	horizon := base.Add(72 * time.Hour)
	baselines, prunedObserved, _, err := eng.Prune(ctx, org, RetentionConfig{Horizon: horizon})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if baselines != 1 {
		t.Fatalf("expected exactly 1 baseline written, got %d", baselines)
	}
	if prunedObserved != 3 {
		t.Fatalf("expected 3 pre-horizon observed rows pruned, got %d", prunedObserved)
	}

	// All three original non-baseline rows were pruned; only the baseline remains.
	nonBaseline, baseline := countEntityRowsByKind(ctx, t, st, org, entity)
	if nonBaseline != 0 {
		t.Errorf("expected 0 non-baseline rows after prune, got %d", nonBaseline)
	}
	if baseline != 1 {
		t.Errorf("expected 1 retained baseline row after prune, got %d", baseline)
	}

	// The baseline is a full-snapshot entity.attribute_updated at event_time=horizon.
	bt, bct := baselineRowShape(ctx, t, st, org, entity)
	if !bt.Equal(horizon) {
		t.Errorf("baseline event_time = %v, want horizon %v", bt, horizon)
	}
	if bct != ChangeEntityAttributeUpdated {
		t.Errorf("baseline change_type = %q, want %q", bct, ChangeEntityAttributeUpdated)
	}

	// As-of a time AFTER the pruned window: the fold sees only the retained
	// baseline, and it must reconstruct the last pre-horizon state (Req 11.4).
	asOf := horizon.Add(24 * time.Hour)
	state, live := foldStateAt(ctx, t, st, org, entity, asOf)
	if !live {
		t.Fatalf("entity should be live as-of %v, resolved as absent", asOf)
	}
	if got := state["public"]; got != true {
		t.Errorf("as-of public = %v, want true (from baseline)", got)
	}
	if got := state["tier"]; got != "gold" {
		t.Errorf("as-of tier = %v, want gold (from baseline)", got)
	}
	if got := state["region"]; got != "us-east-1" {
		t.Errorf("as-of region = %v, want us-east-1 (from baseline)", got)
	}
	// The baseline marker is an internal retention artifact; it rides in the
	// snapshot but is not provider state. Its presence confirms the resolved
	// state came from the retained baseline rather than a surviving raw row.
	if !IsBaselineAttrs(state) {
		t.Errorf("resolved state should carry the baseline marker, proving it came from the baseline: %#v", state)
	}
}

// seedRow is one observed_events row to seed with an explicit event_time.
type seedRow struct {
	changeType string
	canonical  string
	attrs      map[string]any
	eventTime  time.Time
}

// seedObserved inserts a single entity observed_events row directly, so the test
// controls event_time precisely (the change engine derives event_time from
// evidence, which is less direct for seeding a multi-time history).
func seedObserved(ctx context.Context, t *testing.T, st *store.Store, org int64, runID, source string, r seedRow) {
	t.Helper()
	attrs, err := json.Marshal(r.attrs)
	if err != nil {
		t.Fatalf("marshal attrs: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO observed_events
		   (org_id, run_id, source, element_kind, change_type, canonical_id,
		    attributes, diff, evidence_ids, event_time, recorded_at)
		 VALUES ($1,$2,$3,'entity',$4,$5,$6,'{}'::jsonb,'{}',$7,now())`,
		org, runID, source, r.changeType, r.canonical, attrs, r.eventTime); err != nil {
		t.Fatalf("seed observed row: %v", err)
	}
}

// countEntityRows returns the total observed_events rows for an entity.
func countEntityRows(ctx context.Context, t *testing.T, st *store.Store, org int64, canonical string) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM observed_events WHERE org_id = $1 AND canonical_id = $2`,
		org, canonical).Scan(&n); err != nil {
		t.Fatalf("count entity rows: %v", err)
	}
	return n
}

// countEntityRowsByKind splits an entity's observed_events into non-baseline and
// baseline counts using the attributes-JSONB baseline marker.
func countEntityRowsByKind(ctx context.Context, t *testing.T, st *store.Store, org int64, canonical string) (nonBaseline, baseline int) {
	t.Helper()
	if err := st.Pool().QueryRow(ctx,
		`SELECT
		   count(*) FILTER (WHERE COALESCE(attributes ->> $3, 'false') <> 'true'),
		   count(*) FILTER (WHERE COALESCE(attributes ->> $3, 'false')  = 'true')
		 FROM observed_events WHERE org_id = $1 AND canonical_id = $2`,
		org, canonical, BaselineMarkerKey).Scan(&nonBaseline, &baseline); err != nil {
		t.Fatalf("count entity rows by kind: %v", err)
	}
	return nonBaseline, baseline
}

// baselineRowShape returns the event_time and change_type of the (single)
// retained baseline row for an entity.
func baselineRowShape(ctx context.Context, t *testing.T, st *store.Store, org int64, canonical string) (time.Time, string) {
	t.Helper()
	var (
		et time.Time
		ct string
	)
	if err := st.Pool().QueryRow(ctx,
		`SELECT event_time, change_type FROM observed_events
		  WHERE org_id = $1 AND canonical_id = $2
		    AND COALESCE(attributes ->> $3, 'false') = 'true'`,
		org, canonical, BaselineMarkerKey).Scan(&et, &ct); err != nil {
		t.Fatalf("load baseline row: %v", err)
	}
	return et.UTC(), ct
}

// foldStateAt mirrors the design's as-of fold (Property 4): it folds the entity
// observed lane over event_time <= asOf, ordered by (event_time, recorded_at,
// seq); a non-deleted change replaces the attribute snapshot, a terminal
// entity.deleted resolves the entity as absent. It returns the folded attributes
// and whether the entity is live. (Replace with Querier.StateAt once task 12.1
// lands; the ordering and deleted-is-absent semantics are identical.)
func foldStateAt(ctx context.Context, t *testing.T, st *store.Store, org int64, canonical string, asOf time.Time) (map[string]any, bool) {
	t.Helper()
	rows, err := st.Pool().Query(ctx,
		`SELECT change_type, attributes FROM observed_events
		  WHERE org_id = $1 AND canonical_id = $2 AND element_kind = 'entity'
		    AND event_time <= $3
		  ORDER BY event_time ASC, recorded_at ASC, seq ASC`,
		org, canonical, asOf)
	if err != nil {
		t.Fatalf("fold query: %v", err)
	}
	defer rows.Close()

	var (
		attrs map[string]any
		live  bool
	)
	for rows.Next() {
		var (
			changeType string
			raw        []byte
		)
		if err := rows.Scan(&changeType, &raw); err != nil {
			t.Fatalf("scan fold row: %v", err)
		}
		if changeType == ChangeEntityDeleted {
			attrs = nil
			live = false
			continue
		}
		attrs = decodeAttrs(raw)
		live = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate fold rows: %v", err)
	}
	return attrs, live
}
