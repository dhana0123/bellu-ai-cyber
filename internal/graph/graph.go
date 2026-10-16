// Package graph writes the resolved canonical graph into Memgraph over the Bolt
// protocol using neo4j-go-driver v5 (no JVM).
//
// Safety: node labels and relationship types are never interpolated from raw
// input. They are validated against the model allow-list before being placed
// in a Cypher string; all other values are passed as query parameters.
package graph

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// WriteGap records one edge batch for which Memgraph created or matched fewer
// relationships than were submitted — i.e. at least one endpoint node was
// absent. Recorded for visibility (R2); never fatal.
type WriteGap struct {
	Relation  string
	SyncRunID string
	BatchSize int
	Observed  int
}

// Writer syncs entities and edges into Memgraph.
type Writer struct {
	driver neo4j.DriverWithContext
}

// Open connects to Memgraph and verifies connectivity.
func Open(ctx context.Context, uri, user, password string) (*Writer, error) {
	auth := neo4j.BasicAuth(user, password, "")
	if user == "" {
		auth = neo4j.NoAuth()
	}
	drv, err := neo4j.NewDriverWithContext(uri, auth)
	if err != nil {
		return nil, fmt.Errorf("graph: new driver: %w", err)
	}
	if err := drv.VerifyConnectivity(ctx); err != nil {
		_ = drv.Close(ctx)
		return nil, fmt.Errorf("graph: connectivity: %w", err)
	}
	return &Writer{driver: drv}, nil
}

// Close releases the driver.
func (w *Writer) Close(ctx context.Context) error { return w.driver.Close(ctx) }

// Driver exposes the underlying driver for read queries (used by tests and the
// explain path).
func (w *Writer) Driver() neo4j.DriverWithContext { return w.driver }

func (w *Writer) session(ctx context.Context) neo4j.SessionWithContext {
	return w.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
}

// EnsureSchema creates the :Entity(canonical_id) uniqueness constraint and a
// per-label index. Memgraph supports label-property indexes and existence/
// uniqueness constraints.
func (w *Writer) EnsureSchema(ctx context.Context) error {
	sess := w.session(ctx)
	defer sess.Close(ctx)

	stmts := []string{
		"CREATE CONSTRAINT ON (n:Entity) ASSERT n.canonical_id IS UNIQUE",
		"CREATE INDEX ON :Entity(canonical_id)",
	}
	for _, k := range model.AllKinds {
		stmts = append(stmts, fmt.Sprintf("CREATE INDEX ON :%s(canonical_id)", k))
	}
	for _, s := range stmts {
		if _, err := sess.Run(ctx, s, nil); err != nil {
			// Constraints/indexes may already exist; Memgraph returns an error
			// we treat as non-fatal only for the "already exists" case.
			if !isAlreadyExists(err) {
				return fmt.Errorf("graph: schema %q: %w", s, err)
			}
		}
	}
	return nil
}

const batchSize = 500

// SyncEntities writes entities in batches, grouped by kind so a single dynamic
// label can be used per batch. Evidence ids and sources are stored on the node;
// first_seen/last_seen/stale are maintained.
func (w *Writer) SyncEntities(ctx context.Context, entities []store.GraphEntity) (int, error) {
	byKind := map[model.Kind][]store.GraphEntity{}
	for _, e := range entities {
		if !model.ValidKind(e.Kind) {
			return 0, fmt.Errorf("graph: refusing unknown kind %q", e.Kind)
		}
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}

	sess := w.session(ctx)
	defer sess.Close(ctx)

	total := 0
	for kind, ents := range byKind {
		// Label is from the allow-list, safe to interpolate.
		cypher := fmt.Sprintf(`
			UNWIND $rows AS row
			MERGE (n:Entity {canonical_id: row.canonical_id})
			ON CREATE SET n.first_seen = timestamp()
			SET n:%s,
			    n.kind = row.kind,
			    n.provider = row.provider,
			    n.name = row.name,
			    n.evidence_ids = row.evidence_ids,
			    n.sources = row.sources,
			    n.last_seen = timestamp(),
			    n.stale = false,
			    n += row.props`, kind)

		for _, chunk := range chunkEntities(ents, batchSize) {
			rows := make([]map[string]any, 0, len(chunk))
			for _, e := range chunk {
				rows = append(rows, map[string]any{
					"canonical_id": e.CanonicalID,
					"kind":         string(e.Kind),
					"provider":     e.Provider,
					"name":         e.Name,
					"evidence_ids": toAnySlice(e.EvidenceIDs),
					"sources":      toAnySlice(e.Sources),
					"props":        scalarProps(e.Attributes),
				})
			}
			if _, err := sess.Run(ctx, cypher, map[string]any{"rows": rows}); err != nil {
				return total, fmt.Errorf("graph: sync %s: %w", kind, err)
			}
			total += len(chunk)
		}
	}
	return total, nil
}

// SyncEdges writes edges in batches grouped by relation type. Evidence ids and
// sources are unioned with any existing values on the edge.
//
// SyncEdges preserves the original signature (count, error) for existing
// callers and the intelcorrelate.GraphWriter interface; it delegates to
// SyncEdgesWithGaps and discards the gap records.
func (w *Writer) SyncEdges(ctx context.Context, edges []store.GraphEdge) (int, error) {
	total, _, err := w.SyncEdgesWithGaps(ctx, edges)
	return total, err
}

// SyncEdgesWithGaps writes edges in batches grouped by relation type and
// detects graph-write gaps (R2): because each batch does MATCH...MATCH...MERGE,
// a row whose endpoint node is absent produces no relationship. The Cypher
// returns the number of relationships actually created or matched; when that is
// less than the batch size, one WriteGap is recorded and the sync continues
// (non-fatal). All gaps from this call share one sync-run identifier.
func (w *Writer) SyncEdgesWithGaps(ctx context.Context, edges []store.GraphEdge) (int, []WriteGap, error) {
	byRel := map[model.Relation][]store.GraphEdge{}
	for _, e := range edges {
		if !model.ValidRelation(e.Relation) {
			return 0, nil, fmt.Errorf("graph: refusing unknown relation %q", e.Relation)
		}
		byRel[e.Relation] = append(byRel[e.Relation], e)
	}

	sess := w.session(ctx)
	defer sess.Close(ctx)

	syncRunID := uuid.NewString()
	total := 0
	var gaps []WriteGap
	for rel, es := range byRel {
		// Relation type is from the allow-list, safe to interpolate.
		cypher := fmt.Sprintf(`
			UNWIND $rows AS row
			MATCH (a:Entity {canonical_id: row.src})
			MATCH (b:Entity {canonical_id: row.dst})
			MERGE (a)-[r:%s]->(b)
			ON CREATE SET r.first_seen = timestamp()
			SET r.confidence = CASE WHEN r.confidence IS NULL OR row.confidence > r.confidence
			                        THEN row.confidence ELSE r.confidence END,
			    r.evidence_ids = row.evidence_ids,
			    r.sources = row.sources,
			    r.last_seen = timestamp(),
			    r.stale = false,
			    r += row.props
			RETURN count(r) AS written`, rel)

		for _, chunk := range chunkEdges(es, batchSize) {
			rows := make([]map[string]any, 0, len(chunk))
			for _, e := range chunk {
				rows = append(rows, map[string]any{
					"src":          e.SrcID,
					"dst":          e.DstID,
					"confidence":   e.Confidence,
					"evidence_ids": toAnySlice(e.EvidenceIDs),
					"sources":      toAnySlice(e.Sources),
					"props":        scalarProps(e.Attributes),
				})
			}
			res, err := sess.Run(ctx, cypher, map[string]any{"rows": rows})
			if err != nil {
				return total, gaps, fmt.Errorf("graph: sync %s: %w", rel, err)
			}
			written := readWrittenCount(ctx, res)
			if written < len(chunk) {
				// R2.2: an endpoint node was absent for at least one row. Record
				// the gap and keep going (R2.4, R2.5).
				gaps = append(gaps, WriteGap{
					Relation: string(rel), SyncRunID: syncRunID,
					BatchSize: len(chunk), Observed: written,
				})
			}
			total += len(chunk)
		}
	}
	return total, gaps, nil
}

// readWrittenCount drains the single-row RETURN count(r) result. Any read error
// or missing value is treated as 0 written, which only widens gap detection
// (never masks a gap).
func readWrittenCount(ctx context.Context, res neo4j.ResultWithContext) int {
	if res.Next(ctx) {
		if v, ok := res.Record().Get("written"); ok {
			if n, ok := v.(int64); ok {
				return int(n)
			}
		}
	}
	return 0
}

// MarkStale sets stale=true on the given canonical IDs. Nothing is deleted;
// stale nodes remain queryable. The set of stale IDs is computed by the store
// (entities not seen in a source's latest run).
func (w *Writer) MarkStale(ctx context.Context, canonicalIDs []string) error {
	if len(canonicalIDs) == 0 {
		return nil
	}
	sess := w.session(ctx)
	defer sess.Close(ctx)
	_, err := sess.Run(ctx,
		`UNWIND $ids AS id
		 MATCH (n:Entity {canonical_id: id})
		 SET n.stale = true`,
		map[string]any{"ids": toAnySlice(canonicalIDs)})
	return err
}

// Counts returns node and relationship counts, for verification.
func (w *Writer) Counts(ctx context.Context) (nodes, rels int64, err error) {
	sess := w.session(ctx)
	defer sess.Close(ctx)

	rec, err := neo4j.ExecuteQuery(ctx, w.driver,
		"MATCH (n:Entity) RETURN count(n) AS c", nil,
		neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(""))
	if err != nil {
		return 0, 0, err
	}
	nodes, _ = rec.Records[0].AsMap()["c"].(int64)

	rec2, err := neo4j.ExecuteQuery(ctx, w.driver,
		"MATCH ()-[r]->() RETURN count(r) AS c", nil,
		neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(""))
	if err != nil {
		return nodes, 0, err
	}
	rels, _ = rec2.Records[0].AsMap()["c"].(int64)
	return nodes, rels, nil
}
