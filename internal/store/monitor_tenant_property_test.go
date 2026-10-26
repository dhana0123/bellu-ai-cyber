//go:build integration

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
)

// This file carries the property-based test for Property 1 (Tenant isolation)
// on the monitor_events store slice. It is gated behind the `integration` build
// tag because the store slice (InsertMonitorEvent, SetMonitorEventRoute,
// ClaimPendingMonitorEvents, ListMonitorEvents) runs its org predicate in real
// SQL — the isolation IS the WHERE org_id = $1 clause plus the per-org unique
// index on (org_id, source, delivery_id) — so it needs a real Postgres. It
// reuses the testcontainers harness newTestStore defined in
// store_integration_test.go. Run it with a container runtime:
//
//	go test -tags integration ./internal/store/... -run TestMonitorEventTenantIsolation
//
// Without a container runtime it is still expected to compile under the tag
// (go vet -tags integration ./internal/store/...).
//
// Helper names here are deliberately prefixed `tenant...` and the seed helper is
// `tenantSeedOrg` (distinct from seedOrg in monitor_events_property_test.go) so
// this file and the property-test files created by tasks 2.2/2.4 can all live in
// the same `integration` package build without duplicate-symbol clashes.

// tenantSeedOrg inserts one organizations row directly and returns its id. The
// store package test has no auth seed (that catalog lives in internal/auth), and
// monitor_events.org_id is an FK to organizations(id), so a bare org row is the
// minimal fixture the slice needs. Org is always supplied by the caller and
// never read from the payload, so two distinct seeded orgs are all that Property
// 1 needs to prove cross-tenant invisibility.
func tenantSeedOrg(t *testing.T, ctx context.Context, st *Store, name string) int64 {
	t.Helper()
	var id int64
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed org %q: %v", name, err)
	}
	return id
}

// tenantEvent is one generated event for a single org. source/kind/payload are
// drawn from the migration's CHECK-constrained vocabularies so each row is a
// real insert rather than a trip over an unrelated constraint.
type tenantEvent struct {
	source     string
	deliveryID string
	kind       string
	payload    json.RawMessage
}

// tenantInsertRouted inserts every event for org and advances it to `routed`
// (so it is claimable). It returns the set of created row ids so the test can
// assert cross-org reads never surface them. A routed row needs no agent_run:
// run_id is nullable and only stamped at claim, so the store slice is fully
// exercisable without the run/launch machinery.
func tenantInsertRouted(t *testing.T, ctx context.Context, st *Store, org int64, events []tenantEvent) map[int64]bool {
	t.Helper()
	ids := make(map[int64]bool, len(events))
	for _, e := range events {
		id, err := st.InsertMonitorEvent(ctx, org, MonitorEventInput{
			Source:     e.source,
			DeliveryID: e.deliveryID,
			EventKind:  e.kind,
			Payload:    e.payload,
		})
		if err != nil {
			t.Fatalf("org %d insert (%s/%s): %v", org, e.source, e.deliveryID, err)
		}
		// Route every row so it becomes claimable (team is non-empty so the row
		// advances to 'routed', not the terminal 'skipped').
		if err := st.SetMonitorEventRoute(ctx, org, id, "red_team"); err != nil {
			t.Fatalf("org %d route id %d: %v", org, id, err)
		}
		ids[id] = true
	}
	return ids
}

// tenantClaimAll drains org's routed queue via ClaimPendingMonitorEvents in
// bounded batches, returning every claimed row. It loops until a claim comes
// back empty so the whole queue is observed — the property is that NONE of the
// claimed rows belongs to the other org, no matter how many batches it takes.
func tenantClaimAll(t *testing.T, ctx context.Context, st *Store, org int64, batch int) []MonitorEvent {
	t.Helper()
	var all []MonitorEvent
	for {
		got, err := st.ClaimPendingMonitorEvents(ctx, org, batch)
		if err != nil {
			t.Fatalf("org %d claim: %v", org, err)
		}
		if len(got) == 0 {
			return all
		}
		all = append(all, got...)
	}
}

// TestMonitorEventTenantIsolation is the property test for
//
//	Property 1: Tenant isolation
//	Validates: Requirements 8.1, 19.1, 19.2, 20.5
//
// The property: seed events for two distinct orgs A and B (random counts /
// sources / delivery_ids, varied per generated case), then assert the store
// slice keeps the two tenants strictly apart:
//
//	(a) ListMonitorEvents(A) returns ONLY org-A rows and never any org-B row,
//	    and symmetrically ListMonitorEvents(B) returns only org-B rows — a
//	    cross-org read finds nothing of the other tenant (Req 8.1, 19.2).
//	(b) ClaimPendingMonitorEvents(A) never returns an org-B row even when B has
//	    routed (claimable) rows waiting, and vice versa — the claim predicate is
//	    org-scoped so a worker is bound to its single tenant (Req 19.1, 20.5).
//	(c) InsertMonitorEvent with the SAME (source, delivery_id) under org A and
//	    under org B BOTH succeed, because the unique key is per-org
//	    (org_id, source, delivery_id) — the delivery_id namespace is per-tenant
//	    (Req 19.1).
//
// The per-org event sets vary across generated cases (count in [1,6], random
// sources/kinds/payloads) via a fixed-seed math/rand loop (pgregory.net/rapid is
// not in go.mod; the fixed seed keeps a failure reproducible). A fresh org pair
// is seeded per case so ids never alias across cases.
func TestMonitorEventTenantIsolation(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// Fixed seed: deterministic case generation so a failure is reproducible.
	rng := rand.New(rand.NewSource(0x7E9A97))

	const cases = 40
	for c := 0; c < cases; c++ {
		// A fresh org pair per case: ids never alias across cases, and each case
		// is an independent two-tenant world.
		orgA := tenantSeedOrg(t, ctx, st, fmt.Sprintf("tenant-A-%d", c))
		orgB := tenantSeedOrg(t, ctx, st, fmt.Sprintf("tenant-B-%d", c))

		// Random, non-empty per-org counts drive the generated sets. The two
		// orgs get DIFFERENT counts most of the time so the sets are not
		// symmetric, which would hide a predicate that leaked the larger set.
		nA := 1 + rng.Intn(6)
		nB := 1 + rng.Intn(6)

		// The overlap window is the set of (source, delivery_id) pairs offered to
		// BOTH orgs. For those shared pairs we pin an identical source per index
		// so arm (c) tests a truly identical (source, delivery_id) across tenants.
		shared := 1 + rng.Intn(minInt(nA, nB))
		sharedSources := make([]string, shared)
		sharedDeliveries := make([]string, shared)
		srcChoices := []string{"github", "correlate", "discovery", "sweep"}
		for i := 0; i < shared; i++ {
			sharedSources[i] = srcChoices[rng.Intn(len(srcChoices))]
			sharedDeliveries[i] = fmt.Sprintf("shared-%d-%d", c, i)
		}

		// Build each org's event set. The first `shared` entries use the IDENTICAL
		// (source, delivery_id) pairs across both orgs (arm c); the remainder use
		// org-tagged delivery_ids that are unique to that org.
		eventsA := tenantBuildSet(rng, "A", nA, shared, sharedSources, sharedDeliveries, c)
		eventsB := tenantBuildSet(rng, "B", nB, shared, sharedSources, sharedDeliveries, c)

		// (c) Insert + route both orgs. The shared (source, delivery_id) pairs
		// MUST succeed under BOTH orgs because the unique key is per-org; if the
		// namespace were global, org B's shared inserts would collide and
		// tenantInsertRouted would fail here.
		idsA := tenantInsertRouted(t, ctx, st, orgA, eventsA)
		idsB := tenantInsertRouted(t, ctx, st, orgB, eventsB)

		if len(idsA) != nA {
			t.Fatalf("case %d: org A persisted %d rows, want %d (per-org delivery_id namespace)", c, len(idsA), nA)
		}
		if len(idsB) != nB {
			t.Fatalf("case %d: org B persisted %d rows, want %d (per-org delivery_id namespace)", c, len(idsB), nB)
		}

		// (a) ListMonitorEvents(A) returns ONLY org-A rows; none of org B's ids
		//     appear, and every row carries org A's id. Symmetric for B.
		tenantAssertListScoped(t, ctx, st, c, "A", orgA, idsA, idsB)
		tenantAssertListScoped(t, ctx, st, c, "B", orgB, idsB, idsA)

		// (b) ClaimPendingMonitorEvents(A) never returns an org-B row even though
		//     B has routed rows waiting (we have NOT claimed B yet, so its queue
		//     is full). Drain A's queue; assert every claimed row is an org-A id
		//     and carries org A's org_id. Then drain B and assert the same for B,
		//     proving A's drain did not consume or hide any of B's rows.
		claimedA := tenantClaimAll(t, ctx, st, orgA, 1+rng.Intn(4))
		if len(claimedA) != nA {
			t.Fatalf("case %d: org A claimed %d rows, want %d (its full routed queue)", c, len(claimedA), nA)
		}
		for _, e := range claimedA {
			if e.OrgID != orgA {
				t.Fatalf("case %d: org A claim returned a row with org_id %d (want %d) — cross-tenant leak", c, e.OrgID, orgA)
			}
			if idsB[e.ID] {
				t.Fatalf("case %d: org A claim returned org-B row id %d — SKIP LOCKED claim is not org-scoped", c, e.ID)
			}
			if !idsA[e.ID] {
				t.Fatalf("case %d: org A claim returned unknown row id %d", c, e.ID)
			}
		}

		// B's queue was untouched by A's drain: claiming B now still yields all nB
		// of B's rows, and never an org-A row.
		claimedB := tenantClaimAll(t, ctx, st, orgB, 1+rng.Intn(4))
		if len(claimedB) != nB {
			t.Fatalf("case %d: org B claimed %d rows, want %d (A's drain must not touch B's queue)", c, len(claimedB), nB)
		}
		for _, e := range claimedB {
			if e.OrgID != orgB {
				t.Fatalf("case %d: org B claim returned a row with org_id %d (want %d) — cross-tenant leak", c, e.OrgID, orgB)
			}
			if idsA[e.ID] {
				t.Fatalf("case %d: org B claim returned org-A row id %d — SKIP LOCKED claim is not org-scoped", c, e.ID)
			}
		}
	}
}

// tenantBuildSet builds one org's event set: the first `shared` entries use the
// IDENTICAL (source, delivery_id) pairs passed in (so the same key is offered to
// both orgs, exercising the per-tenant namespace), and the remaining entries use
// org-tagged delivery_ids unique to this org.
func tenantBuildSet(rng *rand.Rand, orgTag string, n, shared int, sharedSources, sharedDeliveries []string, caseIdx int) []tenantEvent {
	kinds := []string{"ci", "vulnerability", "code_change", "resource"}
	srcChoices := []string{"github", "correlate", "discovery", "sweep"}
	payloads := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"a":1}`),
		json.RawMessage(`{"nested":{"k":"v"},"arr":[1,2,3]}`),
		json.RawMessage(fmt.Sprintf(`{"org_tag":%q,"severity":"high"}`, orgTag)),
	}
	out := make([]tenantEvent, 0, n)
	for i := 0; i < n; i++ {
		if i < shared {
			// Identical (source, delivery_id) across both orgs (arm c).
			out = append(out, tenantEvent{
				source:     sharedSources[i],
				deliveryID: sharedDeliveries[i],
				kind:       kinds[rng.Intn(len(kinds))],
				payload:    payloads[i%len(payloads)],
			})
			continue
		}
		// Org-unique delivery_id for the non-overlapping tail.
		out = append(out, tenantEvent{
			source:     srcChoices[rng.Intn(len(srcChoices))],
			deliveryID: fmt.Sprintf("org%s-%d-%d", orgTag, caseIdx, i),
			kind:       kinds[rng.Intn(len(kinds))],
			payload:    payloads[i%len(payloads)],
		})
	}
	return out
}

// tenantAssertListScoped asserts ListMonitorEvents(org) returns exactly this
// org's rows (ownIDs), never any of the other org's rows (otherIDs), and that
// every returned row carries this org's id. A generous limit drains the whole
// list so a leaked row cannot hide beyond the page.
func tenantAssertListScoped(t *testing.T, ctx context.Context, st *Store, caseIdx int, tag string, org int64, ownIDs, otherIDs map[int64]bool) {
	t.Helper()
	list, err := st.ListMonitorEvents(ctx, org, 1000)
	if err != nil {
		t.Fatalf("case %d: list org %s: %v", caseIdx, tag, err)
	}
	if len(list) != len(ownIDs) {
		t.Fatalf("case %d: list org %s returned %d rows, want %d (only this tenant's rows)", caseIdx, tag, len(list), len(ownIDs))
	}
	seen := make(map[int64]bool, len(list))
	for _, e := range list {
		if e.OrgID != org {
			t.Fatalf("case %d: list org %s returned a row with org_id %d (want %d) — cross-tenant leak", caseIdx, tag, e.OrgID, org)
		}
		if otherIDs[e.ID] {
			t.Fatalf("case %d: list org %s returned the OTHER tenant's row id %d — list is not org-scoped", caseIdx, tag, e.ID)
		}
		if !ownIDs[e.ID] {
			t.Fatalf("case %d: list org %s returned unknown row id %d", caseIdx, tag, e.ID)
		}
		seen[e.ID] = true
	}
	// Every one of this org's rows is present (nothing of its own went missing).
	for id := range ownIDs {
		if !seen[id] {
			t.Fatalf("case %d: list org %s missing own row id %d", caseIdx, tag, id)
		}
	}
}

// minInt returns the smaller of two ints (Go 1.21's builtin min is avoided to
// keep this test buildable on the module's pinned toolchain).
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
