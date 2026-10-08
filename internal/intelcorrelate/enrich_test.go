package intelcorrelate

import (
	"context"
	"testing"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// fakeGraphWriter captures what Enrich syncs so tests can assert attributes and
// the evidence invariant without a live Memgraph. It only records; it performs
// no graph writes.
type fakeGraphWriter struct {
	entities []store.GraphEntity
	edges    []store.GraphEdge
}

func (f *fakeGraphWriter) SyncEntities(_ context.Context, entities []store.GraphEntity) (int, error) {
	f.entities = append(f.entities, entities...)
	return len(entities), nil
}

func (f *fakeGraphWriter) SyncEdges(_ context.Context, edges []store.GraphEdge) (int, error) {
	f.edges = append(f.edges, edges...)
	return len(edges), nil
}

func TestEnrich_NodeAndEdgeCarryAttributesNoEvidence(t *testing.T) {
	cand := Candidate{
		AssetID:    "package:pkg:pypi/requests@2.30.0",
		AssetKind:  model.KindPackage,
		VulnID:     "CVE-2024-0001",
		IntelRef:   "CVE-2024-0001",
		MatchBasis: BasisPurlRange,
	}
	vulns := map[string]canonical.CanonicalVuln{
		"CVE-2024-0001": {
			ID:             "CVE-2024-0001",
			Aliases:        []string{"CVE-2024-0001"},
			Severity:       canonical.SevHigh,
			CVSS:           canonical.CVSS{Score: 7.5},
			EPSS:           0.42,
			KnownExploited: true,
		},
	}

	w := &fakeGraphWriter{}
	res, err := Enrich(context.Background(), w, []Candidate{cand}, vulns)
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 1 || res.Edges != 1 {
		t.Fatalf("expected 1 node + 1 edge synced, got %+v", res)
	}

	// --- Vulnerability node ---
	if len(w.entities) != 1 {
		t.Fatalf("expected 1 vulnerability node, got %d", len(w.entities))
	}
	node := w.entities[0]
	if node.Kind != model.KindVulnerability {
		t.Fatalf("node kind = %q, want %q", node.Kind, model.KindVulnerability)
	}
	// Keyed with the resolver's vuln: convention so it MERGEs onto the existing node.
	if node.CanonicalID != "vuln:CVE-2024-0001" {
		t.Fatalf("node canonical id = %q, want vuln:CVE-2024-0001", node.CanonicalID)
	}
	if len(node.EvidenceIDs) != 0 {
		t.Fatalf("node EvidenceIDs must be empty (no fabricated evidence), got %v", node.EvidenceIDs)
	}
	assertVulnAttrs(t, "node", node.Attributes)

	// --- HAS_VULN edge ---
	if len(w.edges) != 1 {
		t.Fatalf("expected 1 HAS_VULN edge, got %d", len(w.edges))
	}
	edge := w.edges[0]
	if edge.Relation != model.RelHasVuln {
		t.Fatalf("edge relation = %q, want %q", edge.Relation, model.RelHasVuln)
	}
	if edge.SrcID != cand.AssetID {
		t.Fatalf("edge src = %q, want %q", edge.SrcID, cand.AssetID)
	}
	if edge.DstID != "vuln:CVE-2024-0001" {
		t.Fatalf("edge dst = %q, want vuln:CVE-2024-0001", edge.DstID)
	}
	if len(edge.EvidenceIDs) != 0 {
		t.Fatalf("edge EvidenceIDs must be empty (annotates existing asset, no raw_event), got %v", edge.EvidenceIDs)
	}
	assertVulnAttrs(t, "edge", edge.Attributes)
}

// assertVulnAttrs checks the required intel attributes and, critically, that
// intel_ref cites the intel vuln id and is never a raw_event id.
func assertVulnAttrs(t *testing.T, where string, attrs map[string]any) {
	t.Helper()
	if got := attrs["intel_ref"]; got != "CVE-2024-0001" {
		t.Fatalf("%s intel_ref = %v, want the intel vuln id CVE-2024-0001 (never a raw_event id)", where, got)
	}
	if got := attrs["severity"]; got != "high" {
		t.Fatalf("%s severity = %v, want high", where, got)
	}
	if got := attrs["cvss"]; got != 7.5 {
		t.Fatalf("%s cvss = %v, want 7.5", where, got)
	}
	if got := attrs["epss"]; got != 0.42 {
		t.Fatalf("%s epss = %v, want 0.42", where, got)
	}
	if got := attrs["known_exploited"]; got != true {
		t.Fatalf("%s known_exploited = %v, want true", where, got)
	}
}

func TestEnrich_IdempotentAndDeduped(t *testing.T) {
	vulns := map[string]canonical.CanonicalVuln{
		"CVE-A": {ID: "CVE-A", Aliases: []string{"CVE-A"}, Severity: canonical.SevMedium},
		"CVE-B": {ID: "CVE-B", Aliases: []string{"CVE-B"}, Severity: canonical.SevLow},
	}
	// Duplicate candidate for the same (asset, vuln) plus a second vuln on the
	// same asset. Duplicates must collapse; two distinct vulns -> two nodes.
	cands := []Candidate{
		{AssetID: "package:x", AssetKind: model.KindPackage, VulnID: "CVE-B", IntelRef: "CVE-B", MatchBasis: BasisPurlRange},
		{AssetID: "package:x", AssetKind: model.KindPackage, VulnID: "CVE-A", IntelRef: "CVE-A", MatchBasis: BasisPurlRange},
		{AssetID: "package:x", AssetKind: model.KindPackage, VulnID: "CVE-A", IntelRef: "CVE-A", MatchBasis: BasisPurlRange},
	}

	w := &fakeGraphWriter{}
	res, err := Enrich(context.Background(), w, cands, vulns)
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 2 || res.Edges != 2 {
		t.Fatalf("expected 2 nodes + 2 edges after dedupe, got %+v", res)
	}
	// Deterministic order: sorted by canonical id -> CVE-A before CVE-B.
	if w.entities[0].CanonicalID != "vuln:CVE-A" || w.entities[1].CanonicalID != "vuln:CVE-B" {
		t.Fatalf("nodes not in deterministic order: %q, %q",
			w.entities[0].CanonicalID, w.entities[1].CanonicalID)
	}

	// Re-run with the same inputs must yield identical writes (idempotent).
	w2 := &fakeGraphWriter{}
	if _, err := Enrich(context.Background(), w2, cands, vulns); err != nil {
		t.Fatal(err)
	}
	if len(w2.entities) != len(w.entities) || len(w2.edges) != len(w.edges) {
		t.Fatalf("re-run produced different write counts: nodes %d/%d edges %d/%d",
			len(w2.entities), len(w.entities), len(w2.edges), len(w.edges))
	}
	for i := range w.entities {
		if w2.entities[i].CanonicalID != w.entities[i].CanonicalID {
			t.Fatalf("re-run node %d differs: %q vs %q", i, w2.entities[i].CanonicalID, w.entities[i].CanonicalID)
		}
	}
}

func TestEnrich_SkipsEmptyAssetOrVuln(t *testing.T) {
	w := &fakeGraphWriter{}
	res, err := Enrich(context.Background(), w, []Candidate{
		{AssetID: "", VulnID: "CVE-X", IntelRef: "CVE-X"},
		{AssetID: "package:y", VulnID: "", IntelRef: ""},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 0 || res.Edges != 0 {
		t.Fatalf("expected nothing synced for empty asset/vuln, got %+v", res)
	}
}
