//go:build integration

// This file implements the optional test task 14.4: a testcontainers +
// Memgraph (and Postgres) integration test that exercises the full correlation
// path end to end and proves Property 2 — correlation never fabricates
// evidence.
//
// It seeds a customer-ingested Package asset into a real Memgraph, runs the
// Matcher (New) against an in-memory IntelSource, enriches the graph (Enrich),
// then asserts:
//
//   - a HAS_VULN edge now links the package asset to the Vulnerability node,
//     carrying cvss / epss / known_exploited / severity / intel_ref, with
//     intel_ref equal to the intel vuln id;
//   - CRITICAL (Property 2 / Req 18.5): the Postgres `evidence` table row count
//     is identical before and after correlation + enrichment — correlation
//     wrote NO evidence rows; and the graph-side HAS_VULN edge and Vulnerability
//     node carry empty evidence_ids.
//
// Postgres is required (not just Memgraph) because the invariant under test is
// about the `evidence` table, which only exists in Postgres after migration.
package intelcorrelate

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// newTestWriter starts a disposable Memgraph (no auth), opens a *graph.Writer,
// and ensures the schema. It mirrors the harness in
// internal/graph/graph_integration_test.go, replicated here because that
// helper is unexported in package graph.
func newTestWriter(t *testing.T) *graph.Writer {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "memgraph/memgraph:2.18.1",
		Cmd:          []string{"--bolt-port=7687", "--log-level=WARNING"},
		ExposedPorts: []string{"7687/tcp"},
		WaitingFor:   wait.ForListeningPort("7687/tcp").WithStartupTimeout(60 * time.Second),
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	if err != nil {
		t.Fatalf("start memgraph: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	host, _ := ctr.Host(ctx)
	port, _ := ctr.MappedPort(ctx, "7687")
	uri := "bolt://" + host + ":" + port.Port()

	// Retry connect until bolt is ready.
	var w *graph.Writer
	deadline := time.Now().Add(30 * time.Second)
	for {
		w, err = graph.Open(ctx, uri, "", "")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("open memgraph: %v", err)
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() { _ = w.Close(ctx) })
	if err := w.EnsureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return w
}

// newTestStore spins up a disposable Postgres and returns a migrated store, so
// the `evidence` table exists for the Property 2 assertion. It mirrors the
// harness in internal/store/store_integration_test.go.
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
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// querySingle runs a read-only Cypher query expected to return exactly one
// record and returns it as a map. It uses the exported QueryRows read path on
// *graph.Writer (the same path the matcher uses), since package graph's own
// single-record helper is unexported.
func querySingle(ctx context.Context, t *testing.T, w *graph.Writer, cypher string) map[string]any {
	t.Helper()
	rows, err := w.QueryRows(ctx, cypher, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 record, got %d (%+v)", len(rows), rows)
	}
	return rows[0]
}

// evidenceCount returns the current row count of the Postgres `evidence` table.
func evidenceCount(ctx context.Context, t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM evidence`).Scan(&n); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	return n
}

// TestCorrelateEnrichNeverFabricatesEvidence seeds a Package asset in Memgraph,
// correlates it against an in-memory intel corpus, enriches the graph, and
// asserts both the enrichment outcome (HAS_VULN edge + attributes) and
// Property 2 (the evidence table is unchanged; graph evidence_ids are empty).
func TestCorrelateEnrichNeverFabricatesEvidence(t *testing.T) {
	ctx := context.Background()

	w := newTestWriter(t)
	st := newTestStore(t)

	const (
		purlName  = "pkg:pypi/requests"
		purlFull  = "pkg:pypi/requests@2.30.0"
		assetID   = "package:pkg:pypi/requests@2.30.0"
		vulnID    = "CVE-2024-0001"
		vulnNode  = "vuln:CVE-2024-0001"
		assetEvID = "ev-customer-sbom-1"
	)

	// 1. Seed a customer-ingested Package asset into Memgraph via the
	//    allow-listed write path. It carries a (fake) customer evidence id and
	//    source to mimic an SBOM-derived asset — correlation must not disturb
	//    this, and must not add any Postgres evidence of its own.
	seed := []store.GraphEntity{{
		CanonicalID: assetID,
		Kind:        model.KindPackage,
		Provider:    "image",
		Name:        purlFull,
		Attributes:  map[string]any{},
		EvidenceIDs: []string{assetEvID},
		Sources:     []string{"syft"},
	}}
	if _, err := w.SyncEntities(ctx, seed); err != nil {
		t.Fatalf("seed package: %v", err)
	}

	// 2. Record the evidence-table row count BEFORE correlation. A freshly
	//    migrated store starts empty, but we capture whatever it is so the
	//    assertion is a strict before/after equality, not a hard-coded 0.
	before := evidenceCount(ctx, t, st)

	// 3. Build a fake intel corpus with a vuln affecting pkg:pypi/requests in a
	//    range that contains 2.30.0, and run the matcher then the enrichment.
	v := canonical.CanonicalVuln{
		ID:      vulnID,
		Aliases: []string{vulnID},
		Summary: "requests TLS verification bypass",
		Affected: []canonical.AffectedPackage{{
			Purl:      purlName,
			Ecosystem: "PyPI",
			Ranges:    []canonical.SemverRange{{Introduced: "2.0.0", Fixed: "2.31.0"}},
		}},
		CVSS:           canonical.CVSS{Score: 7.5, Version: "3.1"},
		Severity:       canonical.SevHigh,
		KnownExploited: true,
		EPSS:           0.42,
	}
	intel := fakeIntel{vulns: []canonical.CanonicalVuln{v}}

	cands, err := New(w, intel).Correlate(ctx)
	if err != nil {
		t.Fatalf("correlate: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %+v", len(cands), cands)
	}
	if cands[0].AssetID != assetID || cands[0].VulnID != vulnID {
		t.Fatalf("unexpected candidate: %+v", cands[0])
	}

	vulnsMap := map[string]canonical.CanonicalVuln{vulnID: v}
	res, err := Enrich(ctx, w, cands, vulnsMap)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if res.Nodes != 1 || res.Edges != 1 {
		t.Fatalf("expected 1 node + 1 edge enriched, got %+v", res)
	}

	// 4. Assert a HAS_VULN edge now exists from the asset to the vuln node,
	//    carrying the intel attributes, and that intel_ref == the intel vuln id.
	//    coalesce(size(r.evidence_ids), 0) tolerates the property being absent
	//    (never written) as well as present-but-empty.
	// The graph writer promotes attribute-map entries to node/edge properties
	// prefixed with "attr_" (scalarProps), matching the rest of the model
	// (e.g. attr_is_ai_api). So the intel attributes land as attr_cvss,
	// attr_epss, attr_known_exploited, attr_severity, attr_intel_ref.
	rec := querySingle(ctx, t, w, `
		MATCH (p:Package {canonical_id:"`+assetID+`"})
		      -[r:HAS_VULN]->(vn:Vulnerability {canonical_id:"`+vulnNode+`"})
		RETURN r.attr_cvss AS cvss, r.attr_epss AS epss,
		       r.attr_known_exploited AS known_exploited, r.attr_severity AS severity,
		       r.attr_intel_ref AS intel_ref,
		       coalesce(size(r.evidence_ids), 0) AS edge_ev,
		       coalesce(size(vn.evidence_ids), 0) AS node_ev`)

	if got := rec["intel_ref"]; got != vulnID {
		t.Fatalf("intel_ref = %v, want %q", got, vulnID)
	}
	if got := rec["severity"]; got != string(canonical.SevHigh) {
		t.Fatalf("severity = %v, want %q", got, canonical.SevHigh)
	}
	if got, ok := rec["cvss"].(float64); !ok || got != 7.5 {
		t.Fatalf("cvss = %v (%T), want 7.5", rec["cvss"], rec["cvss"])
	}
	if got, ok := rec["epss"].(float64); !ok || got != 0.42 {
		t.Fatalf("epss = %v (%T), want 0.42", rec["epss"], rec["epss"])
	}
	if got := rec["known_exploited"]; got != true {
		t.Fatalf("known_exploited = %v, want true", got)
	}

	// 5. CRITICAL — Property 2 / Req 18.5: correlation + enrichment wrote NO
	//    rows to the Postgres evidence table.
	after := evidenceCount(ctx, t, st)
	if after != before {
		t.Fatalf("evidence table changed: before=%d after=%d (correlation must write no evidence)", before, after)
	}

	// And on the graph side the enriched edge and vuln node carry no evidence
	// ids — enrichment cites the intel record, it does not fabricate evidence.
	if ec := rec["edge_ev"].(int64); ec != 0 {
		t.Fatalf("HAS_VULN edge has %d evidence_ids, want 0", ec)
	}
	if nc := rec["node_ev"].(int64); nc != 0 {
		t.Fatalf("Vulnerability node has %d evidence_ids, want 0", nc)
	}

	// Sanity: the seeded asset's own evidence id survived untouched (correlation
	// annotates, it does not rewrite the customer asset's provenance).
	assetRec := querySingle(ctx, t, w, `
		MATCH (p:Package {canonical_id:"`+assetID+`"})
		RETURN size(p.evidence_ids) AS ec`)
	if ec := assetRec["ec"].(int64); ec != 1 {
		t.Fatalf("seeded asset evidence_ids = %d, want 1 (customer provenance preserved)", ec)
	}
}
