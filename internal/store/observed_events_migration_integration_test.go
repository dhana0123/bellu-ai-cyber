//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// TestObservedEventsMigrationUp asserts the 00015_observed_events migration
// creates the observed_events and entity_assertions tables with every column,
// both CHECK constraints, the FK to collection_runs, and all indexes. The
// newTestStore helper (store_integration_test.go) runs Store.Migrate, which
// applies every embedded migration including 00015.
func TestObservedEventsMigrationUp(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// Both tables exist.
	for _, tbl := range []string{"observed_events", "entity_assertions"} {
		if !tableExists(ctx, t, st, tbl) {
			t.Errorf("expected table %s to exist after migration", tbl)
		}
	}

	// observed_events columns.
	wantObservedCols := []string{
		"seq", "org_id", "run_id", "source", "element_kind", "change_type",
		"canonical_id", "relation", "dst_id", "attributes", "diff",
		"confidence", "evidence_ids", "event_time", "recorded_at",
	}
	for _, col := range wantObservedCols {
		if !columnExists(ctx, t, st, "observed_events", col) {
			t.Errorf("observed_events missing column %s", col)
		}
	}

	// entity_assertions columns.
	wantAssertionCols := []string{
		"org_id", "canonical_id", "source", "last_run_id", "last_seen",
	}
	for _, col := range wantAssertionCols {
		if !columnExists(ctx, t, st, "entity_assertions", col) {
			t.Errorf("entity_assertions missing column %s", col)
		}
	}

	// CHECK constraints: change_type restriction and edge-shape.
	wantChecks := []string{
		"observed_events_change_type_chk",
		"observed_events_edge_shape_chk",
	}
	for _, c := range wantChecks {
		if !checkConstraintExists(ctx, t, st, "observed_events", c) {
			t.Errorf("observed_events missing CHECK constraint %s", c)
		}
	}

	// FK from observed_events.run_id -> collection_runs.
	fks := foreignKeys(ctx, t, st, "observed_events")
	if fks["run_id"] != "collection_runs" {
		t.Errorf("observed_events.run_id FK target = %q, want collection_runs", fks["run_id"])
	}

	// Indexes.
	wantObservedIdx := []string{
		"observed_events_event_time_idx",
		"observed_events_recorded_idx",
		"observed_events_entity_idx",
		"observed_events_edge_idx",
		"observed_events_run_idx",
	}
	for _, idx := range wantObservedIdx {
		if !indexExists(ctx, t, st, idx) {
			t.Errorf("expected index %s on observed_events", idx)
		}
	}
	if !indexExists(ctx, t, st, "entity_assertions_entity_idx") {
		t.Errorf("expected index entity_assertions_entity_idx on entity_assertions")
	}
}

// TestObservedEventsChangeTypeCheckEnforced asserts the change_type CHECK
// rejects a value outside the allowed observed-lane set and accepts one inside
// it, proving the constraint is live (not just present in the catalog).
func TestObservedEventsChangeTypeCheckEnforced(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// A run row is required for the FK.
	if err := st.StartRun(ctx, "run-oe", "fixture"); err != nil {
		t.Fatal(err)
	}

	// Rejected: a derived-lane change type must violate the observed CHECK.
	_, err := st.Pool().Exec(ctx,
		`INSERT INTO observed_events (org_id, run_id, source, element_kind, change_type, canonical_id, event_time)
		 VALUES (1, 'run-oe', 'fixture', 'entity', 'classified', 'cid-1', now())`)
	if err == nil {
		t.Errorf("expected change_type CHECK to reject 'classified', insert succeeded")
	}

	// Accepted: a valid observed-lane entity change type.
	_, err = st.Pool().Exec(ctx,
		`INSERT INTO observed_events (org_id, run_id, source, element_kind, change_type, canonical_id, event_time)
		 VALUES (1, 'run-oe', 'fixture', 'entity', 'entity.created', 'cid-1', now())`)
	if err != nil {
		t.Errorf("expected valid change_type 'entity.created' to insert, got %v", err)
	}
}

// TestObservedEventsEdgeShapeCheckEnforced asserts the edge-shape CHECK forces
// edge rows to carry relation+dst_id and entity rows to omit them.
func TestObservedEventsEdgeShapeCheckEnforced(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.StartRun(ctx, "run-oe2", "fixture"); err != nil {
		t.Fatal(err)
	}

	// Rejected: an edge row missing relation/dst_id.
	_, err := st.Pool().Exec(ctx,
		`INSERT INTO observed_events (org_id, run_id, source, element_kind, change_type, canonical_id, event_time)
		 VALUES (1, 'run-oe2', 'fixture', 'edge', 'relation.added', 'src-1', now())`)
	if err == nil {
		t.Errorf("expected edge-shape CHECK to reject an edge row without relation/dst_id")
	}

	// Accepted: a well-formed edge row.
	_, err = st.Pool().Exec(ctx,
		`INSERT INTO observed_events (org_id, run_id, source, element_kind, change_type, canonical_id, relation, dst_id, event_time)
		 VALUES (1, 'run-oe2', 'fixture', 'edge', 'relation.added', 'src-1', 'CALLS', 'dst-1', now())`)
	if err != nil {
		t.Errorf("expected well-formed edge row to insert, got %v", err)
	}
}

// TestObservedEventsMigrationDown asserts the 00015 Down reverses cleanly:
// stepping the schema back below 00015 drops both tables it created. Because
// 00016_derived_events sits on top of 00015, we migrate down to version 14,
// which runs 00016 Down then 00015 Down in order.
func TestObservedEventsMigrationDown(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// Precondition: tables exist after the full Up migration.
	for _, tbl := range []string{"observed_events", "entity_assertions"} {
		if !tableExists(ctx, t, st, tbl) {
			t.Fatalf("precondition: table %s should exist before Down", tbl)
		}
	}

	db := stdlib.OpenDBFromPool(st.Pool())
	defer db.Close()
	goose.SetBaseFS(MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	// Reverse everything above 00014: this runs 00016 Down then 00015 Down.
	if err := goose.DownToContext(ctx, db, "migrations", 14); err != nil {
		t.Fatalf("migrate down to 14: %v", err)
	}

	// Both tables created by 00015 are gone.
	for _, tbl := range []string{"observed_events", "entity_assertions"} {
		if tableExists(ctx, t, st, tbl) {
			t.Errorf("expected table %s to be dropped after Down", tbl)
		}
	}

	// Re-applying Up restores both tables, proving the Down/Up cycle is clean.
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		t.Fatalf("re-migrate up: %v", err)
	}
	for _, tbl := range []string{"observed_events", "entity_assertions"} {
		if !tableExists(ctx, t, st, tbl) {
			t.Errorf("expected table %s to be recreated after re-Up", tbl)
		}
	}
}

// --- schema introspection helpers ---

func tableExists(ctx context.Context, t *testing.T, st *Store, name string) bool {
	t.Helper()
	var exists bool
	err := st.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		  WHERE table_schema = 'public' AND table_name = $1)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return exists
}

func columnExists(ctx context.Context, t *testing.T, st *Store, table, col string) bool {
	t.Helper()
	var exists bool
	err := st.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		  WHERE table_name = $1 AND column_name = $2)`, table, col).Scan(&exists)
	if err != nil {
		t.Fatalf("check column %s.%s: %v", table, col, err)
	}
	return exists
}

func checkConstraintExists(ctx context.Context, t *testing.T, st *Store, table, name string) bool {
	t.Helper()
	var exists bool
	err := st.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.table_constraints
		  WHERE table_name = $1 AND constraint_name = $2 AND constraint_type = 'CHECK')`,
		table, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check constraint %s on %s: %v", name, table, err)
	}
	return exists
}

func indexExists(ctx context.Context, t *testing.T, st *Store, name string) bool {
	t.Helper()
	var exists bool
	err := st.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes
		  WHERE schemaname = 'public' AND indexname = $1)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check index %s: %v", name, err)
	}
	return exists
}

func foreignKeys(ctx context.Context, t *testing.T, st *Store, table string) map[string]string {
	t.Helper()
	rows, err := st.Pool().Query(ctx, `
		SELECT kcu.column_name, ccu.table_name
		  FROM information_schema.table_constraints tc
		  JOIN information_schema.key_column_usage kcu
		    ON tc.constraint_name = kcu.constraint_name
		  JOIN information_schema.constraint_column_usage ccu
		    ON tc.constraint_name = ccu.constraint_name
		 WHERE tc.constraint_type = 'FOREIGN KEY'
		   AND tc.table_name = $1`, table)
	if err != nil {
		t.Fatalf("query fks for %s: %v", table, err)
	}
	defer rows.Close()
	fks := map[string]string{}
	for rows.Next() {
		var col, target string
		if err := rows.Scan(&col, &target); err != nil {
			t.Fatalf("scan fk: %v", err)
		}
		fks[col] = target
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("fk rows: %v", err)
	}
	return fks
}
