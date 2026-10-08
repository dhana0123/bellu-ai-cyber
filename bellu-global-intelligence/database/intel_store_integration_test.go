//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// newIntelTestStore spins up a disposable Postgres, applies all migrations
// (including 00005_intel), and returns a migrated store. It mirrors the
// harness in internal/store/store_integration_test.go so intel persistence is
// exercised against the exact same database access path as the rest of the
// pipeline.
func newIntelTestStore(t *testing.T) *store.Store {
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

// intelFixtures returns a small set of valid canonical vulnerabilities covering
// aliases, affected ranges, CVSS, EPSS, and the KEV flag.
func intelFixtures() []canonical.CanonicalVuln {
	return []canonical.CanonicalVuln{
		{
			ID:      "CVE-2024-1001",
			Aliases: []string{"CVE-2024-1001", "GHSA-AAAA-BBBB-CCCC"},
			Summary: "Remote code execution in widget parser",
			Affected: []canonical.AffectedPackage{
				{
					Purl:      "pkg:pypi/requests",
					Ecosystem: "PyPI",
					Ranges: []canonical.SemverRange{
						{Introduced: "2.0.0", Fixed: "2.31.0"},
					},
				},
			},
			CVSS:           canonical.CVSS{Vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", Score: 9.8, Version: "3.1"},
			Severity:       canonical.SevCritical,
			KnownExploited: true,
			EPSS:           0.42,
			Published:      time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			Modified:       time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
			Sources:        []string{"osv", "kev"},
		},
		{
			ID:      "CVE-2024-1002",
			Aliases: []string{"CVE-2024-1002", "PYSEC-2024-7"},
			Summary: "Denial of service via crafted input",
			Affected: []canonical.AffectedPackage{
				{
					Purl:      "pkg:npm/left-pad",
					Ecosystem: "npm",
					Ranges: []canonical.SemverRange{
						{Introduced: "1.0.0", Fixed: "1.3.0"},
						{Introduced: "2.0.0", Fixed: ""},
					},
				},
			},
			CVSS:     canonical.CVSS{Vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H", Score: 7.5, Version: "3.1"},
			Severity: canonical.SevHigh,
			EPSS:     0.11,
			Sources:  []string{"osv"},
		},
		{
			ID:      "GHSA-ZZZZ-YYYY-XXXX",
			Aliases: []string{"GHSA-ZZZZ-YYYY-XXXX"},
			Summary: "Information disclosure (no CVE assigned)",
			Affected: []canonical.AffectedPackage{
				{
					Purl:      "pkg:golang/github.com/example/lib",
					Ecosystem: "Go",
					// No explicit ranges: records a single open range.
				},
			},
			Severity: canonical.SevMedium,
			Sources:  []string{"osv"},
		},
	}
}

// intelState is a snapshot of the row counts across the three persisted intel
// tables that idempotence (Property 3) must leave unchanged on a no-op re-run.
type intelState struct {
	vulns    int
	aliases  int
	affected int
}

func snapshotIntelState(t *testing.T, st *store.Store) intelState {
	t.Helper()
	ctx := context.Background()
	var s intelState
	count := func(table string) int {
		var n int
		if err := st.Pool().QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	s.vulns = count("intel_vulnerabilities")
	s.aliases = count("intel_aliases")
	s.affected = count("intel_affected_packages")
	return s
}

// TestIntelUpsertVulnsIdempotent proves Property 3: re-running the same
// canonical set leaves every intel_* table unchanged except last_seen on
// intel_vulnerabilities (Requirements 6.2, 18.4).
func TestIntelUpsertVulnsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newIntelTestStore(t)
	is := New(st)

	fixtures := intelFixtures()
	const knownID = "CVE-2024-1001"

	// First upsert: establishes the baseline state.
	if err := is.UpsertVulns(ctx, fixtures, time.Time{}); err != nil {
		t.Fatalf("first UpsertVulns: %v", err)
	}

	before := snapshotIntelState(t, st)
	if before.vulns != len(fixtures) {
		t.Fatalf("expected %d vulns after first run, got %d", len(fixtures), before.vulns)
	}
	// Fixtures carry 2 + 2 + 1 = 5 aliases, and 1 + 2 + 1 = 4 affected ranges.
	if before.aliases != 5 {
		t.Fatalf("expected 5 aliases after first run, got %d", before.aliases)
	}
	if before.affected != 4 {
		t.Fatalf("expected 4 affected ranges after first run, got %d", before.affected)
	}

	var firstSeenBefore, lastSeenBefore time.Time
	if err := st.Pool().QueryRow(ctx,
		`SELECT first_seen, last_seen FROM intel_vulnerabilities WHERE id=$1`, knownID).
		Scan(&firstSeenBefore, &lastSeenBefore); err != nil {
		t.Fatalf("select timestamps before: %v", err)
	}

	// A tiny pause so now() on the re-run is strictly observable.
	time.Sleep(10 * time.Millisecond)

	// Second upsert: the SAME set. This must be a no-op except for last_seen.
	if err := is.UpsertVulns(ctx, fixtures, time.Time{}); err != nil {
		t.Fatalf("second UpsertVulns: %v", err)
	}

	after := snapshotIntelState(t, st)
	if after != before {
		t.Fatalf("row counts changed on re-run: before=%+v after=%+v", before, after)
	}

	var firstSeenAfter, lastSeenAfter time.Time
	if err := st.Pool().QueryRow(ctx,
		`SELECT first_seen, last_seen FROM intel_vulnerabilities WHERE id=$1`, knownID).
		Scan(&firstSeenAfter, &lastSeenAfter); err != nil {
		t.Fatalf("select timestamps after: %v", err)
	}

	if !firstSeenAfter.Equal(firstSeenBefore) {
		t.Fatalf("first_seen changed on re-run: before=%s after=%s", firstSeenBefore, firstSeenAfter)
	}
	if lastSeenAfter.Before(lastSeenBefore) {
		t.Fatalf("last_seen moved backwards on re-run: before=%s after=%s", lastSeenBefore, lastSeenAfter)
	}
	if !lastSeenAfter.After(lastSeenBefore) {
		t.Fatalf("last_seen was not refreshed on re-run: before=%s after=%s", lastSeenBefore, lastSeenAfter)
	}
}

// TestIntelRecordFetchRun verifies RecordFetchRun inserts an audit row into
// intel_fetch_runs and returns its id (Requirement 6.4).
func TestIntelRecordFetchRun(t *testing.T) {
	ctx := context.Background()
	st := newIntelTestStore(t)
	is := New(st)

	run := FetchRun{
		Source:         "osv",
		Cursor:         "cursor-2",
		RawCount:       100,
		RecordsAdded:   10,
		RecordsUpdated: 5,
		CursorBefore:   "cursor-1",
		CursorAfter:    "cursor-2",
		Status:         "SUCCESS",
		StartedAt:      time.Now().UTC().Add(-time.Minute),
		FinishedAt:     time.Now().UTC(),
	}
	id, err := is.RecordFetchRun(ctx, run)
	if err != nil {
		t.Fatalf("RecordFetchRun: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive run id, got %d", id)
	}

	var (
		count         int
		source, state string
		added         int
	)
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM intel_fetch_runs`).Scan(&count); err != nil {
		t.Fatalf("count fetch runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 fetch run row, got %d", count)
	}
	if err := st.Pool().QueryRow(ctx,
		`SELECT source, status, records_added FROM intel_fetch_runs WHERE id=$1`, id).
		Scan(&source, &state, &added); err != nil {
		t.Fatalf("select fetch run: %v", err)
	}
	if source != "osv" || state != "SUCCESS" || added != 10 {
		t.Fatalf("unexpected fetch run row: source=%q status=%q added=%d", source, state, added)
	}
}

// TestIntelEPSSHistoryIdempotent verifies that UpsertVuln with a non-zero
// scoredOn appends an intel_epss_history row keyed by (vuln_id, scored_on), and
// that re-running on the same calendar day does not add a second row — only the
// day's epss/percentile are overwritten (Requirement 6.3 / Property 3).
func TestIntelEPSSHistoryIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newIntelTestStore(t)
	is := New(st)

	v := intelFixtures()[0] // CVE-2024-1001, EPSS 0.42
	scoredOn := time.Date(2024, 6, 1, 13, 30, 0, 0, time.UTC)

	if err := is.UpsertVuln(ctx, v, scoredOn, 0.9); err != nil {
		t.Fatalf("first UpsertVuln with EPSS history: %v", err)
	}

	historyCount := func() int {
		var n int
		if err := st.Pool().QueryRow(ctx,
			`SELECT count(*) FROM intel_epss_history WHERE vuln_id=$1`, v.ID).Scan(&n); err != nil {
			t.Fatalf("count epss history: %v", err)
		}
		return n
	}

	if got := historyCount(); got != 1 {
		t.Fatalf("expected 1 epss history row after first upsert, got %d", got)
	}

	// Re-run on the SAME calendar day (different clock time): still one row.
	sameDay := time.Date(2024, 6, 1, 23, 59, 59, 0, time.UTC)
	if err := is.UpsertVuln(ctx, v, sameDay, 0.95); err != nil {
		t.Fatalf("second UpsertVuln on same day: %v", err)
	}
	if got := historyCount(); got != 1 {
		t.Fatalf("expected epss history to stay at 1 row on same-day re-run, got %d", got)
	}

	// The day's values are overwritten in place.
	var epss, percentile float64
	if err := st.Pool().QueryRow(ctx,
		`SELECT epss, percentile FROM intel_epss_history WHERE vuln_id=$1`, v.ID).
		Scan(&epss, &percentile); err != nil {
		t.Fatalf("select epss history row: %v", err)
	}
	if epss != v.EPSS {
		t.Fatalf("expected epss %v, got %v", v.EPSS, epss)
	}
	if percentile != 0.95 {
		t.Fatalf("expected percentile overwritten to 0.95, got %v", percentile)
	}

	// A later day appends a second data point.
	nextDay := scoredOn.AddDate(0, 0, 1)
	if err := is.UpsertVuln(ctx, v, nextDay, 0.5); err != nil {
		t.Fatalf("UpsertVuln on next day: %v", err)
	}
	if got := historyCount(); got != 2 {
		t.Fatalf("expected 2 epss history rows after a new day, got %d", got)
	}
}

// TestHydrateVulnWithNullTimestamps proves that a vuln ingested WITHOUT a
// published/modified date (common for KEV- or EPSS-only records, which store
// those columns as NULL) hydrates back without error and surfaces zero
// timestamps — rather than failing the read (and 500-ing the intel API) by
// scanning NULL into a non-pointer time.Time.
func TestHydrateVulnWithNullTimestamps(t *testing.T) {
	ctx := context.Background()
	st := newIntelTestStore(t)
	is := New(st)

	// A minimal CVE-keyed record with zero Published/Modified -> persisted NULL.
	v := canonical.CanonicalVuln{
		ID:       "CVE-2024-5000",
		Aliases:  []string{"CVE-2024-5000"},
		Summary:  "kev-only record with no published date",
		Severity: canonical.SevHigh,
		Sources:  []string{"kev"},
	}
	if err := is.UpsertVuln(ctx, v, time.Time{}, 0); err != nil {
		t.Fatalf("UpsertVuln: %v", err)
	}

	// Confirm the columns really are NULL (not a zero timestamp) so the test
	// exercises the NULL-scan path.
	var nullPublished, nullModified bool
	if err := st.Pool().QueryRow(ctx,
		`SELECT published IS NULL, modified IS NULL FROM intel_vulnerabilities WHERE id=$1`,
		v.ID).Scan(&nullPublished, &nullModified); err != nil {
		t.Fatalf("probe nulls: %v", err)
	}
	if !nullPublished || !nullModified {
		t.Fatalf("expected published/modified to be NULL, got published-null=%v modified-null=%v", nullPublished, nullModified)
	}

	got, err := is.VulnByID(ctx, v.ID)
	if err != nil {
		t.Fatalf("VulnByID with NULL timestamps must not error: %v", err)
	}
	if !got.Published.IsZero() || !got.Modified.IsZero() {
		t.Fatalf("expected zero Published/Modified, got %v / %v", got.Published, got.Modified)
	}
	if got.ID != v.ID || got.Summary != v.Summary {
		t.Fatalf("hydrated vuln mismatch: %+v", got)
	}

	// AllVulns must also tolerate the NULL timestamps.
	if _, err := is.AllVulns(ctx); err != nil {
		t.Fatalf("AllVulns with a NULL-timestamp record must not error: %v", err)
	}
}

// TestRecordEPSSHistoryPreservesMergedAttributes proves that recording EPSS
// history for a CVE does NOT clobber the merged KEV flag / CVSS / summary that a
// prior full upsert established. It writes a merged record (KEV + CVSS +
// summary), then records an EPSS-only daily score for the same id, and asserts
// the merged attributes survive while the current epss is refreshed and a daily
// history row is appended.
func TestRecordEPSSHistoryPreservesMergedAttributes(t *testing.T) {
	ctx := context.Background()
	st := newIntelTestStore(t)
	is := New(st)

	const id = "CVE-2024-7000"
	merged := canonical.CanonicalVuln{
		ID:             id,
		Aliases:        []string{id},
		Summary:        "merged OSV+KEV record",
		CVSS:           canonical.CVSS{Score: 9.1, Vector: "CVSS:3.1/AV:N", Version: "3.1"},
		Severity:       canonical.SevCritical,
		KnownExploited: true,
		Sources:        []string{"osv", "kev"},
	}
	if err := is.UpsertVuln(ctx, merged, time.Time{}, 0); err != nil {
		t.Fatalf("UpsertVuln merged: %v", err)
	}

	// The EPSS-only shape of the same CVE: empty summary, zero CVSS, KEV false.
	epssOnly := canonical.CanonicalVuln{
		ID:      id,
		Aliases: []string{id},
		EPSS:    0.88,
		Sources: []string{"epss"},
	}
	scoredOn := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := is.RecordEPSSHistory(ctx, []canonical.CanonicalVuln{epssOnly}, scoredOn); err != nil {
		t.Fatalf("RecordEPSSHistory: %v", err)
	}

	got, err := is.VulnByID(ctx, id)
	if err != nil {
		t.Fatalf("VulnByID: %v", err)
	}
	if !got.KnownExploited {
		t.Fatalf("known_exploited was clobbered by the EPSS-history pass")
	}
	if got.CVSS.Score != 9.1 {
		t.Fatalf("CVSS was clobbered: got %v, want 9.1", got.CVSS.Score)
	}
	if got.Summary != "merged OSV+KEV record" {
		t.Fatalf("summary was clobbered: got %q", got.Summary)
	}
	if got.EPSS != 0.88 {
		t.Fatalf("current epss not refreshed: got %v, want 0.88", got.EPSS)
	}

	// A daily history row for scoredOn exists, and re-running the same day stays
	// at one row (idempotent).
	historyCount := func() int {
		var n int
		if err := st.Pool().QueryRow(ctx,
			`SELECT count(*) FROM intel_epss_history WHERE vuln_id=$1`, id).Scan(&n); err != nil {
			t.Fatalf("count epss history: %v", err)
		}
		return n
	}
	if got := historyCount(); got != 1 {
		t.Fatalf("expected 1 epss history row, got %d", got)
	}
	if err := is.RecordEPSSHistory(ctx, []canonical.CanonicalVuln{epssOnly}, scoredOn); err != nil {
		t.Fatalf("RecordEPSSHistory (re-run): %v", err)
	}
	if got := historyCount(); got != 1 {
		t.Fatalf("expected epss history to stay at 1 row on same-day re-run, got %d", got)
	}
}
