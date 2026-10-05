//go:build integration

package graph

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// neo4jSingle runs a query expected to return a single record and returns it
// as a map.
func neo4jSingle(ctx context.Context, w *Writer, cypher string) (map[string]any, error) {
	sess := w.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, cypher, nil)
	if err != nil {
		return nil, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return nil, err
	}
	return rec.AsMap(), nil
}

// newTestWriter starts a disposable Memgraph (no auth) and returns a Writer.
func newTestWriter(t *testing.T) *Writer {
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
	var w *Writer
	deadline := time.Now().Add(30 * time.Second)
	for {
		w, err = Open(ctx, uri, "", "")
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

func ent(id string, kind model.Kind, evIDs, sources []string) store.GraphEntity {
	return store.GraphEntity{
		CanonicalID: id, Kind: kind, Provider: "image", Name: id,
		Attributes: map[string]any{}, EvidenceIDs: evIDs, Sources: sources,
	}
}

func TestSyncIdempotent(t *testing.T) {
	ctx := context.Background()
	w := newTestWriter(t)

	entities := []store.GraphEntity{
		ent("image:sha256:x", model.KindContainerImage, []string{"e1"}, []string{"syft"}),
		ent("package:pkg:pypi/a@1", model.KindPackage, []string{"e2"}, []string{"syft"}),
	}
	edges := []store.GraphEdge{
		{SrcID: "image:sha256:x", Relation: model.RelDependsOn, DstID: "package:pkg:pypi/a@1", Confidence: 1, EvidenceIDs: []string{"e2"}, Sources: []string{"syft"}},
	}

	for i := 0; i < 2; i++ {
		if _, err := w.SyncEntities(ctx, entities); err != nil {
			t.Fatalf("sync entities: %v", err)
		}
		if _, err := w.SyncEdges(ctx, edges); err != nil {
			t.Fatalf("sync edges: %v", err)
		}
	}

	nodes, rels, err := w.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nodes != 2 {
		t.Fatalf("expected 2 nodes after re-sync, got %d", nodes)
	}
	if rels != 1 {
		t.Fatalf("expected 1 rel after re-sync, got %d", rels)
	}
}

func TestEvidenceMergeFromTwoSources(t *testing.T) {
	ctx := context.Background()
	w := newTestWriter(t)

	// First sync: syft only.
	e1 := []store.GraphEntity{ent("image:sha256:y", model.KindContainerImage, []string{"syft1"}, []string{"syft"})}
	if _, err := w.SyncEntities(ctx, e1); err != nil {
		t.Fatal(err)
	}
	// Second sync: same node, now with union of both sources' evidence (as the
	// store would present after loading aggregated evidence).
	e2 := []store.GraphEntity{ent("image:sha256:y", model.KindContainerImage, []string{"syft1", "trivy1"}, []string{"syft", "trivy"})}
	if _, err := w.SyncEntities(ctx, e2); err != nil {
		t.Fatal(err)
	}

	rec, err := neo4jSingle(ctx, w, `MATCH (n:Entity {canonical_id:"image:sha256:y"}) RETURN size(n.evidence_ids) AS ec, size(n.sources) AS sc`)
	if err != nil {
		t.Fatal(err)
	}
	if rec["ec"].(int64) != 2 {
		t.Fatalf("expected 2 evidence ids, got %v", rec["ec"])
	}
	if rec["sc"].(int64) != 2 {
		t.Fatalf("expected 2 sources, got %v", rec["sc"])
	}
}

func TestStaleFlagging(t *testing.T) {
	ctx := context.Background()
	w := newTestWriter(t)

	if _, err := w.SyncEntities(ctx, []store.GraphEntity{
		ent("image:sha256:z", model.KindContainerImage, []string{"e1"}, []string{"syft"}),
	}); err != nil {
		t.Fatal(err)
	}
	// Mark stale (an entity not seen in the latest run keeps data, flagged).
	if err := w.MarkStale(ctx, []string{"image:sha256:z"}); err != nil {
		t.Fatal(err)
	}
	rec, err := neo4jSingle(ctx, w, `MATCH (n:Entity {canonical_id:"image:sha256:z"}) RETURN n.stale AS s`)
	if err != nil {
		t.Fatal(err)
	}
	if rec["s"] != true {
		t.Fatalf("expected stale=true, got %v", rec["s"])
	}
}
