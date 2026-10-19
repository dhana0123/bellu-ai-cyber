//go:build integration

package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
	"github.com/bellu/bellu-ai-cyber/internal/temporal"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/classify"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/infer"
)

// TestReplayDeterminism is Property 1 (replay determinism). For a crafted
// sequence of runs it proves two things:
//
//  1. rebuild(logs ≤ T) == applyRuns(logs ≤ T): the Memgraph projection built
//     by applying each run incrementally (Rebuilder.ApplyRun) is identical to
//     the projection produced by a single full fold of the same two logs
//     (Rebuilder.RebuildAsOf) — the authoritative recovery path yields the same
//     graph as the incremental path.
//  2. Memgraph holds no state not derivable from the two logs: every node and
//     relationship in the incremental projection traces back to an
//     observed_events / derived_events row for the crafted sequence (Req 7.4).
//
// The test writes the two logs directly (one crafted, deterministic sequence of
// observed facts + derived inferences/classifications) rather than running the
// change engine's diff, so it isolates the replay layer under test. The same
// crafted data drives both the incremental ApplyRun calls and the RebuildAsOf
// fold, so any divergence is a replay-determinism bug, not a fixture mismatch.
func TestReplayDeterminism(t *testing.T) {
	ctx := context.Background()

	st := startPostgres(t)

	// --- craft a deterministic sequence of runs into the two logs ---
	//
	// The sequence deliberately exercises every fold edge: a created entity, a
	// later attribute/state change to it, an added edge, a removed edge, and a
	// reference-counted deletion — plus a derived inferred edge and a datastore
	// classification. RebuildAsOf must fold all of these to the same end state
	// the incremental ApplyRun calls produced.
	runs := craftedRuns()
	for _, r := range runs {
		seedCollectionRun(ctx, t, st, r.runID)
		writeObserved(ctx, t, st, org, r.runID, r.observedRows)
		writeDerived(ctx, t, st, org, r.runID, r.derivedRows)
	}

	// --- PATH A: incremental apply, run by run, into projection A ---
	writerA, okA := startMemgraph(t)
	if !okA {
		t.Skip("no container runtime available for Memgraph; skipping replay-determinism test")
	}
	rbA := NewRebuilder(st, writerA)
	for _, r := range runs {
		if err := rbA.ApplyRun(ctx, org, r.changes, r.derived); err != nil {
			t.Fatalf("ApplyRun(%s): %v", r.runID, err)
		}
	}
	snapA := snapshotGraph(ctx, t, writerA)

	// --- PATH B: full fold of the same two logs into a fresh projection B ---
	writerB, okB := startMemgraph(t)
	if !okB {
		t.Skip("no container runtime available for Memgraph; skipping replay-determinism test")
	}
	rbB := NewRebuilder(st, writerB)
	now := time.Now().UTC().Add(time.Hour) // cutoff strictly after every crafted event_time
	if err := rbB.RebuildAsOf(ctx, org, now, now); err != nil {
		t.Fatalf("RebuildAsOf: %v", err)
	}
	snapB := snapshotGraph(ctx, t, writerB)

	// --- assertion 1: rebuild == applyRuns ---
	if !reflect.DeepEqual(snapA, snapB) {
		t.Fatalf("replay non-deterministic: incremental projection != full rebuild\n incremental=%s\n rebuild    =%s",
			snapA, snapB)
	}

	// --- assertion 2: Memgraph holds no state not derivable from the two logs ---
	derivable := derivableFromLogs(ctx, t, st, org)
	for _, n := range snapA.Nodes {
		if _, ok := derivable.nodes[n.CanonicalID]; !ok {
			t.Errorf("projected node %q is not derivable from the two logs", n.CanonicalID)
		}
	}
	for _, e := range snapA.Edges {
		key := edgeIdentity{src: e.Src, rel: e.Relation, dst: e.Dst, inferred: e.Inferred}
		if _, ok := derivable.edges[key]; !ok {
			t.Errorf("projected edge %v is not derivable from the two logs", key)
		}
	}

	// Guard against a vacuous pass: the crafted sequence must actually project
	// some graph, otherwise "equal and derivable" would hold trivially.
	if len(snapA.Nodes) == 0 {
		t.Fatal("precondition: crafted sequence projected no nodes")
	}
}

// ---------------------------------------------------------------------------
// crafted run sequence
// ---------------------------------------------------------------------------

const org = int64(42)

// observedRow is one crafted observed_events row plus the matching temporal
// Change the incremental path applies. Keeping both in one struct guarantees
// the log and the incremental input never drift apart.
type observedRow struct {
	changeType string
	kind       model.Kind
	provider   string
	name       string
	canonical  string
	relation   model.Relation
	dstID      string
	attrs      map[string]any
	eventTime  time.Time
}

// derivedRow is one crafted derived_events row plus the matching DerivedChange.
type derivedRow struct {
	kind       string // "inferred_edge" | "classification"
	changeType string
	canonical  string
	relation   model.Relation
	dstID      string
	class      string
	confidence float64
	producer   string
	origin     string
	eventTime  time.Time
}

type craftedRun struct {
	runID        string
	observedRows []observedRow
	derivedRows  []derivedRow
	changes      []temporal.Change
	derived      []DerivedChange
}

var baseTime = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

const (
	imgID  = "containerimage:aws:img-1"
	wlID   = "workload:aws:wl-1"
	dsID   = "datastore:aws:bucket-1"
	roleID = "identity:aws:role-1"
)

// craftedRuns builds a fixed three-run sequence. It is pure (no DB) so the same
// data feeds both the log writes and the incremental ApplyRun input.
func craftedRuns() []craftedRun {
	t0 := baseTime
	t1 := baseTime.Add(time.Hour)
	t2 := baseTime.Add(2 * time.Hour)

	// Run 1: create three entities + one edge; infer one edge; classify bucket.
	run1 := craftedRun{runID: "run-1"}
	run1.observedRows = []observedRow{
		{changeType: temporal.ChangeEntityCreated, kind: model.KindContainerImage, provider: "aws", name: "img-1", canonical: imgID,
			attrs: map[string]any{"kind": "ContainerImage", "provider": "aws", "name": "img-1", "digest": "sha256:aaa"}, eventTime: t0},
		{changeType: temporal.ChangeEntityCreated, kind: model.KindWorkload, provider: "aws", name: "wl-1", canonical: wlID,
			attrs: map[string]any{"kind": "Workload", "provider": "aws", "name": "wl-1", "public": false}, eventTime: t0},
		{changeType: temporal.ChangeEntityCreated, kind: model.KindDataStore, provider: "aws", name: "bucket-1", canonical: dsID,
			attrs: map[string]any{"kind": "DataStore", "provider": "aws", "name": "bucket-1", "public": false}, eventTime: t0},
		{changeType: temporal.ChangeRelationAdded, kind: model.KindWorkload, canonical: wlID, relation: model.RelRunsImage, dstID: imgID,
			attrs: map[string]any{"confidence": 1.0}, eventTime: t0},
	}
	run1.derivedRows = []derivedRow{
		{kind: "inferred_edge", changeType: "inferred.added", canonical: wlID, relation: model.RelDependsOn, dstID: dsID,
			confidence: 0.8, producer: "env-var-dependency", origin: infer.OriginRule, eventTime: t0},
		{kind: "classification", changeType: "classified", canonical: dsID, class: "internal", confidence: 0.6, producer: "rule:naming", eventTime: t0},
	}

	// Run 2: create role entity; add workload->role edge; state-change bucket to
	// public; reclassify bucket to pii.
	run2 := craftedRun{runID: "run-2"}
	run2.observedRows = []observedRow{
		{changeType: temporal.ChangeEntityCreated, kind: model.KindIdentity, provider: "aws", name: "role-1", canonical: roleID,
			attrs: map[string]any{"kind": "Identity", "provider": "aws", "name": "role-1"}, eventTime: t1},
		{changeType: temporal.ChangeEntityStateChanged, kind: model.KindDataStore, provider: "aws", name: "bucket-1", canonical: dsID,
			attrs: map[string]any{"kind": "DataStore", "provider": "aws", "name": "bucket-1", "public": true}, eventTime: t1},
		{changeType: temporal.ChangeRelationAdded, kind: model.KindWorkload, canonical: wlID, relation: model.RelAuthenticatesAs, dstID: roleID,
			attrs: map[string]any{"confidence": 1.0}, eventTime: t1},
	}
	run2.derivedRows = []derivedRow{
		{kind: "classification", changeType: "reclassified", canonical: dsID, class: "pii", confidence: 0.9, producer: "rule:naming", eventTime: t1},
	}

	// Run 3: remove the inferred edge; remove the workload->role edge; delete the
	// image (reference-counted deletion -> absence).
	run3 := craftedRun{runID: "run-3"}
	run3.observedRows = []observedRow{
		{changeType: temporal.ChangeRelationRemoved, kind: model.KindWorkload, canonical: wlID, relation: model.RelAuthenticatesAs, dstID: roleID,
			attrs: map[string]any{}, eventTime: t2},
		{changeType: temporal.ChangeEntityDeleted, kind: model.KindContainerImage, canonical: imgID,
			attrs: map[string]any{}, eventTime: t2},
	}
	run3.derivedRows = []derivedRow{
		{kind: "inferred_edge", changeType: "inferred.removed", canonical: wlID, relation: model.RelDependsOn, dstID: dsID,
			confidence: 0.8, producer: "env-var-dependency", origin: infer.OriginRule, eventTime: t2},
	}

	runs := []craftedRun{run1, run2, run3}
	for i := range runs {
		runs[i].changes = toChanges(runs[i].observedRows)
		runs[i].derived = toDerived(runs[i].derivedRows)
	}
	return runs
}

// toChanges maps the crafted observed rows to the temporal.Change input the
// incremental ApplyRun consumes, mirroring exactly what the fold will read back.
func toChanges(rows []observedRow) []temporal.Change {
	out := make([]temporal.Change, 0, len(rows))
	for _, r := range rows {
		kind := elementEntity
		if r.relation != "" {
			kind = elementEdge
		}
		out = append(out, temporal.Change{
			ElementKind: kind,
			ChangeType:  r.changeType,
			CanonicalID: r.canonical,
			Relation:    string(r.relation),
			DstID:       r.dstID,
			Attributes:  r.attrs,
			EventTime:   r.eventTime,
		})
	}
	return out
}

// toDerived maps the crafted derived rows to DerivedChange input. An
// inferred.removed row becomes a RemovedInferredEdge so the incremental path
// deletes the edge, converging on the same absence the fold produces;
// declassified rows carry no live overlay and are omitted.
func toDerived(rows []derivedRow) []DerivedChange {
	var out []DerivedChange
	for _, r := range rows {
		switch r.kind {
		case "inferred_edge":
			ie := infer.InferredEdge{
				SrcID: r.canonical, Relation: r.relation, DstID: r.dstID,
				Confidence: r.confidence, Origin: r.origin, Producer: r.producer,
			}
			if r.changeType == derivedInferredRemoved {
				// A removed inferred edge is deleted incrementally so the
				// end state matches the fold, which omits it entirely.
				out = append(out, RemovedInferredEdge(ie))
				continue
			}
			out = append(out, InferredEdge(ie))
		case "classification":
			if r.changeType == derivedDeclassified {
				continue
			}
			out = append(out, Classification(classify.DataClass{
				CanonicalID: r.canonical, Class: r.class,
				Confidence: r.confidence, ClassifiedBy: r.producer,
			}))
		}
	}
	return out
}

const (
	elementEntity = "entity"
	elementEdge   = "edge"
)

// ---------------------------------------------------------------------------
// log writers (craft the two append-only logs directly)
// ---------------------------------------------------------------------------

func seedCollectionRun(ctx context.Context, t *testing.T, st *store.Store, runID string) {
	t.Helper()
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO collection_runs (run_id, source, status) VALUES ($1, $2, 'completed')
		 ON CONFLICT (run_id) DO NOTHING`,
		runID, "fixture"); err != nil {
		t.Fatalf("seed collection_run %s: %v", runID, err)
	}
}

func writeObserved(ctx context.Context, t *testing.T, st *store.Store, org int64, runID string, rows []observedRow) {
	t.Helper()
	for _, r := range rows {
		kind := elementEntity
		var relation, dstID any
		if r.relation != "" {
			kind = elementEdge
			relation = string(r.relation)
			dstID = r.dstID
		}
		attrs, err := json.Marshal(nonNil(r.attrs))
		if err != nil {
			t.Fatalf("marshal attrs: %v", err)
		}
		if _, err := st.Pool().Exec(ctx,
			`INSERT INTO observed_events
			   (org_id, run_id, source, element_kind, change_type, canonical_id,
			    relation, dst_id, attributes, event_time, recorded_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`,
			org, runID, "fixture", kind, r.changeType, r.canonical,
			relation, dstID, attrs, r.eventTime); err != nil {
			t.Fatalf("write observed row (%s %s): %v", r.changeType, r.canonical, err)
		}
	}
}

func writeDerived(ctx context.Context, t *testing.T, st *store.Store, org int64, runID string, rows []derivedRow) {
	t.Helper()
	for _, r := range rows {
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
		if _, err := st.Pool().Exec(ctx,
			`INSERT INTO derived_events
			   (org_id, run_id, kind, change_type, origin, producer, canonical_id,
			    relation, dst_id, attributes, confidence, event_time, recorded_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`,
			org, runID, r.kind, r.changeType, nonEmpty(r.origin, "rule"), r.producer, r.canonical,
			relation, dstID, attrsJSON, r.confidence, r.eventTime); err != nil {
			t.Fatalf("write derived row (%s %s): %v", r.changeType, r.canonical, err)
		}
	}
}

// ---------------------------------------------------------------------------
// projection snapshot + derivability
// ---------------------------------------------------------------------------

type nodeSnap struct {
	CanonicalID  string
	Kind         string
	Stale        bool
	DataClass    string
	ClassifiedBy string
}

type edgeSnap struct {
	Src      string
	Relation string
	Dst      string
	Inferred bool
}

type graphSnap struct {
	Nodes []nodeSnap
	Edges []edgeSnap
}

func (g graphSnap) String() string {
	b, _ := json.Marshal(g)
	return string(b)
}

// snapshotGraph reads the whole projection into a canonical, order-independent
// snapshot so two projections can be compared for structural equality.
func snapshotGraph(ctx context.Context, t *testing.T, w *graph.Writer) graphSnap {
	t.Helper()
	sess := w.Driver().NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer sess.Close(ctx)

	var snap graphSnap

	nres, err := sess.Run(ctx,
		`MATCH (n:Entity)
		 RETURN n.canonical_id AS id, n.kind AS kind, coalesce(n.stale,false) AS stale,
		        n.data_class AS data_class, n.classified_by AS classified_by
		 ORDER BY id`, nil)
	if err != nil {
		t.Fatalf("snapshot nodes: %v", err)
	}
	for nres.Next(ctx) {
		m := nres.Record().AsMap()
		snap.Nodes = append(snap.Nodes, nodeSnap{
			CanonicalID:  asString(m["id"]),
			Kind:         asString(m["kind"]),
			Stale:        asBool(m["stale"]),
			DataClass:    asString(m["data_class"]),
			ClassifiedBy: asString(m["classified_by"]),
		})
	}
	if err := nres.Err(); err != nil {
		t.Fatalf("iterate nodes: %v", err)
	}

	eres, err := sess.Run(ctx,
		`MATCH (a:Entity)-[r]->(b:Entity)
		 RETURN a.canonical_id AS src, type(r) AS rel, b.canonical_id AS dst,
		        coalesce(r.inferred,false) AS inferred
		 ORDER BY src, rel, dst`, nil)
	if err != nil {
		t.Fatalf("snapshot edges: %v", err)
	}
	for eres.Next(ctx) {
		m := eres.Record().AsMap()
		snap.Edges = append(snap.Edges, edgeSnap{
			Src:      asString(m["src"]),
			Relation: asString(m["rel"]),
			Dst:      asString(m["dst"]),
			Inferred: asBool(m["inferred"]),
		})
	}
	if err := eres.Err(); err != nil {
		t.Fatalf("iterate edges: %v", err)
	}

	sort.Slice(snap.Nodes, func(i, j int) bool { return snap.Nodes[i].CanonicalID < snap.Nodes[j].CanonicalID })
	sort.Slice(snap.Edges, func(i, j int) bool {
		return edgeKeyStr(snap.Edges[i]) < edgeKeyStr(snap.Edges[j])
	})
	return snap
}

func edgeKeyStr(e edgeSnap) string {
	return fmt.Sprintf("%s|%s|%s|%t", e.Src, e.Relation, e.Dst, e.Inferred)
}

type edgeIdentity struct {
	src, rel, dst string
	inferred      bool
}

type derivableSet struct {
	nodes map[string]struct{}
	edges map[edgeIdentity]struct{}
}

// derivableFromLogs folds the two logs the same way RebuildAsOf does and returns
// the set of node ids and edge identities that are derivable (live) at the end
// of the sequence. A projected element outside this set would be Memgraph state
// not reconstructable from the logs (Req 7.4). We compute it independently from
// the Rebuilder's own fold so the check is a genuine cross-validation.
func derivableFromLogs(ctx context.Context, t *testing.T, st *store.Store, org int64) derivableSet {
	t.Helper()
	ds := derivableSet{nodes: map[string]struct{}{}, edges: map[edgeIdentity]struct{}{}}

	// Live entities: latest observed entity row per canonical_id, unless deleted.
	entRows, err := st.Pool().Query(ctx,
		`SELECT DISTINCT ON (canonical_id) canonical_id, change_type
		   FROM observed_events
		  WHERE org_id = $1 AND element_kind = 'entity' AND canonical_id IS NOT NULL
		  ORDER BY canonical_id, event_time DESC, recorded_at DESC, seq DESC`, org)
	if err != nil {
		t.Fatalf("fold entities: %v", err)
	}
	for entRows.Next() {
		var id, ct string
		if err := entRows.Scan(&id, &ct); err != nil {
			entRows.Close()
			t.Fatalf("scan entity fold: %v", err)
		}
		// A deleted entity is marked stale, not removed, so the node remains
		// present in the projection; it is still derivable from the logs.
		ds.nodes[id] = struct{}{}
	}
	entRows.Close()
	if err := entRows.Err(); err != nil {
		t.Fatalf("iterate entity fold: %v", err)
	}

	// Live observed edges: latest per (src,rel,dst), unless removed.
	addEdgeFold(ctx, t, st, ds, org,
		`SELECT DISTINCT ON (canonical_id, relation, dst_id) canonical_id, relation, dst_id, change_type
		   FROM observed_events
		  WHERE org_id = $1 AND element_kind = 'edge'
		    AND canonical_id IS NOT NULL AND relation IS NOT NULL AND dst_id IS NOT NULL
		  ORDER BY canonical_id, relation, dst_id, event_time DESC, recorded_at DESC, seq DESC`,
		temporal.ChangeRelationRemoved, false)

	// Live inferred edges: latest per (src,rel,dst), unless inferred.removed.
	addEdgeFold(ctx, t, st, ds, org,
		`SELECT DISTINCT ON (canonical_id, relation, dst_id) canonical_id, relation, dst_id, change_type
		   FROM derived_events
		  WHERE org_id = $1 AND kind = 'inferred_edge'
		    AND canonical_id IS NOT NULL AND relation IS NOT NULL AND dst_id IS NOT NULL
		  ORDER BY canonical_id, relation, dst_id, event_time DESC, recorded_at DESC, seq DESC`,
		derivedInferredRemoved, true)

	return ds
}

func addEdgeFold(ctx context.Context, t *testing.T, st *store.Store, ds derivableSet, org int64, query, absentChangeType string, inferred bool) {
	t.Helper()
	rows, err := st.Pool().Query(ctx, query, org)
	if err != nil {
		t.Fatalf("fold edges: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var src, rel, dst, ct string
		if err := rows.Scan(&src, &rel, &dst, &ct); err != nil {
			t.Fatalf("scan edge fold: %v", err)
		}
		if ct == absentChangeType {
			continue
		}
		ds.edges[edgeIdentity{src: src, rel: rel, dst: dst, inferred: inferred}] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate edge fold: %v", err)
	}
}

// ---------------------------------------------------------------------------
// container helpers
// ---------------------------------------------------------------------------

func startPostgres(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bellu"), tcpostgres.WithUsername("bellu"), tcpostgres.WithPassword("bellu"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		if isRuntimeUnavailable(err) {
			t.Skipf("no container runtime available for Postgres; skipping replay-determinism test: %v", err)
		}
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

// startMemgraph starts a disposable Memgraph and returns a Writer. The bool is
// false (with a Skip-worthy signal already logged) when the container runtime
// is unavailable, so the caller can skip cleanly rather than fail.
func startMemgraph(t *testing.T) (*graph.Writer, bool) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: "memgraph/memgraph:2.18.1", Cmd: []string{"--bolt-port=7687", "--log-level=WARNING"},
		ExposedPorts: []string{"7687/tcp"},
		WaitingFor:   wait.ForListeningPort("7687/tcp").WithStartupTimeout(60 * time.Second),
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		if isRuntimeUnavailable(err) {
			t.Logf("no container runtime available for Memgraph: %v", err)
			return nil, false
		}
		t.Fatalf("start memgraph: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	host, _ := ctr.Host(ctx)
	port, _ := ctr.MappedPort(ctx, "7687")
	uri := "bolt://" + host + ":" + port.Port()

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
	return w, true
}

// isRuntimeUnavailable reports whether a container start failure is because no
// Docker-compatible runtime is reachable (so the test should skip, not fail).
func isRuntimeUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"cannot connect to the docker daemon",
		"docker daemon",
		"rootless docker not found",
		"failed to find any container runtime",
		"could not find a working container runtime",
		"no such host",
		"permission denied while trying to connect",
		"connection refused",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// Validates: Requirements 7.3, 7.4
