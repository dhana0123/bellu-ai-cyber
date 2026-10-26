//go:build integration

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"testing/quick"
)

// Property 4: Claim exclusivity (SKIP LOCKED).
// Validates Requirements 6.1, 6.2.
//
// ClaimPendingMonitorEvents is the single lock-and-advance CTE
// (SELECT ... FOR UPDATE SKIP LOCKED). The invariant this test pins down is
// claim exclusivity under concurrency: when K workers race to claim over the
// same pool of N routed rows for one org, a given row is handed to AT MOST ONE
// worker. Concretely, for any generated (N, K):
//
//	(a) the UNION of every worker's claimed id set has NO duplicate — no row is
//	    claimed by two workers (Req 6.2: a claimed row is never re-claimable);
//	(b) the total number of distinct claimed ids never exceeds N — the race can
//	    never conjure a row that was not seeded (Req 6.1);
//	(c) each worker's own returned set is itself duplicate-free and every id it
//	    returns is one of the seeded ids (no cross-talk, no phantom rows).
//
// Because SKIP LOCKED only excludes rows a CONCURRENT transaction currently
// holds locked, the K claims must genuinely overlap for the property to mean
// anything. Each claim runs as one statement over its own pooled connection, so
// launching the K goroutines together and barrier-releasing them maximizes the
// window in which their row locks coexist. We assert on the returned id sets
// (pairwise-disjoint), which is exactly the observable guarantee SKIP LOCKED
// gives a caller.

// seedClaimOrg inserts a fresh organization and returns its id. monitor_events
// has an FK to organizations(id), so every seeded event must reference a real
// org. A new org per trial keeps trials isolated (no routed rows leak between
// generated cases). Named distinctly from any task 2.2 helper to avoid a
// duplicate-symbol clash in the shared package build.
func seedClaimOrg(t *testing.T, st *Store) int64 {
	t.Helper()
	var org int64
	if err := st.Pool().QueryRow(context.Background(),
		`INSERT INTO organizations (name) VALUES ($1) RETURNING id`,
		"claim-prop-org").Scan(&org); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return org
}

// seedRoutedEvents inserts n monitor_events for org already advanced to the
// 'routed' state (the only state ClaimPendingMonitorEvents considers). It
// returns the set of seeded ids so the property can assert the claimed union is
// a subset of exactly these. delivery_id is made unique per row so the
// (org,source,delivery_id) idempotency index never collapses two seeds.
func seedRoutedEvents(t *testing.T, st *Store, org int64, n int) map[int64]bool {
	t.Helper()
	ctx := context.Background()
	ids := make(map[int64]bool, n)
	for i := 0; i < n; i++ {
		var id int64
		if err := st.Pool().QueryRow(ctx,
			`INSERT INTO monitor_events
			     (org_id, source, delivery_id, event_kind, payload, team, state)
			 VALUES ($1,'github',$2,'ci',$3,'red_team','routed')
			 RETURNING id`,
			org, fmt.Sprintf("claim-prop-%d-%d", org, i), json.RawMessage(`{}`)).Scan(&id); err != nil {
			t.Fatalf("seed routed event %d: %v", i, err)
		}
		ids[id] = true
	}
	return ids
}

func TestProperty4_ClaimExclusivitySkipLocked(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// For a generated (N, K): seed N routed rows for a fresh org, then run K
	// concurrent claims racing over them and assert the returned id sets are
	// pairwise-disjoint, subset of the seeded ids, and union-bounded by N.
	property := func(seed int64, nSize, kSize uint8) bool {
		rng := rand.New(rand.NewSource(seed))

		// N in [1,24]: enough rows for a real race without a slow trial. K in
		// [2,8]: at least two racers (a single claim cannot violate exclusivity)
		// and a varied degree of contention across generated cases.
		n := 1 + int(nSize)%24
		k := 2 + int(kSize)%7

		// Occasionally over-subscribe the per-claim limit so some racers come up
		// empty (claimed set smaller than their limit) and sometimes under so no
		// single claim can drain the pool — both stress the overlap window.
		limit := 1 + rng.Intn(n+1)

		org := seedClaimOrg(t, st)
		seeded := seedRoutedEvents(t, st, org, n)

		// Launch K goroutines that all block on one barrier, then fire their
		// claim at once so their row locks genuinely coexist (the condition that
		// gives SKIP LOCKED something to skip).
		var (
			start   = make(chan struct{})
			wg      sync.WaitGroup
			mu      sync.Mutex
			results = make([]map[int64]bool, k)
			claimErr error
		)
		wg.Add(k)
		for w := 0; w < k; w++ {
			go func(idx int) {
				defer wg.Done()
				<-start
				events, err := st.ClaimPendingMonitorEvents(ctx, org, limit)
				got := make(map[int64]bool, len(events))
				for _, e := range events {
					got[e.ID] = true
				}
				mu.Lock()
				results[idx] = got
				if err != nil && claimErr == nil {
					claimErr = err
				}
				mu.Unlock()
			}(w)
		}
		close(start) // release all racers together
		wg.Wait()

		if claimErr != nil {
			t.Logf("claim returned error (N=%d K=%d limit=%d): %v", n, k, limit, claimErr)
			return false
		}

		// (a) + (c): fold every worker's set into a union, verifying as we go
		// that no id is seen twice across workers (pairwise-disjoint) and that
		// each returned id is one of the seeded rows.
		union := make(map[int64]bool, n)
		for idx, got := range results {
			for id := range got {
				if !seeded[id] {
					t.Logf("worker %d claimed id %d that was not seeded (N=%d K=%d)", idx, id, n, k)
					return false
				}
				if union[id] {
					t.Logf("id %d claimed by two workers (N=%d K=%d limit=%d)", id, n, k, limit)
					return false // duplicate across workers => exclusivity broken
				}
				union[id] = true
			}
		}

		// (b): the union can never exceed the number of seeded rows.
		if len(union) > n {
			t.Logf("union size %d exceeds seeded N=%d (K=%d)", len(union), n, k)
			return false
		}

		return true
	}

	// A modest MaxCount keeps the suite fast: each trial spins K goroutines
	// against the shared container and seeds a fresh org, so the cost per trial
	// is real. The varied (N, K, limit) still covers low/high contention.
	if err := quick.Check(property, &quick.Config{MaxCount: 60}); err != nil {
		t.Fatalf("Property 4 (claim exclusivity, SKIP LOCKED) failed: %v", err)
	}
}
