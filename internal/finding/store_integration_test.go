//go:build integration

package finding

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// newTestStore spins up a disposable Postgres and returns a migrated store.
// It mirrors the harness in internal/store/store_integration_test.go so the
// findings integration suite runs standalone against a real database with all
// migrations (including 00006_findings) applied.
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

// readOne returns the single findings row for (assetID, vulnID), failing the
// test if there is not exactly one. It reads via List filtered down to the one
// asset_kind/severity-agnostic set and then narrows in Go, keeping the helper
// independent of List's filter surface.
func readOne(t *testing.T, s *Store, assetID, vulnID string) Finding {
	t.Helper()
	all, err := s.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var matches []Finding
	for _, f := range all {
		if f.AssetID == assetID && f.VulnID == vulnID {
			matches = append(matches, f)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 row for (%s,%s), got %d", assetID, vulnID, len(matches))
	}
	return matches[0]
}

// TestFindingUpsertAndIdempotence proves a matched finding is written open with
// first/last seen set, and that re-upserting the same (asset,vuln) is
// idempotent except for last_seen: the row count stays at one (dedup), first_seen
// is preserved, last_seen advances, and risk_score/severity reflect the latest
// upsert.
//
// Validates: Requirements 12.1, 12.2, 12.4, 18.4 (correctness Property 3).
func TestFindingUpsertAndIdempotence(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	fs := NewStore(st)

	f := Finding{
		AssetID:   "arn:aws:iam::111122223333:role/admin",
		AssetKind: model.KindIdentity,
		VulnID:    "CVE-2024-0001",
		IntelRef:  "osv:CVE-2024-0001",
		RiskScore: 72.5,
		Severity:  canonical.SevHigh,
	}
	if err := fs.Upsert(ctx, f); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	got := readOne(t, fs, f.AssetID, f.VulnID)
	if got.Status != StatusOpen {
		t.Fatalf("expected status open, got %q", got.Status)
	}
	if got.FirstSeen.IsZero() || got.LastSeen.IsZero() {
		t.Fatalf("expected first_seen and last_seen set, got first=%v last=%v",
			got.FirstSeen, got.LastSeen)
	}
	if got.RiskScore != 72.5 {
		t.Fatalf("expected risk_score 72.5, got %v", got.RiskScore)
	}
	if got.Severity != canonical.SevHigh {
		t.Fatalf("expected severity high, got %q", got.Severity)
	}
	firstSeen := got.FirstSeen
	lastSeen := got.LastSeen

	// Advance wall clock so now() on the second upsert is observably later.
	time.Sleep(10 * time.Millisecond)

	// Re-upsert the SAME (asset,vuln) with an updated risk_score/severity to
	// exercise the ON CONFLICT UPDATE branch.
	f.RiskScore = 88.0
	f.Severity = canonical.SevCritical
	if err := fs.Upsert(ctx, f); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got2 := readOne(t, fs, f.AssetID, f.VulnID) // also asserts exactly one row (dedup)
	if !got2.FirstSeen.Equal(firstSeen) {
		t.Fatalf("first_seen changed on re-upsert: was %v now %v", firstSeen, got2.FirstSeen)
	}
	if !got2.LastSeen.After(lastSeen) {
		t.Fatalf("last_seen did not advance: was %v now %v", lastSeen, got2.LastSeen)
	}
	if got2.RiskScore != 88.0 {
		t.Fatalf("expected updated risk_score 88.0, got %v", got2.RiskScore)
	}
	if got2.Severity != canonical.SevCritical {
		t.Fatalf("expected updated severity critical, got %q", got2.Severity)
	}
	if got2.Status != StatusOpen {
		t.Fatalf("expected status still open, got %q", got2.Status)
	}
}

// TestFindingResolveMissing proves the resolve-on-disappear lifecycle: an open
// finding not in the matched set transitions to resolved while matched ones stay
// open; re-matching a resolved finding re-opens it with its original first_seen
// preserved; and an empty matched set resolves every open finding.
//
// Validates: Requirement 12.3 (and 12.4 first_seen preservation on re-open).
func TestFindingResolveMissing(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	fs := NewStore(st)

	a := Finding{
		AssetID: "asset-A", AssetKind: model.KindCompute,
		VulnID: "CVE-A", IntelRef: "osv:CVE-A",
		RiskScore: 50, Severity: canonical.SevMedium,
	}
	b := Finding{
		AssetID: "asset-B", AssetKind: model.KindDataStore,
		VulnID: "CVE-B", IntelRef: "osv:CVE-B",
		RiskScore: 60, Severity: canonical.SevHigh,
	}
	if err := fs.Upsert(ctx, a); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if err := fs.Upsert(ctx, b); err != nil {
		t.Fatalf("upsert B: %v", err)
	}
	bFirstSeen := readOne(t, fs, b.AssetID, b.VulnID).FirstSeen

	// A still matches this run; B no longer matches.
	if err := fs.ResolveMissing(ctx, []Key{{AssetID: a.AssetID, VulnID: a.VulnID}}); err != nil {
		t.Fatalf("resolve missing (B drops): %v", err)
	}
	if s := readOne(t, fs, a.AssetID, a.VulnID).Status; s != StatusOpen {
		t.Fatalf("expected A open, got %q", s)
	}
	if s := readOne(t, fs, b.AssetID, b.VulnID).Status; s != StatusResolved {
		t.Fatalf("expected B resolved, got %q", s)
	}

	// B matches again on a later run: it must re-open, preserving first_seen.
	time.Sleep(10 * time.Millisecond)
	if err := fs.Upsert(ctx, b); err != nil {
		t.Fatalf("re-upsert B: %v", err)
	}
	gotB := readOne(t, fs, b.AssetID, b.VulnID)
	if gotB.Status != StatusOpen {
		t.Fatalf("expected B re-opened, got %q", gotB.Status)
	}
	if !gotB.FirstSeen.Equal(bFirstSeen) {
		t.Fatalf("B first_seen changed on re-open: was %v now %v", bFirstSeen, gotB.FirstSeen)
	}

	// Empty matched set: nothing matched this run, so every open finding resolves.
	if err := fs.ResolveMissing(ctx, nil); err != nil {
		t.Fatalf("resolve missing (empty): %v", err)
	}
	if s := readOne(t, fs, a.AssetID, a.VulnID).Status; s != StatusResolved {
		t.Fatalf("expected A resolved after empty match, got %q", s)
	}
	if s := readOne(t, fs, b.AssetID, b.VulnID).Status; s != StatusResolved {
		t.Fatalf("expected B resolved after empty match, got %q", s)
	}
}

// TestFindingListFilters proves List applies severity, status, and asset_kind
// filters plus the row limit, backing the findings API query surface.
//
// Validates: Requirement 13.1 (list filtering support).
func TestFindingListFilters(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	fs := NewStore(st)

	seed := []Finding{
		{AssetID: "a1", AssetKind: model.KindIdentity, VulnID: "v1", IntelRef: "r1", RiskScore: 90, Severity: canonical.SevCritical},
		{AssetID: "a2", AssetKind: model.KindCompute, VulnID: "v2", IntelRef: "r2", RiskScore: 80, Severity: canonical.SevHigh},
		{AssetID: "a3", AssetKind: model.KindCompute, VulnID: "v3", IntelRef: "r3", RiskScore: 40, Severity: canonical.SevMedium},
		{AssetID: "a4", AssetKind: model.KindDataStore, VulnID: "v4", IntelRef: "r4", RiskScore: 20, Severity: canonical.SevLow},
	}
	for _, f := range seed {
		if err := fs.Upsert(ctx, f); err != nil {
			t.Fatalf("seed upsert %s: %v", f.AssetID, err)
		}
	}
	// Resolve one so status filtering has something to exclude.
	if err := fs.ResolveMissing(ctx, []Key{
		{AssetID: "a1", VulnID: "v1"},
		{AssetID: "a2", VulnID: "v2"},
		{AssetID: "a3", VulnID: "v3"},
	}); err != nil { // a4 drops -> resolved
		t.Fatalf("resolve a4: %v", err)
	}

	// Severity filter.
	bySev, err := fs.List(ctx, Filter{Severity: canonical.SevHigh})
	if err != nil {
		t.Fatalf("list by severity: %v", err)
	}
	if len(bySev) != 1 || bySev[0].AssetID != "a2" {
		t.Fatalf("expected only a2 for severity=high, got %+v", bySev)
	}

	// Asset-kind filter: two Compute findings.
	byKind, err := fs.List(ctx, Filter{AssetKind: model.KindCompute})
	if err != nil {
		t.Fatalf("list by kind: %v", err)
	}
	if len(byKind) != 2 {
		t.Fatalf("expected 2 Compute findings, got %d: %+v", len(byKind), byKind)
	}

	// Status filter: a4 was resolved, the rest open.
	resolved, err := fs.List(ctx, Filter{Status: StatusResolved})
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(resolved) != 1 || resolved[0].AssetID != "a4" {
		t.Fatalf("expected only a4 resolved, got %+v", resolved)
	}
	open, err := fs.List(ctx, Filter{Status: StatusOpen})
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 3 {
		t.Fatalf("expected 3 open findings, got %d", len(open))
	}

	// Limit caps the result set.
	limited, err := fs.List(ctx, Filter{Limit: 2})
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected 2 rows with limit=2, got %d", len(limited))
	}
}
