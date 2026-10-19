//go:build integration

package temporal

import (
	"context"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// seedCollectionRun inserts a collection_runs row so Apply's observed_events
// INSERT (whose run_id is a FK into collection_runs) can commit. A successful
// Apply requires its run to exist; the failed-run scenario deliberately omits
// this to force a mid-write rollback.
func seedCollectionRun(ctx context.Context, t *testing.T, st *store.Store, runID, source string) {
	t.Helper()
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO collection_runs (run_id, source, status) VALUES ($1, $2, 'completed')`,
		runID, source); err != nil {
		t.Fatalf("seed collection_run %s: %v", runID, err)
	}
}

// countDeletionEvents returns how many entity.deleted observed_events rows exist
// for a given canonical_id in an org. Reference-counted deletion is observable
// precisely as the presence (or absence) of these rows in the facts log.
func countDeletionEvents(ctx context.Context, t *testing.T, st *store.Store, org int64, canonicalID string) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM observed_events
		 WHERE org_id = $1 AND canonical_id = $2 AND change_type = $3`,
		org, canonicalID, ChangeEntityDeleted).Scan(&n); err != nil {
		t.Fatalf("count entity.deleted events: %v", err)
	}
	return n
}

// countAssertionsFor returns the number of entity_assertions rows (across all
// sources) that still assert a canonical_id for an org. Zero means no source
// reports the entity any more.
func countAssertionsFor(ctx context.Context, t *testing.T, st *store.Store, org int64, canonicalID string) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM entity_assertions WHERE org_id = $1 AND canonical_id = $2`,
		org, canonicalID).Scan(&n); err != nil {
		t.Fatalf("count assertions for %s: %v", canonicalID, err)
	}
	return n
}

// assertResult builds a normalize.Result asserting a single DataStore entity by
// canonical_id, with one piece of raw-event evidence stamped at fixedEventTime
// so Apply classifies it as a writable (non-coalesced) change the first time.
func assertEntity(canonicalID, rawEventID string, public bool) normalize.Result {
	return normalize.Result{
		Entities: []model.Entity{{
			Kind:        model.Kind("DataStore"),
			CanonicalID: canonicalID,
			Attributes:  map[string]any{"region": "us-east-1", "public": public},
			Evidence: []model.Evidence{{
				RawEventID: rawEventID,
				ObservedAt: fixedEventTime,
			}},
		}},
	}
}

// TestReferenceCountedDeletion proves Property 3 (reference-counted deletion)
// end-to-end through the real Apply transaction (decrement + remaining-count +
// emit all in one tx):
//
//	Req 5.4 — while at least one source still asserts an entity, dropping ONE
//	          source must NOT delete it: no entity.deleted event, assertions
//	          remain.
//	Req 5.3 — dropping the LAST source leaves zero assertions across all
//	          sources, so entity.deleted IS emitted (the projection then marks
//	          the entity absent on replay).
//	Req 5.6 — a failed/aborted run retains that source's assertions and emits no
//	          reference-counted deletion.
//
// The shared entity is asserted by two sources ("sourceA", "sourceB") via
// separate Apply runs so the full transactional path is exercised with crafted
// normalize.Result inputs, exactly as production drives it.
func TestReferenceCountedDeletion(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	eng := NewEngine(st)

	const org = int64(42)
	const sourceA = "sourceA"
	const sourceB = "sourceB"
	const shared = "entity:aws:bucket-shared"

	// --- Setup: both sources assert the SAME entity via separate Apply runs. ---
	seedCollectionRun(ctx, t, st, "runA-1", sourceA)
	seedCollectionRun(ctx, t, st, "runB-1", sourceB)

	if _, err := eng.Apply(ctx, org, "runA-1", sourceA, assertEntity(shared, "raw-a1", false), nil); err != nil {
		t.Fatalf("sourceA initial assert: %v", err)
	}
	if _, err := eng.Apply(ctx, org, "runB-1", sourceB, assertEntity(shared, "raw-b1", false), nil); err != nil {
		t.Fatalf("sourceB initial assert: %v", err)
	}

	// Precondition: two assertion rows (one per source) and no deletion yet.
	if got := countAssertionsFor(ctx, t, st, org, shared); got != 2 {
		t.Fatalf("precondition: expected 2 assertions for shared entity, got %d", got)
	}
	if got := countDeletionEvents(ctx, t, st, org, shared); got != 0 {
		t.Fatalf("precondition: expected 0 entity.deleted events, got %d", got)
	}

	// --- (1) Drop ONE source: sourceA runs WITHOUT the shared entity. ---
	// Req 5.4: sourceB still asserts it, so NO deletion and the entity survives.
	seedCollectionRun(ctx, t, st, "runA-2", sourceA)
	changesA2, err := eng.Apply(ctx, org, "runA-2", sourceA, normalize.Result{}, nil)
	if err != nil {
		t.Fatalf("sourceA drop run: %v", err)
	}
	for _, c := range changesA2 {
		if c.ChangeType == ChangeEntityDeleted {
			t.Errorf("dropping one of two sources must not emit entity.deleted, got %+v", c)
		}
	}
	if got := countDeletionEvents(ctx, t, st, org, shared); got != 0 {
		t.Errorf("Req 5.4: no entity.deleted expected while sourceB still asserts, got %d", got)
	}
	if got := countAssertionsFor(ctx, t, st, org, shared); got != 1 {
		t.Errorf("Req 5.4: expected 1 remaining assertion (sourceB), got %d", got)
	}
	// The remaining assertion must be sourceB's.
	if got := assertionSource(ctx, t, st, org, shared); got != sourceB {
		t.Errorf("Req 5.4: remaining assertion should belong to sourceB, got %q", got)
	}

	// --- (2) Drop the OTHER source: sourceB runs WITHOUT the shared entity. ---
	// Req 5.3: zero assertions remain across all sources, so entity.deleted IS
	// emitted (and the projection marks the entity absent on replay / MarkStale).
	seedCollectionRun(ctx, t, st, "runB-2", sourceB)
	changesB2, err := eng.Apply(ctx, org, "runB-2", sourceB, normalize.Result{}, nil)
	if err != nil {
		t.Fatalf("sourceB drop run: %v", err)
	}
	sawDeleted := false
	for _, c := range changesB2 {
		if c.ChangeType == ChangeEntityDeleted && c.CanonicalID == shared {
			sawDeleted = true
		}
	}
	if !sawDeleted {
		t.Errorf("Req 5.3: dropping the last source must return an entity.deleted change, got %+v", changesB2)
	}
	if got := countDeletionEvents(ctx, t, st, org, shared); got != 1 {
		t.Errorf("Req 5.3: expected exactly one entity.deleted event written, got %d", got)
	}
	if got := countAssertionsFor(ctx, t, st, org, shared); got != 0 {
		t.Errorf("Req 5.3: expected zero remaining assertions for the deleted entity, got %d", got)
	}

	// --- (3) Failed run: a source's aborted run retains its assertions and ---
	// --- emits no reference-counted deletion (Req 5.6). ---
	// A fresh entity asserted by sourceA (committed), then an aborted drop run.
	const other = "entity:aws:bucket-other"
	seedCollectionRun(ctx, t, st, "runA-3", sourceA)
	if _, err := eng.Apply(ctx, org, "runA-3", sourceA, assertEntity(other, "raw-a3", false), nil); err != nil {
		t.Fatalf("sourceA assert other entity: %v", err)
	}
	if got := countAssertionsFor(ctx, t, st, org, other); got != 1 {
		t.Fatalf("precondition: expected 1 assertion for other entity, got %d", got)
	}

	beforeDeletions := countDeletionEvents(ctx, t, st, org, other)

	// Drive an aborted run the way production would fail mid-write: Apply with a
	// run_id that has NO collection_runs row. The run asserts a NEW entity (a
	// writable observed_events row) and drops `other`; the first observed_events
	// INSERT fails on the FK AFTER the per-org lock and the reference-counted
	// deletion reads/deletes have run inside the tx, so the whole transaction —
	// including the dropped assertion — must roll back.
	_, err = eng.Apply(ctx, org, "run-missing", sourceA, assertEntity("entity:aws:bucket-fresh", "raw-x", false), nil)
	if err == nil {
		t.Fatal("Req 5.6: expected the aborted run to return an error, got nil")
	}

	// Req 5.6: sourceA's assertion for `other` is retained (rolled back drop).
	if got := countAssertionsFor(ctx, t, st, org, other); got != 1 {
		t.Errorf("Req 5.6: failed run must retain the source's assertion, got %d assertions for %s", got, other)
	}
	// Req 5.6: no reference-counted deletion was emitted by the aborted run.
	if got := countDeletionEvents(ctx, t, st, org, other); got != beforeDeletions {
		t.Errorf("Req 5.6: failed run must emit no entity.deleted, deletions went %d -> %d", beforeDeletions, got)
	}
	if got := countDeletionEvents(ctx, t, st, org, "entity:aws:bucket-fresh"); got != 0 {
		t.Errorf("Req 5.6: aborted run must persist no events for its new entity, got %d", got)
	}
}

// assertionSource returns the single remaining source asserting a canonical_id.
// It fails the test if there is not exactly one.
func assertionSource(ctx context.Context, t *testing.T, st *store.Store, org int64, canonicalID string) string {
	t.Helper()
	rows, err := st.Pool().Query(ctx,
		`SELECT source FROM entity_assertions WHERE org_id = $1 AND canonical_id = $2`,
		org, canonicalID)
	if err != nil {
		t.Fatalf("select assertion source: %v", err)
	}
	defer rows.Close()
	var sources []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan assertion source: %v", err)
		}
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate assertion sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected exactly one remaining assertion source, got %d: %v", len(sources), sources)
	}
	return sources[0]
}
