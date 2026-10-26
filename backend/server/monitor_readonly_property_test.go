//go:build integration

package server

import (
	"fmt"
	"math/rand"
	"net/http"
	"testing"
)

// This file is the property-based test for Task 6.1 — Property 5: Read-only
// auto-runs (Validates: Requirements 9.1, 9.2, 9.3). It is gated behind the
// `integration` build tag and reuses the testcontainers harness defined in
// monitor_loop_integration_test.go (loopEnv / newLoopEnv / doJSON / decode,
// plus mustJSON), all in the same `server` package under the integration tag.
// It therefore needs a container runtime: run it with
//
//	go test -tags integration ./backend/server/... -run TestReadOnlyAutoRunsProperty
//
// (With no Docker available it still compiles under the tag, but the harness's
// newLoopEnv will fail to start Postgres at run time.)
//
// Property 5 — Read-only auto-runs. For an arbitrary set of monitor events
// (varied sources, kinds, and payloads that route to a team), ingesting and
// claiming each one launches a run, and EVERY launched run is strictly
// read-only:
//
//   - GET /api/agent/runs/:id            reports active_testing == false  (Req 9.1)
//   - GET /api/agent/runs/:id/detail     reports scope_rules   == 0       (Req 9.2)
//
// There is no claim path that can produce a run with active_testing == true or
// a non-nil authz_scope: handleMonitorClaim builds AgentRunInput with Scope ==
// nil, so authz_scope persists as NULL (scope_rules 0) and active_testing as
// false. The property asserts this invariant holds across many generated cases,
// varying both the number and the kind of events per case. Findings produced by
// such runs land "Unvalidated" (Req 9.3); that label is applied at finding
// ingest rather than on the run, so this property focuses on the read-only run
// invariant — the structural core of 9.1/9.2 that keeps auto-runs safe.

// roKind is one routable (source, event_kind) shape plus a payload builder. The
// generator picks among these so every generated event routes to a team and
// therefore launches a run on claim — exactly the population Property 5 is about
// (a read-only run per launched claim). Payload variety (canonical_id / raw
// target / url / host / bare) exercises every targetFromEvent branch so the
// read-only invariant is checked across all the target shapes a claim can build.
type roKind struct {
	source  string
	payload func(r *rand.Rand, n int) map[string]any
}

// roKinds is the catalog the generator samples from. Each entry classifies to a
// routable event_kind (ci / code_change / vulnerability / resource) so the
// ingest advances to `routed` and the row is claimable.
var roKinds = []roKind{
	{
		// github check_run -> event_kind "ci" -> red_team; carries a canonical_id
		// so targetFromEvent takes the entity branch.
		source: "github",
		payload: func(_ *rand.Rand, n int) map[string]any {
			return map[string]any{
				"event_type":   "check_run",
				"check_run":    map[string]any{"status": "completed", "conclusion": "failure"},
				"canonical_id": fmt.Sprintf("repo:github:octo/app-%d", n),
			}
		},
	},
	{
		// github workflow_run -> "ci"; raw target branch of targetFromEvent.
		source: "github",
		payload: func(_ *rand.Rand, n int) map[string]any {
			return map[string]any{
				"event_type":   "workflow_run",
				"workflow_run": map[string]any{"status": "completed", "conclusion": "failure"},
				"target":       fmt.Sprintf("https://ci.example/run/%d", n),
			}
		},
	},
	{
		// github push -> "code_change"; url branch of targetFromEvent.
		source: "github",
		payload: func(_ *rand.Rand, n int) map[string]any {
			return map[string]any{
				"event_type": "push",
				"ref":        "refs/heads/main",
				"url":        fmt.Sprintf("https://github.com/octo/app/commit/%d", n),
			}
		},
	},
	{
		// github pull_request -> "code_change"; host branch of targetFromEvent.
		source: "github",
		payload: func(_ *rand.Rand, n int) map[string]any {
			return map[string]any{
				"event_type":   "pull_request",
				"pull_request": map[string]any{"action": "opened"},
				"host":         fmt.Sprintf("host-%d.example", n),
			}
		},
	},
	{
		// correlate vulnerability -> "vulnerability" -> blue_team; asset_id entity
		// branch of targetFromEvent. (The correlate source is the finding-delta
		// tap and always classifies to "vulnerability".)
		source: "correlate",
		payload: func(_ *rand.Rand, n int) map[string]any {
			return map[string]any{
				"vulnerability": map[string]any{
					"id":       fmt.Sprintf("CVE-2024-%04d", 1000+n),
					"severity": "high",
				},
				"asset_id": fmt.Sprintf("pkg:npm/app-%d", n),
			}
		},
	},
	{
		// discovery resource -> "resource" -> triage; no target-bearing field so
		// targetFromEvent falls back to the source:delivery_id raw reference.
		source: "discovery",
		payload: func(_ *rand.Rand, n int) map[string]any {
			return map[string]any{
				"event_type": "resource",
				"resource":   map[string]any{"kind": "host", "seq": n},
			}
		},
	},
}

// TestReadOnlyAutoRunsProperty is the Property 5 check. It runs a fixed-seed
// randomized loop over generated cases; each case ingests a varied set of
// routable events, claims them all, and asserts every launched run is read-only.
//
// Validates: Requirements 9.1, 9.2, 9.3 — every claim-launched run has
// authz_scope == NULL (scope_rules 0) and active_testing == false; no claim
// path can produce a scoped or active run.
func TestReadOnlyAutoRunsProperty(t *testing.T) {
	env := newLoopEnv(t)

	// Fixed seed => deterministic, reproducible case set.
	rng := rand.New(rand.NewSource(0x5eed_9001))

	const cases = 25

	// seq makes every delivery_id globally unique across cases so no ingest is
	// swallowed as a duplicate (which would launch no run and weaken the sample).
	seq := 0

	for ci := 0; ci < cases; ci++ {
		// Vary the number of events per case: 1..8. Mixing small and large
		// batches exercises both single-row and multi-row SKIP LOCKED claims.
		eventCount := 1 + rng.Intn(8)

		// Ingest eventCount varied, routable events. Record each launching event
		// id so we can confirm every one of them got claimed and launched a run.
		wantEventIDs := make(map[int64]bool, eventCount)
		for i := 0; i < eventCount; i++ {
			k := roKinds[rng.Intn(len(roKinds))]
			seq++
			deliveryID := fmt.Sprintf("ro-%d-%d-%d", ci, i, seq)

			var ingested struct {
				ID    int64  `json:"id"`
				State string `json:"state"`
			}
			decode(t, env.doJSON(t, "POST", "/api/monitor/events", map[string]any{
				"source":      k.source,
				"delivery_id": deliveryID,
				"payload":     mustJSON(t, k.payload(rng, seq)),
			}), http.StatusAccepted, &ingested)

			// Every generated kind is routable, so the event must land `routed`
			// (claimable) — not `skipped`. If this ever regresses the sample would
			// silently shrink, so assert it.
			if ingested.State != "routed" {
				t.Fatalf("case %d event %d (%s): state = %q, want routed", ci, i, k.source, ingested.State)
			}
			if ingested.ID == 0 {
				t.Fatalf("case %d event %d (%s): ingest returned no id", ci, i, k.source)
			}
			wantEventIDs[ingested.ID] = true
		}

		// Claim every routed row for this case. Claims come in batches (the claim
		// handler caps a batch), so loop until the queue drains, asserting the
		// read-only invariant on each launched run as it appears.
		launched := 0
		gotEventIDs := make(map[int64]bool, eventCount)
		for {
			var claimResp struct {
				Claims []struct {
					ID    int64  `json:"id"`
					RunID string `json:"run_id"`
					State string `json:"state"`
				} `json:"claims"`
			}
			decode(t, env.doJSON(t, "POST", "/api/agent/monitor/claim",
				map[string]any{"limit": maxClaimBatch}), http.StatusOK, &claimResp)

			if len(claimResp.Claims) == 0 {
				break // queue drained for this case
			}

			for _, cl := range claimResp.Claims {
				if cl.RunID == "" {
					t.Fatalf("case %d: claim of event %d launched no run (empty run_id)", ci, cl.ID)
				}
				gotEventIDs[cl.ID] = true
				launched++

				assertRunReadOnly(t, env, ci, cl.ID, cl.RunID)
			}
		}

		// Every event we ingested in this case must have been claimed exactly
		// once and launched a run — the population the invariant was asserted
		// over is the full set, not a subset.
		if launched != eventCount {
			t.Fatalf("case %d: launched %d runs, want %d (one per ingested event)", ci, launched, eventCount)
		}
		for id := range wantEventIDs {
			if !gotEventIDs[id] {
				t.Fatalf("case %d: ingested event %d was never claimed", ci, id)
			}
		}
	}
}

// assertRunReadOnly is the core Property 5 invariant check for a single launched
// run: the run reports active_testing == false (Req 9.1) and its detail reports
// scope_rules == 0, i.e. a NULL authz_scope (Req 9.2). A failure here means a
// claim path produced a non-read-only run, which must never happen.
//
// It is named distinctly from any helper in monitor_loop_integration_test.go to
// avoid a symbol clash in the shared `server` integration build.
func assertRunReadOnly(t *testing.T, env *loopEnv, caseIdx int, eventID int64, runID string) {
	t.Helper()

	// Req 9.1: GET /api/agent/runs/:id surfaces active_testing; an auto-triggered
	// run must report false.
	var status struct {
		RunID         string `json:"run_id"`
		Status        string `json:"status"`
		ActiveTesting bool   `json:"active_testing"`
	}
	decode(t, env.doJSON(t, "GET", "/api/agent/runs/"+runID, nil), http.StatusOK, &status)
	if status.ActiveTesting {
		t.Fatalf("case %d event %d run %s: active_testing=true, want false (read-only, Req 9.1)",
			caseIdx, eventID, runID)
	}

	// Req 9.2: GET /api/agent/runs/:id/detail reports scope_rules ==
	// len(authz_scope.allow_targets); a nil/NULL scope yields 0.
	var detail struct {
		RunID      string `json:"run_id"`
		ScopeRules int    `json:"scope_rules"`
	}
	decode(t, env.doJSON(t, "GET", "/api/agent/runs/"+runID+"/detail", nil), http.StatusOK, &detail)
	if detail.ScopeRules != 0 {
		t.Fatalf("case %d event %d run %s: scope_rules=%d, want 0 (NULL authz_scope, Req 9.2)",
			caseIdx, eventID, runID, detail.ScopeRules)
	}
}
