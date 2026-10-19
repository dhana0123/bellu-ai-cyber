//go:build integration

package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// newTestStore spins up a disposable Postgres and returns a migrated store.
// It mirrors the store package's integration test helper so the temporal
// change engine can be driven against a real transaction (rollback can only be
// proven against a real DB).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bellu"),
		tcpostgres.WithUsername("bellu"),
		tcpostgres.WithPassword("bellu"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// countObservedEvents returns the number of observed_events rows for an org.
func countObservedEvents(ctx context.Context, t *testing.T, st *store.Store, org int64) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM observed_events WHERE org_id = $1`, org).Scan(&n); err != nil {
		t.Fatalf("count observed_events: %v", err)
	}
	return n
}

// snapshotAssertions returns the entity_assertions rows for an org keyed by
// (canonical_id, source) with their last_run_id, so a before/after comparison
// can prove the set was left untouched by a rolled-back Apply.
func snapshotAssertions(ctx context.Context, t *testing.T, st *store.Store, org int64) map[string]string {
	t.Helper()
	rows, err := st.Pool().Query(ctx,
		`SELECT canonical_id, source, last_run_id FROM entity_assertions WHERE org_id = $1`, org)
	if err != nil {
		t.Fatalf("select entity_assertions: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var cid, src, runID string
		if err := rows.Scan(&cid, &src, &runID); err != nil {
			t.Fatalf("scan entity_assertions: %v", err)
		}
		out[cid+"|"+src] = runID
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate entity_assertions: %v", err)
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestApplyRollsBackEntireTransactionOnMidWriteFailure proves Req 3.7/3.8: when
// a write inside the run's change transaction fails mid-way, Apply rolls the
// whole transaction back (no partial observed_events persist, entity_assertions
// is left exactly as it was) and returns the error so the caller never proceeds
// to projection.
//
// The failure is injected the way production would hit it: observed_events.run_id
// is a FK to collection_runs(run_id). By calling Apply with a run_id that has NO
// collection_runs row, the first INSERT INTO observed_events fails with a FK
// violation after the transaction has begun, acquired the per-org advisory lock,
// and done its reference-counted-deletion reads — i.e. a genuine mid-write
// failure, not a pre-flight rejection.
func TestApplyRollsBackEntireTransactionOnMidWriteFailure(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	eng := NewEngine(st)

	const org = int64(7)
	const source = "fixture"

	// Seed a baseline entity_assertions row from a PRIOR, committed run for this
	// source. After the failed Apply we assert this row is still present and
	// unchanged — proving the rollback did not drop or rewrite assertions.
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO entity_assertions (org_id, canonical_id, source, last_run_id, last_seen)
		 VALUES ($1, $2, $3, $4, now())`,
		org, "entity:aws:bucket-prior", source, "run-prior"); err != nil {
		t.Fatalf("seed assertion: %v", err)
	}

	before := snapshotAssertions(ctx, t, st, org)
	if len(before) != 1 {
		t.Fatalf("precondition: expected 1 seeded assertion, got %d", len(before))
	}

	// A run that asserts a NEW entity (so classifyChanges yields a writable
	// entity.created row) and, because it does not re-assert bucket-prior, also
	// drives a reference-counted deletion. Both must be rolled back.
	res := normalize.Result{
		Entities: []model.Entity{{
			Kind:        model.Kind("DataStore"),
			CanonicalID: "entity:aws:bucket-new",
			Attributes:  map[string]any{"region": "us-east-1", "public": false},
			Evidence: []model.Evidence{{
				RawEventID: "raw-1",
				ObservedAt: fixedEventTime,
			}},
		}},
	}

	// run-missing has NO collection_runs row, so the observed_events FK fails on
	// the first write inside the transaction.
	_, err := eng.Apply(ctx, org, "run-missing", source, res, nil)
	if err == nil {
		t.Fatal("expected Apply to return an error on mid-write FK violation, got nil")
	}

	// No partial observed_events persisted: the whole write set rolled back.
	if n := countObservedEvents(ctx, t, st, org); n != 0 {
		t.Errorf("expected 0 observed_events after rollback, got %d", n)
	}

	// entity_assertions is unchanged: the deletion of bucket-prior was rolled
	// back and no new assertion for bucket-new was committed.
	after := snapshotAssertions(ctx, t, st, org)
	if !mapsEqual(before, after) {
		t.Errorf("entity_assertions changed across a rolled-back Apply:\n before=%v\n after =%v", before, after)
	}
	if _, ok := after["entity:aws:bucket-new|"+source]; ok {
		t.Errorf("rolled-back Apply must not persist the new entity's assertion")
	}
	if runID := after["entity:aws:bucket-prior|"+source]; runID != "run-prior" {
		t.Errorf("prior assertion mutated by rolled-back Apply: last_run_id = %q, want run-prior", runID)
	}
}
