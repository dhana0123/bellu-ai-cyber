//go:build integration

package store

import (
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// This file carries the INTEGRATION arm of the PoC-redaction property test: the
// persist-and-read-back round-trip that proves the store never returns a
// weaponized body. It is gated behind the `integration` build tag because
// UpsertFindingReport / GetFindingReport need a real Postgres (the redaction is
// a defense-in-depth strip that must hold against an actual INSERT/SELECT), so
// it reuses the testcontainers harness newTestStore from
// store_integration_test.go. Run it with a container runtime:
//
//	go test -tags integration ./internal/store/... -run TestFindingReportRoundTripRedactsPoC
//
// The PURE arm (over stripPoCScriptCode, no DB) lives in the non-tagged
// finding_reports_property_test.go and shares the plantCounter / collectKeys /
// weaponizedPoC generators defined there (same package, both built under this
// tag). seedOrg in monitor_events_property_test.go is NOT reused for a finding
// — this arm needs a findings row, so it uses a distinctly named helper
// reportSeedFinding to avoid any symbol clash with existing store test files.

// reportSeedFinding inserts one findings row directly and returns its id. The
// store package test has no finding seed helper (finding ingest lives in
// internal/finding), and finding_reports.finding_id is an FK to findings(id)
// with UpsertFindingReport gated through an INSERT ... SELECT FROM findings, so
// a bare findings row is the minimal fixture the round-trip needs. The name is
// deliberately distinct from monitor_events_property_test.go's seedOrg to avoid
// a redeclaration clash under the shared integration build.
func reportSeedFinding(t *testing.T, ctx context.Context, st *Store, assetID, vulnID string) int64 {
	t.Helper()
	var id int64
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO findings (asset_id, asset_kind, vuln_id, intel_ref, severity)
		 VALUES ($1, 'host', $2, 'intel:ref:1', 'high')
		 RETURNING id`,
		assetID, vulnID).Scan(&id); err != nil {
		t.Fatalf("seed finding (%s/%s): %v", assetID, vulnID, err)
	}
	return id
}

// TestFindingReportRoundTripRedactsPoC is the INTEGRATION arm of
//
//	Property 6: PoC redaction
//	Validates: Requirements 16.1, 16.2, 16.3, 18.3 (store slice)
//
// The property: for any generated sections JSON carrying a weaponized
// poc_script_code value at arbitrary nesting, UpsertFindingReport-ing a report
// whose Sections hold the body and then GetFindingReport-ing it back yields a
// report whose sections contain NO poc_script_code key at any level and in
// which the weaponized body string appears NOWHERE — while poc_available may
// still be true (the flag is allowed; only the body is forbidden). Random
// nested JSON with planted poc_script_code values is built with a fixed-seed
// math/rand builder (shared plantCounter from the pure arm) so a failing case
// is reproducible.
func TestFindingReportRoundTripRedactsPoC(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// A single org and finding are enough: the (finding_id, team) upsert is
	// idempotent, so each case re-reports the SAME red report for the finding,
	// exercising both the initial INSERT and the ON CONFLICT UPDATE paths
	// through the redaction strip.
	org := seedOrg(t, ctx, st, "report-prop-org")
	findingID := reportSeedFinding(t, ctx, st, "asset:prop:1", "CVE-PROP-0001")

	// Fixed seed: deterministic, reproducible section trees.
	rng := rand.New(rand.NewSource(0x5EC5EED))

	const cases = 40
	for c := 0; c < cases; c++ {
		depth := 1 + rng.Intn(5)
		tree, planted := plantCounter(rng, depth)
		if planted == 0 {
			// Guarantee at least one planted body per case so the round-trip is
			// never vacuous: wrap the tree in an object that carries one.
			tree = map[string]any{
				pocScriptCodeKey: weaponizedPoC,
				"wrapped":        tree,
			}
		}

		sections, err := json.Marshal(tree)
		if err != nil {
			t.Fatalf("case %d: marshal generated sections: %v", c, err)
		}

		// Persist a report whose sections carry the weaponized body. poc_available
		// is intentionally true: the flag is allowed to survive, only the body
		// must be stripped.
		if err := st.UpsertFindingReport(ctx, org, FindingReport{
			FindingID:        findingID,
			Team:             "red",
			Title:            "round-trip report",
			Severity:         "high",
			PoCAvailable:     true,
			Sections:         sections,
			RenderedMarkdown: "# report\n\nrendered artifact (no body)",
		}); err != nil {
			t.Fatalf("case %d: upsert finding report: %v", c, err)
		}

		reports, err := st.GetFindingReport(ctx, org, findingID)
		if err != nil {
			t.Fatalf("case %d: get finding report: %v", c, err)
		}
		if len(reports) != 1 {
			t.Fatalf("case %d: got %d reports, want exactly 1 (idempotent upsert)", c, len(reports))
		}
		got := reports[0]

		// The flag is allowed to be true; the body must never come back.
		if !got.PoCAvailable {
			t.Fatalf("case %d: poc_available flag was lost on round-trip", c)
		}

		// 1) The weaponized body string appears NOWHERE in the returned sections.
		if strings.Contains(string(got.Sections), weaponizedPoC) {
			t.Fatalf("case %d: round-tripped sections still contain the weaponized PoC body:\n%s",
				c, string(got.Sections))
		}

		// 2) NO poc_script_code key survives at any nesting level.
		var sectionsVal any
		if err := json.Unmarshal(got.Sections, &sectionsVal); err != nil {
			t.Fatalf("case %d: returned sections are not valid json: %v", c, err)
		}
		keys := map[string]int{}
		collectKeys(sectionsVal, keys)
		if n := keys[pocScriptCodeKey]; n != 0 {
			t.Fatalf("case %d: %d poc_script_code key(s) survived the round-trip, want 0", c, n)
		}
	}
}
