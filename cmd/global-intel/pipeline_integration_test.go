//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/global-intel/correlation"
	"github.com/bellu/bellu-ai-cyber/global-intel/database"
	"github.com/bellu/bellu-ai-cyber/global-intel/fetch"
	epssfetch "github.com/bellu/bellu-ai-cyber/global-intel/fetch/epss"
	kevfetch "github.com/bellu/bellu-ai-cyber/global-intel/fetch/kev"
	osvfetch "github.com/bellu/bellu-ai-cyber/global-intel/fetch/osv"
	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// knownVulnID is the canonical id that every Phase-1 source in the fixtures
// references, so the deduper merges the OSV record (CVSS + affected package),
// the KEV entry (KnownExploited), and the EPSS row (EPSS score) into a single
// canonical vulnerability. Property 3 is asserted against this id's
// first_seen/last_seen across two runs.
const knownVulnID = "CVE-2024-9999"

// epssScoreDate is the EPSS scored date baked into the fixture CSV header and
// pinned via the EPSS fetcher's clock so the file date and cursor are stable
// across both runs.
const epssScoreDate = "2024-05-01"

// osvFixture is a tiny OSV bulk payload: a JSON array of two OSV entries. The
// OSV parser sniffs the leading '[' and decodes the array (the fetcher streams
// the exact bytes and the parser reads them back, so an array body works
// end-to-end without any real zip). The first entry shares knownVulnID with
// the KEV/EPSS fixtures so the three sources merge into one canonical vuln.
const osvFixture = `[
  {
    "id": "CVE-2024-9999",
    "aliases": ["GHSA-aaaa-bbbb-cccc"],
    "summary": "Remote code execution in example parser",
    "affected": [
      {
        "package": {"ecosystem": "PyPI", "name": "examplepkg", "purl": "pkg:pypi/examplepkg"},
        "ranges": [
          {"type": "ECOSYSTEM", "events": [{"introduced": "1.0.0"}, {"fixed": "1.4.2"}]}
        ]
      }
    ],
    "severity": [{"type": "CVSS_V3", "score": "9.8"}],
    "published": "2024-05-01T00:00:00Z",
    "modified": "2024-05-02T00:00:00Z"
  },
  {
    "id": "CVE-2024-8888",
    "aliases": ["PYSEC-2024-42"],
    "summary": "Denial of service via crafted input",
    "affected": [
      {
        "package": {"ecosystem": "PyPI", "name": "otherpkg", "purl": "pkg:pypi/otherpkg"},
        "ranges": [
          {"type": "ECOSYSTEM", "events": [{"introduced": "2.0.0"}, {"fixed": "2.1.0"}]}
        ]
      }
    ],
    "severity": [{"type": "CVSS_V3", "score": "7.5"}]
  }
]`

// kevFixture is a minimal CISA KEV catalog carrying a non-empty catalogVersion
// (the fetcher's cursor) and a single vulnerability keyed by knownVulnID, so
// the merged canonical vuln carries KnownExploited=true.
const kevFixture = `{
  "title": "CISA Catalog of Known Exploited Vulnerabilities",
  "catalogVersion": "2024.05.01",
  "dateReleased": "2024-05-01T00:00:00.0000Z",
  "count": 1,
  "vulnerabilities": [
    {
      "cveID": "CVE-2024-9999",
      "vendorProject": "Example",
      "product": "ExampleParser",
      "vulnerabilityName": "Example Parser RCE",
      "dateAdded": "2024-05-01",
      "shortDescription": "Known-exploited RCE in the example parser.",
      "requiredAction": "Apply updates.",
      "dueDate": "2024-05-22",
      "knownRansomwareCampaignUse": "Unknown"
    }
  ]
}`

// epssFixture is a tiny EPSS daily CSV: the comment header carries the
// score_date (used both as the per-row ScoredOn and, via epssScoredDate, as the
// EPSS-history key) followed by the column header and two data rows. It is
// served as plain CSV; the parser treats a non-gzip payload as plain text.
const epssFixture = "#model_version:v2024.05.01,score_date:" + epssScoreDate + "T00:00:00+0000\n" +
	"cve,epss,percentile\n" +
	"CVE-2024-9999,0.73500,0.98000\n" +
	"CVE-2024-8888,0.01200,0.42000\n"

// newPipelineTestStore spins up a disposable Postgres, applies every migration
// (including 00005_intel), and returns a migrated store. Mirrors the harness in
// internal/store/store_integration_test.go and database/intel_store_integration_test.go
// so the end-to-end pipeline runs against the same database access path as the
// rest of the system.
func newPipelineTestStore(t *testing.T) *store.Store {
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

// fixtureServer stands up an httptest.Server serving the canned OSV, KEV, and
// EPSS payloads offline. The OSV bulk archive is served at the ecosystem path
// the fetcher requests ("/PyPI/all.zip"); the KEV catalog at "/kev.json"; and
// any EPSS daily-file path ("/epss_scores-YYYY-MM-DD.csv.gz") returns the plain
// CSV fixture (the EPSS parser detects gzip by magic bytes and falls back to
// plain CSV otherwise).
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/PyPI/all.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(osvFixture))
	})
	mux.HandleFunc("/kev.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(kevFixture))
	})
	// Serve every EPSS daily file path with the same plain CSV fixture.
	mux.HandleFunc("/epss/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(epssFixture))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestPipeline wires a pipeline whose fetchers point at the offline fixture
// server; the parsers, canonicalizers, deduper, and intel store are the real
// Phase-1 components, so the test exercises the genuine
// fetch -> parse -> normalize -> correlate path.
func newTestPipeline(t *testing.T, srv *httptest.Server, rawDir string, intel *database.IntelStore, indexer pipelineIndexer) *pipeline {
	t.Helper()

	rs, err := raw.NewFSStore(rawDir)
	if err != nil {
		t.Fatalf("open raw store: %v", err)
	}

	// Pin the EPSS clock to the fixture's scored date so the fetcher requests a
	// stable daily file and the cursor is deterministic across both runs.
	epssClock := func() time.Time {
		return time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	}

	fetchers := map[string]fetch.Fetcher{
		"osv": &osvfetch.Fetcher{
			BaseURL:    srv.URL,
			HTTPClient: srv.Client(),
		},
		"kev": kevfetch.New(
			kevfetch.WithCatalogURL(srv.URL+"/kev.json"),
			kevfetch.WithHTTPClient(srv.Client()),
		),
		"epss": epssfetch.New(
			epssfetch.WithBaseURL(srv.URL+"/epss"),
			epssfetch.WithHTTPClient(srv.Client()),
			epssfetch.WithClock(epssClock),
		),
	}

	return &pipeline{
		mode:     modeDaily,
		rawDir:   rawDir,
		raw:      rs,
		intel:    intel,
		fetchers: fetchers,
		parsers:  newParsers(),
		canons:   newCanonicalizers(),
		deduper:  correlation.NewDeduper(),
		indexer:  indexer,
	}
}

// pipelineCounts is a snapshot of the three persisted intel tables whose row
// counts idempotence (Property 3) must leave unchanged on a no-op re-run.
// intel_fetch_runs is intentionally excluded: it accumulates one audit row per
// source per run, which is expected history, not a persisted-corpus change.
type pipelineCounts struct {
	vulns    int
	aliases  int
	affected int
}

func snapshotPipelineCounts(t *testing.T, st *store.Store) pipelineCounts {
	t.Helper()
	ctx := context.Background()
	var c pipelineCounts
	count := func(table string) int {
		var n int
		if err := st.Pool().QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	c.vulns = count("intel_vulnerabilities")
	c.aliases = count("intel_aliases")
	c.affected = count("intel_affected_packages")
	return c
}

func seenTimes(t *testing.T, st *store.Store, id string) (firstSeen, lastSeen time.Time) {
	t.Helper()
	if err := st.Pool().QueryRow(context.Background(),
		`SELECT first_seen, last_seen FROM intel_vulnerabilities WHERE id=$1`, id).
		Scan(&firstSeen, &lastSeen); err != nil {
		t.Fatalf("select seen times for %q: %v", id, err)
	}
	return firstSeen, lastSeen
}

// TestPhaseOnePipelineIdempotent proves Property 3 end-to-end (Requirement
// 18.4): running fetch -> parse -> normalize -> correlate twice over the same
// offline fixtures leaves every persisted intel_* table unchanged except
// last_seen on intel_vulnerabilities. first_seen is preserved and last_seen
// advances on the second run.
func TestPhaseOnePipelineIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newPipelineTestStore(t)
	intel := database.New(st)

	srv := fixtureServer(t)

	// First end-to-end run establishes the baseline persisted corpus. The raw
	// store directory is shared across both runs so the content-addressed store
	// is also exercised for idempotence (identical bytes -> same ref, no churn).
	rawDir := t.TempDir()

	indexer := &fakePipelineIndexer{}
	first := newTestPipeline(t, srv, rawDir, intel, indexer)
	if err := first.run(ctx); err != nil {
		t.Fatalf("first pipeline run: %v", err)
	}

	before := snapshotPipelineCounts(t, st)

	// Folded indexing: a successful run must have ensured the index and indexed
	// the persisted deduped corpus in the same task (no separate job). The
	// indexed set equals the vulns upserted this run.
	if indexer.ensured == 0 {
		t.Fatal("expected the run to ensure the search index")
	}
	totalIndexed := 0
	for _, batch := range indexer.indexedDocs {
		totalIndexed += len(batch)
	}
	if totalIndexed != before.vulns {
		t.Fatalf("indexed %d docs, want %d (the persisted corpus)", totalIndexed, before.vulns)
	}

	// Fixtures contribute two canonical vulns after dedup (CVE-2024-9999 merged
	// across osv+kev+epss, and CVE-2024-8888 from osv+epss). Guard the baseline
	// so a later fixture change that breaks merging is caught.
	if before.vulns != 2 {
		t.Fatalf("expected 2 canonical vulns after first run, got %d", before.vulns)
	}
	if before.aliases == 0 || before.affected == 0 {
		t.Fatalf("expected non-empty aliases/affected after first run, got %+v", before)
	}

	// The merged known vuln carries the EPSS score (from the EPSS source), the
	// KEV flag (from the KEV source), the CVSS score + summary (from the OSV
	// source), and the affected PyPI package (from the OSV source) — proving the
	// distinct Phase-1 sources were correlated into one canonical record AND that
	// the EPSS-history pass (RecordEPSSHistory) preserved the merged KEV/OSV
	// attributes rather than clobbering them with the EPSS-only record's empty
	// values.
	var (
		epss           *float64
		cvssScore      *float64
		knownExploited bool
		summary        string
	)
	if err := st.Pool().QueryRow(ctx,
		`SELECT epss, cvss_score, known_exploited, summary
		   FROM intel_vulnerabilities WHERE id=$1`, knownVulnID).
		Scan(&epss, &cvssScore, &knownExploited, &summary); err != nil {
		t.Fatalf("select known vuln attributes: %v", err)
	}
	if epss == nil || *epss == 0 {
		t.Fatalf("expected %s to carry an EPSS score after EPSS merge, got %v", knownVulnID, epss)
	}
	if !knownExploited {
		t.Fatalf("expected %s to keep known_exploited=true from KEV after the EPSS-history pass", knownVulnID)
	}
	if cvssScore == nil || *cvssScore == 0 {
		t.Fatalf("expected %s to keep its OSV CVSS score after the EPSS-history pass, got %v", knownVulnID, cvssScore)
	}
	if summary == "" {
		t.Fatalf("expected %s to keep its OSV summary after the EPSS-history pass", knownVulnID)
	}

	var affectedCount int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM intel_affected_packages WHERE vuln_id=$1 AND purl=$2`,
		knownVulnID, "pkg:pypi/examplepkg").Scan(&affectedCount); err != nil {
		t.Fatalf("select known vuln affected packages: %v", err)
	}
	if affectedCount == 0 {
		t.Fatalf("expected %s to carry its OSV affected package after merge", knownVulnID)
	}

	firstSeenBefore, lastSeenBefore := seenTimes(t, st, knownVulnID)

	// A tiny pause so now() on the re-run is strictly observable.
	time.Sleep(10 * time.Millisecond)

	// Second end-to-end run over the SAME fixtures. A fresh pipeline (reusing
	// the same raw dir and store) mirrors a scheduled re-run of the CLI.
	second := newTestPipeline(t, srv, rawDir, intel, &fakePipelineIndexer{})
	if err := second.run(ctx); err != nil {
		t.Fatalf("second pipeline run: %v", err)
	}

	after := snapshotPipelineCounts(t, st)
	if after != before {
		t.Fatalf("persisted intel_* counts changed on re-run: before=%+v after=%+v", before, after)
	}

	firstSeenAfter, lastSeenAfter := seenTimes(t, st, knownVulnID)
	if !firstSeenAfter.Equal(firstSeenBefore) {
		t.Fatalf("first_seen changed on re-run: before=%s after=%s", firstSeenBefore, firstSeenAfter)
	}
	if !lastSeenAfter.After(lastSeenBefore) {
		t.Fatalf("last_seen was not advanced on re-run: before=%s after=%s", lastSeenBefore, lastSeenAfter)
	}

	// intel_fetch_runs accumulates audit history (expected to grow): there must
	// be strictly more run rows after the second run than after the first.
	fetchRuns := func() int {
		var n int
		if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM intel_fetch_runs`).Scan(&n); err != nil {
			t.Fatalf("count intel_fetch_runs: %v", err)
		}
		return n
	}
	if got := fetchRuns(); got <= len(phaseOneSources) {
		t.Fatalf("expected intel_fetch_runs to accumulate audit rows across two runs, got %d", got)
	}
}
