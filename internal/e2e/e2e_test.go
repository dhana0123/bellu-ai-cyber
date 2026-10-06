//go:build integration

// Package e2e holds the end-to-end test that drives the whole pipeline
// (ingest -> normalize -> resolve -> derive agents -> sync) against the full
// fixture set on disposable Postgres and Memgraph containers, then asserts the
// target attack paths exist with evidence.
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/normalize/aiagent"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/resolve"
	"github.com/bellu/bellu-ai-cyber/internal/source"
	cdxsrc "github.com/bellu/bellu-ai-cyber/internal/source/cyclonedx"
	zeeksrc "github.com/bellu/bellu-ai-cyber/internal/source/zeek"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

const testdata = "../../testdata"

func startPostgres(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bellu"), tcpostgres.WithUsername("bellu"), tcpostgres.WithPassword("bellu"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	dsn, _ := ctr.ConnectionString(ctx, "sslmode=disable")
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func startMemgraph(t *testing.T) *graph.Writer {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: "memgraph/memgraph:2.18.1", Cmd: []string{"--bolt-port=7687", "--log-level=WARNING"},
		ExposedPorts: []string{"7687/tcp"},
		WaitingFor:   wait.ForListeningPort("7687/tcp").WithStartupTimeout(60 * time.Second),
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("memgraph: %v", err)
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
		t.Fatal(err)
	}
	return w
}

func loadFixtureJSONL(t *testing.T, rel string) []rawevent.RawEvent {
	t.Helper()
	f, err := os.Open(filepath.Join(testdata, rel))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var evs []rawevent.RawEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e rawevent.RawEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		e.RunID = "e2e"
		e.Finalize()
		evs = append(evs, e)
	}
	return evs
}

func loadSBOMs(t *testing.T) []rawevent.RawEvent {
	t.Helper()
	s := cdxsrc.New(filepath.Join(testdata, "sboms"))
	evc, errc := s.Collect(context.Background(), source.Run{RunID: "e2e"})
	var evs []rawevent.RawEvent
	for evc != nil || errc != nil {
		select {
		case e, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			evs = append(evs, e)
		case err, ok := <-errc:
			if !ok {
				errc = nil
				continue
			}
			if err != nil {
				t.Fatalf("sbom: %v", err)
			}
		}
	}
	return evs
}

// loadZeek copies the fixture Zeek logs to a temp dir (so offset sidecars are
// disposable) and collects events through the Zeek source.
func loadZeek(t *testing.T) []rawevent.RawEvent {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"conn.log", "dns.log", "ssl.log"} {
		b, err := os.ReadFile(filepath.Join(testdata, "zeek", name))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(dir, name), b, 0o644)
	}
	s := zeeksrc.New(dir)
	evc, errc := s.Collect(context.Background(), source.Run{RunID: "e2e"})
	var evs []rawevent.RawEvent
	for evc != nil || errc != nil {
		select {
		case e, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			evs = append(evs, e)
		case err, ok := <-errc:
			if !ok {
				errc = nil
				continue
			}
			if err != nil {
				t.Fatalf("zeek: %v", err)
			}
		}
	}
	return evs
}

func TestEndToEndAttackPath(t *testing.T) {
	ctx := context.Background()
	st := startPostgres(t)
	w := startMemgraph(t)

	// 1) Ingest all fixtures into the evidence store.
	var all []rawevent.RawEvent
	all = append(all, loadSBOMs(t)...)
	all = append(all, loadFixtureJSONL(t, "aws/inventory.jsonl")...)
	all = append(all, loadFixtureJSONL(t, "k8s/eks.jsonl")...)
	all = append(all, loadZeek(t)...)
	if err := st.StartRun(ctx, "e2e", "fixtures"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertEvents(ctx, all); err != nil {
		t.Fatal(err)
	}

	// 2) Normalize + resolve + derive agents.
	events, err := st.LoadRawEvents(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := normalize.NewRegistry()
	reg.Register(normalize.CycloneDX{})
	reg.Register(normalize.AWS{})
	reg.Register(normalize.K8s{})
	reg.Register(normalize.Network{})
	nres, err := reg.Normalize(events)
	if err != nil {
		t.Fatal(err)
	}
	rres, err := resolve.NewDeterministic(nil).Resolve(nres)
	if err != nil {
		t.Fatal(err)
	}
	ae, aedges := aiagent.Derive(aiagent.DefaultRules(), rres.Entities, rres.Edges)
	rres.Entities = append(rres.Entities, ae...)
	rres.Edges = append(rres.Edges, aedges...)
	if err := st.PersistNormalized(ctx, rres.Entities, rres.Edges); err != nil {
		t.Fatal(err)
	}

	// 3) Sync to Memgraph.
	ents, err := st.LoadGraphEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := st.LoadGraphEdges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.SyncEntities(ctx, ents); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SyncEdges(ctx, edges); err != nil {
		t.Fatal(err)
	}

	// 4) Assert the exact attack paths exist, and that every edge on each path
	// carries evidence. We bind each relationship explicitly and check its
	// evidence_ids list (Memgraph does not support list comprehension over
	// relationships(path) in this form).
	assertPath(ctx, t, w,
		`MATCH (a:AIAgent)-[r1:RUNS_IN]->(w:Workload)-[r2:USES]->(sa:Identity)
		   -[r3:AUTHENTICATES_AS]->(cl:Identity)-[r4:CAN_READ]->(d:DataStore)
		 RETURN size(r1.evidence_ids) AS e1, size(r2.evidence_ids) AS e2,
		        size(r3.evidence_ids) AS e3, size(r4.evidence_ids) AS e4`,
		[]string{"e1", "e2", "e3", "e4"},
		"agent -> workload -> identity -> role -> datastore")

	assertPath(ctx, t, w,
		`MATCH (a:AIAgent)-[r1:RUNS_IN]->(w:Workload)-[r2:CONNECTS_TO]->(s:ExternalService)
		 WHERE s.attr_is_ai_api = true
		 RETURN size(r1.evidence_ids) AS e1, size(r2.evidence_ids) AS e2`,
		[]string{"e1", "e2"},
		"agent -> workload -> external AI API")
}

// assertPath runs a query that returns one evidence-count column per edge and
// fails if no row is returned or any edge lacks evidence.
func assertPath(ctx context.Context, t *testing.T, w *graph.Writer, cypher string, cols []string, name string) {
	t.Helper()
	res, err := neo4j.ExecuteQuery(ctx, w.Driver(), cypher, nil,
		neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(""))
	if err != nil {
		t.Fatalf("%s: query: %v", name, err)
	}
	if len(res.Records) == 0 {
		t.Fatalf("%s: path not found", name)
	}
	m := res.Records[0].AsMap()
	for _, c := range cols {
		n, _ := m[c].(int64)
		if n < 1 {
			t.Fatalf("%s: edge %s on path has no evidence (count=%v)", name, c, m[c])
		}
	}
	t.Logf("%s: path present, all %d edges carry evidence", name, len(cols))
}
