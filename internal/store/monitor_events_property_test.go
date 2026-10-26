//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// This file carries the property-based test for the monitor_events ingest path.
// It is gated behind the `integration` build tag because InsertMonitorEvent
// needs a real Postgres (the unique index on (org_id, source, delivery_id) IS
// the dedup mechanism), so it reuses the testcontainers harness newTestStore
// defined in store_integration_test.go. Run it with a container runtime:
//
//	go test -tags integration ./internal/store/... -run TestMonitorEventIngestIdempotency
//
// Without a container runtime it is still expected to compile under the tag
// (go vet -tags integration ./internal/store/...).

// seedOrg inserts one organizations row directly and returns its id. The store
// package test has no auth seed (that catalog lives in internal/auth), and
// monitor_events.org_id is an FK to organizations(id), so a bare org row is the
// minimal fixture the insert needs. Org is always supplied by the caller and
// never read from the payload, so a single seeded org is enough to exercise the
// idempotency key.
func seedOrg(t *testing.T, ctx context.Context, st *Store, name string) int64 {
	t.Helper()
	var id int64
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed org %q: %v", name, err)
	}
	return id
}

// countEventsForKey returns how many monitor_events rows exist for the
// idempotency key (org, source, delivery_id). The property asserts this is
// EXACTLY 1 after any number of repeated inserts of the same key.
func countEventsForKey(t *testing.T, ctx context.Context, st *Store, org int64, source, deliveryID string) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM monitor_events
		  WHERE org_id = $1 AND source = $2 AND delivery_id = $3`,
		org, source, deliveryID).Scan(&n); err != nil {
		t.Fatalf("count events for key (%d,%s,%s): %v", org, source, deliveryID, err)
	}
	return n
}

// TestMonitorEventIngestIdempotency is the property test for
//
//	Property 2: Ingest idempotency
//	Validates: Requirements 2.1, 2.2
//
// The property: for a generated (org, source, delivery_id, event_kind, payload),
// calling InsertMonitorEvent N>=2 times with the same (org, source,
// delivery_id):
//   - leaves EXACTLY ONE row for that key,
//   - the FIRST call returns a fresh id with no error,
//   - EVERY subsequent call returns ErrDuplicateMonitorEvent and creates no row.
//
// Source/kind/payload and N are varied across generated cases via a randomized
// table loop (no new dependency: pgregory.net/rapid is not in go.mod, so this
// uses math/rand over a fixed seed for reproducibility). The row count for the
// key is asserted to be exactly 1 after the repeated inserts of each case.
func TestMonitorEventIngestIdempotency(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// One org is enough: the idempotency key is scoped per org and org is a
	// caller-supplied constant per case, so a single seeded org exercises the
	// (org, source, delivery_id) uniqueness fully.
	org := seedOrg(t, ctx, st, "prop-org")

	// Generators over the valid input space. source and event_kind are drawn
	// from the CHECK-constrained vocabularies the migration allows so inserts
	// exercise real rows rather than tripping an unrelated constraint.
	sources := []string{"github", "correlate", "discovery", "sweep"}
	kinds := []string{"ci", "vulnerability", "code_change", "resource"}
	payloads := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"a":1}`),
		json.RawMessage(`{"nested":{"k":"v"},"arr":[1,2,3]}`),
		json.RawMessage(`{"canonical_id":"repo:github:octo/app","severity":"high"}`),
	}

	// Fixed seed: deterministic case generation so a failure is reproducible.
	rng := rand.New(rand.NewSource(0xBE11))

	const cases = 60
	for c := 0; c < cases; c++ {
		source := sources[rng.Intn(len(sources))]
		kind := kinds[rng.Intn(len(kinds))]
		payload := payloads[rng.Intn(len(payloads))]
		// delivery_id is unique per case so cases do not alias each other; the
		// repetition WITHIN a case is what exercises the dedup.
		deliveryID := fmt.Sprintf("del-%d-%s", c, source)
		// N in [2, 6]: at least two inserts so there is always a duplicate to
		// reject, varied so the "every subsequent call dedups" arm is stressed
		// across different repeat counts.
		n := 2 + rng.Intn(5)

		in := MonitorEventInput{
			Source:     source,
			DeliveryID: deliveryID,
			EventKind:  kind,
			Payload:    payload,
		}

		// First insert: a fresh id, no error (Req 2.1).
		firstID, err := st.InsertMonitorEvent(ctx, org, in)
		if err != nil {
			t.Fatalf("case %d (%s/%s/%s): first insert errored: %v", c, source, kind, deliveryID, err)
		}
		if firstID == 0 {
			t.Fatalf("case %d (%s/%s/%s): first insert returned zero id", c, source, kind, deliveryID)
		}

		// Every subsequent insert of the same key: ErrDuplicateMonitorEvent,
		// zero id, and NO new row (Req 2.2).
		for i := 1; i < n; i++ {
			dupID, dupErr := st.InsertMonitorEvent(ctx, org, in)
			if !errors.Is(dupErr, ErrDuplicateMonitorEvent) {
				t.Fatalf("case %d (%s/%s/%s): insert #%d err = %v, want ErrDuplicateMonitorEvent",
					c, source, kind, deliveryID, i+1, dupErr)
			}
			if dupID != 0 {
				t.Fatalf("case %d (%s/%s/%s): duplicate insert #%d returned non-zero id %d",
					c, source, kind, deliveryID, i+1, dupID)
			}
			// No row was created by the duplicate: the count never grows past 1.
			if got := countEventsForKey(t, ctx, st, org, source, deliveryID); got != 1 {
				t.Fatalf("case %d (%s/%s/%s): after duplicate insert #%d row count = %d, want 1",
					c, source, kind, deliveryID, i+1, got)
			}
		}

		// The invariant: exactly one row exists for the key after N inserts.
		if got := countEventsForKey(t, ctx, st, org, source, deliveryID); got != 1 {
			t.Fatalf("case %d (%s/%s/%s): final row count = %d after %d inserts, want exactly 1",
				c, source, kind, deliveryID, got, n)
		}
	}
}
