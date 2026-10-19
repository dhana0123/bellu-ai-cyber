//go:build integration

package replay

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
	"github.com/bellu/bellu-ai-cyber/internal/temporal"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/classify"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/infer"
)

// TestDerivedOverlayProjection is the derived-overlay projection test for the
// replay engine (Req 8.3, 9.3). It projects a single run that carries one
// inferred edge and one datastore classification, then asserts — by reading
// Memgraph directly with Cypher — that the overlay landed with the exact
// properties the two requirements name:
//
//   - Req 8.3: the projected inferred edge carries inferred=true and carries
//     its confidence and evidence (plus the derivation provenance produced_by /
//     origin that rides alongside them).
//   - Req 9.3: the classified node carries the three namespaced properties
//     data_class, data_class_confidence, and classified_by.
//
// It then runs the authoritative recovery path (RebuildAsOf, which folds the
// same two logs from scratch) against a fresh projection and asserts the same
// overlay properties reproduce — the overlay is idempotent across the
// incremental apply and the full rebuild.
//
// The test writes the two logs directly (reusing the determinism test's log
// writers) so it isolates the replay overlay rather than exercising the change
// engine's diff. It skips cleanly when no container runtime is available.
func TestDerivedOverlayProjection(t *testing.T) {
	ctx := context.Background()

	st := startPostgres(t)

	// --- craft one run: three entities + one inferred edge + one classification ---
	//
	// The datastore is the inferred-edge target and the classification subject,
	// so a single run exercises both overlay shapes. The inferred edge is
	// workload --DEPENDS_ON--> datastore with a sub-1.0 confidence and evidence
	// ids; the classification tags the datastore "pii".
	t0 := baseTime
	const (
		runID         = "overlay-run-1"
		edgeConf      = 0.8
		classConf     = 0.9
		producer      = "env-var-dependency"
		classifiedBy  = "rule:naming"
		dataClassName = "pii"
	)
	evidence := []string{"ev-1", "ev-2"}

	observedRows := []observedRow{
		{changeType: temporal.ChangeEntityCreated, kind: model.KindWorkload, provider: "aws", name: "wl-1", canonical: wlID,
			attrs: map[string]any{"kind": "Workload", "provider": "aws", "name": "wl-1"}, eventTime: t0},
		{changeType: temporal.ChangeEntityCreated, kind: model.KindDataStore, provider: "aws", name: "bucket-1", canonical: dsID,
			attrs: map[string]any{"kind": "DataStore", "provider": "aws", "name": "bucket-1"}, eventTime: t0},
	}
	seedCollectionRun(ctx, t, st, runID)
	writeObserved(ctx, t, st, org, runID, observedRows)
	writeDerivedWithEvidence(ctx, t, st, org, runID, derivedOverlayRow{
		kind: "inferred_edge", changeType: "inferred.added", canonical: wlID,
		relation: model.RelDependsOn, dstID: dsID, confidence: edgeConf,
		producer: producer, origin: infer.OriginRule, evidence: evidence,
	})
	writeDerivedWithEvidence(ctx, t, st, org, runID, derivedOverlayRow{
		kind: "classification", changeType: "classified", canonical: dsID,
		class: dataClassName, confidence: classConf, producer: classifiedBy,
	})

	changes := toChanges(observedRows)
	derived := []DerivedChange{
		InferredEdge(infer.InferredEdge{
			SrcID: wlID, Relation: model.RelDependsOn, DstID: dsID,
			Confidence: edgeConf, Evidence: evidence, Origin: infer.OriginRule, Producer: producer,
		}),
		Classification(classify.DataClass{
			CanonicalID: dsID, Class: dataClassName, Confidence: classConf, ClassifiedBy: classifiedBy,
		}),
	}

	wantEdge := inferredEdgeProps{
		inferred:    true,
		confidence:  edgeConf,
		evidenceIDs: evidence,
		producedBy:  producer,
		origin:      infer.OriginRule,
	}
	wantClass := classificationProps{
		dataClass:    dataClassName,
		confidence:   classConf,
		classifiedBy: classifiedBy,
	}

	// --- PATH A: incremental ApplyRun ---
	writerA, okA := startMemgraph(t)
	if !okA {
		t.Skip("no container runtime available for Memgraph; skipping derived-overlay projection test")
	}
	rbA := NewRebuilder(st, writerA)
	if err := rbA.ApplyRun(ctx, org, changes, derived); err != nil {
		t.Fatalf("ApplyRun: %v", err)
	}

	// Req 8.3 — inferred edge carries inferred=true + confidence + evidence.
	gotEdgeA := readInferredEdge(ctx, t, writerA, wlID, model.RelDependsOn, dsID)
	assertInferredEdge(t, "ApplyRun", gotEdgeA, wantEdge)

	// Req 9.3 — classified node carries the three namespaced properties.
	gotClassA := readClassification(ctx, t, writerA, dsID)
	assertClassification(t, "ApplyRun", gotClassA, wantClass)

	// --- PATH B: full rebuild from the same two logs into a fresh projection ---
	writerB, okB := startMemgraph(t)
	if !okB {
		t.Skip("no container runtime available for Memgraph; skipping derived-overlay projection test")
	}
	rbB := NewRebuilder(st, writerB)
	cutoff := t0.Add(time.Hour) // strictly after the crafted event_time
	if err := rbB.RebuildAsOf(ctx, org, cutoff, cutoff); err != nil {
		t.Fatalf("RebuildAsOf: %v", err)
	}

	// The rebuild reproduces the same overlay properties (idempotent overlay).
	gotEdgeB := readInferredEdge(ctx, t, writerB, wlID, model.RelDependsOn, dsID)
	assertInferredEdge(t, "RebuildAsOf", gotEdgeB, wantEdge)

	gotClassB := readClassification(ctx, t, writerB, dsID)
	assertClassification(t, "RebuildAsOf", gotClassB, wantClass)
}

// ---------------------------------------------------------------------------
// expected / observed overlay property shapes
// ---------------------------------------------------------------------------

type inferredEdgeProps struct {
	inferred    bool
	confidence  float64
	evidenceIDs []string
	producedBy  string
	origin      string
}

type classificationProps struct {
	dataClass    string
	confidence   float64
	classifiedBy string
}

func assertInferredEdge(t *testing.T, path string, got, want inferredEdgeProps) {
	t.Helper()
	if got.inferred != want.inferred {
		t.Errorf("[%s] inferred=%v, want %v", path, got.inferred, want.inferred)
	}
	if got.confidence != want.confidence {
		t.Errorf("[%s] confidence=%v, want %v", path, got.confidence, want.confidence)
	}
	if !equalStringSets(got.evidenceIDs, want.evidenceIDs) {
		t.Errorf("[%s] evidence_ids=%v, want %v", path, got.evidenceIDs, want.evidenceIDs)
	}
	if got.producedBy != want.producedBy {
		t.Errorf("[%s] produced_by=%q, want %q", path, got.producedBy, want.producedBy)
	}
	if got.origin != want.origin {
		t.Errorf("[%s] origin=%q, want %q", path, got.origin, want.origin)
	}
}

func assertClassification(t *testing.T, path string, got, want classificationProps) {
	t.Helper()
	if got.dataClass != want.dataClass {
		t.Errorf("[%s] data_class=%q, want %q", path, got.dataClass, want.dataClass)
	}
	if got.confidence != want.confidence {
		t.Errorf("[%s] data_class_confidence=%v, want %v", path, got.confidence, want.confidence)
	}
	if got.classifiedBy != want.classifiedBy {
		t.Errorf("[%s] classified_by=%q, want %q", path, got.classifiedBy, want.classifiedBy)
	}
}

// ---------------------------------------------------------------------------
// Cypher readers (read the overlay back directly, not via the snapshot)
// ---------------------------------------------------------------------------

// readInferredEdge reads the overlay properties off the projected edge: the
// inferred marker, confidence, evidence ids, and the derivation provenance
// (produced_by / origin) the overlay stamps alongside them.
func readInferredEdge(ctx context.Context, t *testing.T, w *graph.Writer, src string, rel model.Relation, dst string) inferredEdgeProps {
	t.Helper()
	sess := w.Driver().NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer sess.Close(ctx)

	// Overlay attributes land on the edge prefixed with "attr_" (graph.scalarProps
	// namespaces attribute keys so they never clobber reserved edge fields), so
	// inferred=true / produced_by / origin are read as attr_inferred etc.
	// Confidence and evidence_ids are first-class edge fields (no prefix).
	res, err := sess.Run(ctx,
		`MATCH (a:Entity {canonical_id: $src})-[r]->(b:Entity {canonical_id: $dst})
		 WHERE type(r) = $rel
		 RETURN coalesce(r.attr_inferred,false) AS inferred, r.confidence AS confidence,
		        r.evidence_ids AS evidence_ids, r.attr_produced_by AS produced_by, r.attr_origin AS origin`,
		map[string]any{"src": src, "dst": dst, "rel": string(rel)})
	if err != nil {
		t.Fatalf("read inferred edge: %v", err)
	}
	if !res.Next(ctx) {
		t.Fatalf("inferred edge %s-[%s]->%s not found in projection", src, rel, dst)
	}
	m := res.Record().AsMap()
	if err := res.Err(); err != nil {
		t.Fatalf("read inferred edge: %v", err)
	}
	return inferredEdgeProps{
		inferred:    asBool(m["inferred"]),
		confidence:  asFloat(m["confidence"]),
		evidenceIDs: asStringSlice(m["evidence_ids"]),
		producedBy:  asString(m["produced_by"]),
		origin:      asString(m["origin"]),
	}
}

// readClassification reads the three namespaced classification properties off
// the projected node.
func readClassification(ctx context.Context, t *testing.T, w *graph.Writer, id string) classificationProps {
	t.Helper()
	sess := w.Driver().NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer sess.Close(ctx)

	res, err := sess.Run(ctx,
		`MATCH (n:Entity {canonical_id: $id})
		 RETURN n.data_class AS data_class, n.data_class_confidence AS data_class_confidence,
		        n.classified_by AS classified_by`,
		map[string]any{"id": id})
	if err != nil {
		t.Fatalf("read classification: %v", err)
	}
	if !res.Next(ctx) {
		t.Fatalf("classified node %s not found in projection", id)
	}
	m := res.Record().AsMap()
	if err := res.Err(); err != nil {
		t.Fatalf("read classification: %v", err)
	}
	return classificationProps{
		dataClass:    asString(m["data_class"]),
		confidence:   asFloat(m["data_class_confidence"]),
		classifiedBy: asString(m["classified_by"]),
	}
}

// ---------------------------------------------------------------------------
// log writer carrying evidence ids
// ---------------------------------------------------------------------------

// derivedOverlayRow is a crafted derived_events row that also carries evidence
// ids. The determinism test's writeDerived omits evidence_ids (it does not
// assert on them); this overlay test needs them written so the fold path
// reproduces r.evidence_ids on rebuild.
type derivedOverlayRow struct {
	kind       string // "inferred_edge" | "classification"
	changeType string
	canonical  string
	relation   model.Relation
	dstID      string
	class      string
	confidence float64
	producer   string
	origin     string
	evidence   []string
}

func writeDerivedWithEvidence(ctx context.Context, t *testing.T, st *store.Store, org int64, runID string, r derivedOverlayRow) {
	t.Helper()
	var relation, dstID any
	attrs := map[string]any{}
	if r.kind == "inferred_edge" {
		relation = string(r.relation)
		dstID = r.dstID
	} else {
		attrs["data_class"] = r.class
	}
	attrsJSON, err := json.Marshal(attrs)
	if err != nil {
		t.Fatalf("marshal derived attrs: %v", err)
	}
	// evidence_ids is NOT NULL (defaults to '{}'); pass an empty slice rather
	// than nil for rows that carry no evidence (e.g. classifications).
	evidence := r.evidence
	if evidence == nil {
		evidence = []string{}
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO derived_events
		   (org_id, run_id, kind, change_type, origin, producer, canonical_id,
		    relation, dst_id, attributes, confidence, evidence_ids, event_time, recorded_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`,
		org, runID, r.kind, r.changeType, nonEmpty(r.origin, "rule"), r.producer, r.canonical,
		relation, dstID, attrsJSON, r.confidence, evidence, baseTime); err != nil {
		t.Fatalf("write derived row (%s %s): %v", r.changeType, r.canonical, err)
	}
}

// ---------------------------------------------------------------------------
// neo4j value coercion helpers (asString / asBool live in the determinism test)
// ---------------------------------------------------------------------------

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

func asStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

// equalStringSets compares two string slices order-independently.
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ca := append([]string(nil), a...)
	cb := append([]string(nil), b...)
	sort.Strings(ca)
	sort.Strings(cb)
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

// Validates: Requirements 8.3, 9.3
