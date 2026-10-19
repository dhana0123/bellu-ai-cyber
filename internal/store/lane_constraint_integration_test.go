//go:build integration

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Property 2: facts/derived separation.
//
// The observed_events (facts) lane and the derived_events lane are kept strictly
// separate by CHECK constraints on change_type: observed_events admits only the
// entity.*/relation.* change types, and derived_events admits only the
// inferred.*/classified/reclassified/declassified change types. These tests
// apply migrations 00015 and 00016 to a disposable Postgres and assert that each
// lane rejects the other lane's change types while accepting its own.
//
// Validates: Requirements 4.1, 4.2

const (
	laneTestOrgID = int64(1)
	laneTestRunID = "run-lane"
)

// isCheckViolation reports whether err is a Postgres check_violation (23514)
// and, when a constraint name is given, that the violated constraint matches.
func isCheckViolation(err error, constraint string) bool {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" {
		return false
	}
	return constraint == "" || pg.ConstraintName == constraint
}

// setupLaneRun creates the collection_runs row the two lane tables reference by
// FK so inserts fail on the lane CHECK rather than the run FK.
func setupLaneRun(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.StartRun(ctx, laneTestRunID, "fixture"); err != nil {
		t.Fatalf("start run: %v", err)
	}
}

// TestObservedLaneRejectsDerivedChangeType asserts observed_events rejects a
// derived-lane change type ('classified') via its change_type CHECK, and
// accepts a valid observed change type ('entity.created'). (Requirement 4.1)
func TestObservedLaneRejectsDerivedChangeType(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	setupLaneRun(t, st)

	// A derived change type must be rejected by the observed_events CHECK.
	_, err := st.Pool().Exec(ctx, `
		INSERT INTO observed_events
			(org_id, run_id, source, element_kind, change_type, canonical_id, event_time)
		VALUES ($1, $2, 'cloudquery', 'entity', 'classified', 'entity:a', now())`,
		laneTestOrgID, laneTestRunID)
	if err == nil {
		t.Fatal("expected observed_events to reject derived change_type 'classified', got nil error")
	}
	if !isCheckViolation(err, "observed_events_change_type_chk") {
		t.Fatalf("expected check_violation on observed_events_change_type_chk, got %v", err)
	}

	// A valid observed change type must insert successfully.
	if _, err := st.Pool().Exec(ctx, `
		INSERT INTO observed_events
			(org_id, run_id, source, element_kind, change_type, canonical_id, event_time)
		VALUES ($1, $2, 'cloudquery', 'entity', 'entity.created', 'entity:a', now())`,
		laneTestOrgID, laneTestRunID); err != nil {
		t.Fatalf("expected valid observed change_type 'entity.created' to insert, got %v", err)
	}

	var count int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM observed_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 observed_events row (the valid insert), got %d", count)
	}
}

// TestDerivedLaneRejectsObservedChangeType asserts derived_events rejects an
// observed-lane change type ('entity.created') via its change_type CHECK, and
// accepts a valid derived change type ('classified'). (Requirement 4.2)
func TestDerivedLaneRejectsObservedChangeType(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	setupLaneRun(t, st)

	// An observed change type must be rejected by the derived_events CHECK.
	_, err := st.Pool().Exec(ctx, `
		INSERT INTO derived_events
			(org_id, run_id, kind, change_type, origin, canonical_id, event_time)
		VALUES ($1, $2, 'classification', 'entity.created', 'rule', 'entity:a', now())`,
		laneTestOrgID, laneTestRunID)
	if err == nil {
		t.Fatal("expected derived_events to reject observed change_type 'entity.created', got nil error")
	}
	if !isCheckViolation(err, "derived_events_change_type_chk") {
		t.Fatalf("expected check_violation on derived_events_change_type_chk, got %v", err)
	}

	// A valid derived classification row must insert successfully. kind
	// 'classification' requires relation/dst_id to be NULL (shape CHECK).
	if _, err := st.Pool().Exec(ctx, `
		INSERT INTO derived_events
			(org_id, run_id, kind, change_type, origin, canonical_id, event_time)
		VALUES ($1, $2, 'classification', 'classified', 'rule', 'entity:a', now())`,
		laneTestOrgID, laneTestRunID); err != nil {
		t.Fatalf("expected valid derived change_type 'classified' to insert, got %v", err)
	}

	// A valid derived inferred_edge row must insert successfully. kind
	// 'inferred_edge' requires relation and dst_id to be set (shape CHECK).
	if _, err := st.Pool().Exec(ctx, `
		INSERT INTO derived_events
			(org_id, run_id, kind, change_type, origin, canonical_id, relation, dst_id, event_time)
		VALUES ($1, $2, 'inferred_edge', 'inferred.added', 'rule', 'entity:a', 'DEPENDS_ON', 'entity:b', now())`,
		laneTestOrgID, laneTestRunID); err != nil {
		t.Fatalf("expected valid derived change_type 'inferred.added' to insert, got %v", err)
	}

	var count int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM derived_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected exactly 2 derived_events rows (the valid inserts), got %d", count)
	}
}
