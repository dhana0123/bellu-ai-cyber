// Package replay projects the two temporal logs (observed facts + derived
// inferences/classifications) into Memgraph. It is the single write path from
// the logs to the graph projection and reuses the existing graph.Writer MERGE
// path so Memgraph never carries state that is not derivable from the logs.
//
// Two entry points share the same base-then-overlay shape:
//
//   - ApplyRun is the incremental path at the end of each ingest: it writes the
//     current run's observed changes as the base graph (MERGE via SyncEntities /
//     SyncEdges), then the run's derived changes as an overlay (inferred edges
//     MERGE'd with inferred=true + confidence + evidence, and classification
//     properties SET on nodes). Reference-counted deletions (entity.deleted)
//     become MarkStale/absence in the projection. No full replay.
//   - RebuildAsOf (task 11.2) folds both logs to a point-in-time snapshot and
//     writes it the same way; it is the authoritative recovery path.
//
// Because every projection write is a MERGE/SET, applying a run twice or
// rebuilding from scratch yields the same graph: the projection is idempotent.
package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
	"github.com/bellu/bellu-ai-cyber/internal/temporal"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/classify"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/infer"
)

// Rebuilder replays the two logs into Memgraph. For a full rebuild it folds
// both lanes to a point-in-time snapshot and writes it; for incremental ingest
// it applies only the current run's changes. It holds the facts store (log
// source of truth) and the graph.Writer (projection target).
type Rebuilder struct {
	store  *store.Store
	writer *graph.Writer

	// mu guards the per-org rebuild markers. A marker is set when an
	// incremental ApplyRun fails to fully project a run (so Memgraph is in a
	// suspect state) and cleared when a RebuildAsOf for that org succeeds. The
	// marker never touches the committed logs: it only records that the graph
	// projection — not the facts — needs recovery.
	mu            sync.Mutex
	rebuildMarker map[int64]rebuildMarker
}

// rebuildMarker records why an org's Memgraph projection is suspect and when
// the marker was set. It is advisory state for the recovery path (RebuildAsOf);
// the authoritative facts always live in the two logs.
type rebuildMarker struct {
	RunID  string
	Reason string
	SetAt  time.Time
}

// NewRebuilder constructs a Rebuilder over the facts store and graph writer.
func NewRebuilder(st *store.Store, writer *graph.Writer) *Rebuilder {
	return &Rebuilder{store: st, writer: writer, rebuildMarker: map[int64]rebuildMarker{}}
}

// DerivedChange is one derived-lane element to overlay onto the base graph. It
// is either an inferred edge or a datastore classification (never both): the
// two shapes mirror infer.InferredEdge and classify.DataClass respectively, and
// the derived_events lane constraint keeps them distinct. ApplyRun routes each
// to the right overlay write by which field is populated.
type DerivedChange struct {
	// Inferred, when non-nil, is an inferred edge to MERGE with inferred=true,
	// confidence, and evidence. Mutually exclusive with Classification.
	Inferred *infer.InferredEdge
	// Classification, when non-nil, is a datastore data-class to SET as
	// namespaced node properties. Mutually exclusive with Inferred.
	Classification *classify.DataClass
	// Removed marks an Inferred edge as a removal (inferred.removed) rather than
	// an addition: the incremental overlay deletes the edge so the projection
	// converges on the same end state as a full rebuild, which folds a removed
	// inferred edge out entirely. Only meaningful when Inferred is non-nil.
	Removed bool
}

// InferredEdge builds a DerivedChange carrying an inferred edge overlay.
func InferredEdge(e infer.InferredEdge) DerivedChange {
	return DerivedChange{Inferred: &e}
}

// RemovedInferredEdge builds a DerivedChange that deletes a previously inferred
// edge from the overlay (the incremental counterpart of the fold omitting an
// inferred.removed row).
func RemovedInferredEdge(e infer.InferredEdge) DerivedChange {
	return DerivedChange{Inferred: &e, Removed: true}
}

// Classification builds a DerivedChange carrying a classification overlay.
func Classification(c classify.DataClass) DerivedChange {
	return DerivedChange{Classification: &c}
}

// ApplyRun is the incremental path used at the end of each ingest: it writes the
// run's observed changes (base graph) via the existing graph.Writer MERGE path,
// then the run's derived changes (overlay). No full replay.
//
// Ordering matters: the base graph is written first so overlay edges and
// classification properties attach to nodes that already exist. Steps:
//
//  1. Base graph — observed entity changes (created / attribute_updated /
//     state_changed) MERGE via SyncEntities; observed relation changes (added /
//     attribute_changed) MERGE via SyncEdges. entity.deleted changes become
//     MarkStale (absence) rather than a hard delete — reference-counted
//     deletion already decided the entity has no remaining asserter. Unchanged
//     changes carry no row and are skipped.
//  2. Overlay — inferred edges MERGE with inferred=true + confidence +
//     evidence; classifications SET namespaced node properties data_class,
//     data_class_confidence, classified_by.
//
// Every write is a MERGE/SET so re-applying the same run is a no-op on content:
// the projection is idempotent and a failed ApplyRun can simply be retried (or
// recovered by RebuildAsOf).
func (r *Rebuilder) ApplyRun(ctx context.Context, org int64, observed []temporal.Change, derived []DerivedChange) error {
	if err := r.applyRun(ctx, org, observed, derived); err != nil {
		// The run's observed/derived rows are already committed to the logs by
		// the change engine; only the Memgraph projection failed. Mark the org
		// so the authoritative recovery path (RebuildAsOf) can repair the graph
		// later — the facts are never lost, just the projection.
		r.markRebuildNeeded(org, "", fmt.Sprintf("incremental apply failed: %v", err))
		return err
	}
	return nil
}

// applyRun performs the incremental base-then-overlay projection for one run.
// ApplyRun wraps it to set the rebuild marker on any failure.
func (r *Rebuilder) applyRun(ctx context.Context, org int64, observed []temporal.Change, derived []DerivedChange) error {
	_ = org // tenant scope travels with the log fold; the projection is tenant-global in V1.

	entities, edges, removedEdges, stale := partitionObserved(observed)

	// --- base graph (observed lane) via the existing MERGE path ---
	if len(entities) > 0 {
		if _, err := r.writer.SyncEntities(ctx, entities); err != nil {
			return fmt.Errorf("replay: apply observed entities: %w", err)
		}
	}
	if len(edges) > 0 {
		if _, err := r.writer.SyncEdges(ctx, edges); err != nil {
			return fmt.Errorf("replay: apply observed edges: %w", err)
		}
	}
	// A relation.removed change makes the edge absent — the fold path omits it
	// entirely on a full rebuild, so the incremental path deletes it to converge
	// on the same end state (edges carry no stale flag, unlike nodes).
	if len(removedEdges) > 0 {
		if err := r.writer.RemoveEdges(ctx, removedEdges); err != nil {
			return fmt.Errorf("replay: remove observed edges: %w", err)
		}
	}
	// Reference-counted deletions become absence: the entity has no remaining
	// asserter, so mark it stale rather than deleting it (stale nodes stay
	// queryable and a later run can revive them). The node is kept present so
	// it matches the full-rebuild fold, which also keeps a deleted entity as a
	// stale node rather than removing it.
	if len(stale) > 0 {
		if err := r.writer.MarkStale(ctx, stale); err != nil {
			return fmt.Errorf("replay: mark deleted entities stale: %w", err)
		}
	}

	// --- derived overlay (inferred edges + classification props) ---
	if err := r.applyDerived(ctx, derived); err != nil {
		return err
	}
	return nil
}

// partitionObserved splits a run's observed changes into the base-graph writes:
// entities to MERGE, edges to MERGE, edges to remove (relation.removed), and
// canonical_ids whose entity.deleted change makes them absent (MarkStale).
// Unchanged changes are dropped (they carry no row and need no projection
// write).
func partitionObserved(observed []temporal.Change) (entities []store.GraphEntity, edges []store.GraphEdge, removedEdges []graph.EdgeRef, stale []string) {
	for _, c := range observed {
		switch c.ChangeType {
		case temporal.ChangeEntityUnchanged:
			// Coalesced upstream; nothing to project.
			continue

		case temporal.ChangeEntityDeleted:
			if c.CanonicalID != "" {
				stale = append(stale, c.CanonicalID)
			}

		case temporal.ChangeEntityCreated,
			temporal.ChangeEntityAttributeUpdated,
			temporal.ChangeEntityStateChanged:
			if ge, ok := graphEntityFromChange(c); ok {
				entities = append(entities, ge)
			}

		case temporal.ChangeRelationAdded,
			temporal.ChangeRelationAttrChanged:
			if gedge, ok := graphEdgeFromChange(c); ok {
				edges = append(edges, gedge)
			}

		case temporal.ChangeRelationRemoved:
			// A removed relation becomes absence. Delete it incrementally so the
			// end state matches a full rebuild, which folds the edge out
			// entirely (edges carry no stale flag). Endpoint nodes are kept.
			rel := model.Relation(c.Relation)
			if c.CanonicalID != "" && c.DstID != "" && model.ValidRelation(rel) {
				removedEdges = append(removedEdges, graph.EdgeRef{
					SrcID: c.CanonicalID, Relation: rel, DstID: c.DstID,
				})
			}
		}
	}
	return entities, edges, removedEdges, stale
}

// applyDerived overlays the run's derived changes: inferred edges MERGE with
// inferred=true + confidence + evidence, and classifications SET namespaced
// node properties. The base graph must already be written (ApplyRun orders it
// first) so these attach to existing nodes.
func (r *Rebuilder) applyDerived(ctx context.Context, derived []DerivedChange) error {
	var inferredEdges []store.GraphEdge
	var removedInferred []graph.EdgeRef
	for _, d := range derived {
		switch {
		case d.Inferred != nil:
			if d.Removed {
				e := *d.Inferred
				if e.SrcID != "" && e.DstID != "" && model.ValidRelation(e.Relation) {
					removedInferred = append(removedInferred, graph.EdgeRef{
						SrcID: e.SrcID, Relation: e.Relation, DstID: e.DstID,
					})
				}
				continue
			}
			if ge, ok := graphEdgeFromInferred(*d.Inferred); ok {
				inferredEdges = append(inferredEdges, ge)
			}
		case d.Classification != nil:
			if err := r.setClassification(ctx, *d.Classification); err != nil {
				return err
			}
		}
	}

	// Inferred edges reuse the SyncEdges MERGE path; the inferred=true marker,
	// confidence, and evidence ride in the edge Attributes/Confidence so a query
	// can always separate asserted facts from derivations.
	if len(inferredEdges) > 0 {
		if _, err := r.writer.SyncEdges(ctx, inferredEdges); err != nil {
			return fmt.Errorf("replay: apply inferred edges: %w", err)
		}
	}
	// Removed inferred edges become absence, matching the fold.
	if len(removedInferred) > 0 {
		if err := r.writer.RemoveEdges(ctx, removedInferred); err != nil {
			return fmt.Errorf("replay: remove inferred edges: %w", err)
		}
	}
	return nil
}

// setClassification SETs the three namespaced classification properties on a
// datastore node: data_class, data_class_confidence, classified_by. It MATCHes
// an existing node (the base graph is written first) and sets properties only;
// it never creates nodes, so a classification for an unknown id is a no-op.
func (r *Rebuilder) setClassification(ctx context.Context, c classify.DataClass) error {
	if c.CanonicalID == "" {
		return nil
	}
	sess := r.writer.Driver().NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer sess.Close(ctx)

	_, err := sess.Run(ctx,
		`MATCH (n:Entity {canonical_id: $id})
		 SET n.data_class = $class,
		     n.data_class_confidence = $confidence,
		     n.classified_by = $classified_by`,
		map[string]any{
			"id":            c.CanonicalID,
			"class":         c.Class,
			"confidence":    c.Confidence,
			"classified_by": c.ClassifiedBy,
		})
	if err != nil {
		return fmt.Errorf("replay: set classification %s: %w", c.CanonicalID, err)
	}
	return nil
}

// RebuildAsOf projects the two logs into Memgraph for a point in time: it folds
// the observed lane to the base nodes/edges and the derived lane to the overlay
// (inferred edges + classification properties), then writes them through the
// same MERGE/SET path ApplyRun uses. It is the authoritative recovery path
// (Req 7.5): when incremental ApplyRun leaves Memgraph in a suspect state,
// RebuildAsOf(now) replays the two logs from scratch, since the logs — not
// Memgraph — are the source of truth.
//
// Cutoffs are bi-temporal: eventTime is the as-of cutoff (when facts became
// true) and knownAt is the asKnownAt cutoff (when Bellu learned them). Pass
// knownAt = time.Now() for the reality view. The fold keeps the latest row per
// element under (event_time DESC, recorded_at DESC, seq DESC) and treats a
// terminal entity.deleted as absent (Req 7.2).
//
// Every write is a MERGE/SET, so folding the same logs again yields the same
// projection — rebuilding is idempotent and a failed rebuild can simply be
// retried (Req 7.3). A successful rebuild clears the org's rebuild marker.
//
// On Memgraph unavailability the committed observed/derived rows are never
// touched (this method only reads them), so the facts survive and the rebuild
// works once Memgraph returns (Req 7.6); the marker is left set so the caller
// knows a retry is still owed.
func (r *Rebuilder) RebuildAsOf(ctx context.Context, org int64, eventTime, knownAt time.Time) error {
	// --- base graph (observed lane) ---
	entities, stale, err := r.foldObservedEntities(ctx, org, eventTime, knownAt)
	if err != nil {
		return err
	}
	edges, err := r.foldObservedEdges(ctx, org, eventTime, knownAt)
	if err != nil {
		return err
	}

	if len(entities) > 0 {
		if _, err := r.writer.SyncEntities(ctx, entities); err != nil {
			return fmt.Errorf("replay: rebuild observed entities: %w", err)
		}
	}
	if len(edges) > 0 {
		if _, err := r.writer.SyncEdges(ctx, edges); err != nil {
			return fmt.Errorf("replay: rebuild observed edges: %w", err)
		}
	}
	// Entities whose latest folded row is entity.deleted (or that have no live
	// asserter at the cutoff) become absence, not a hard delete.
	if len(stale) > 0 {
		if err := r.writer.MarkStale(ctx, stale); err != nil {
			return fmt.Errorf("replay: rebuild mark stale: %w", err)
		}
	}

	// --- derived overlay (inferred edges + classification props) ---
	derived, err := r.foldDerived(ctx, org, eventTime, knownAt)
	if err != nil {
		return err
	}
	if err := r.applyDerived(ctx, derived); err != nil {
		return fmt.Errorf("replay: rebuild derived overlay: %w", err)
	}

	// The projection is now consistent with the logs ≤ (eventTime, knownAt);
	// clear any rebuild marker owed for this org.
	r.ClearRebuildMarker(org)
	return nil
}

// foldObservedEntities folds the observed lane to the latest entity row per
// canonical_id at the bi-temporal cutoff. It returns the entities to MERGE
// (those whose latest change is a create/update/state-change) and the stale set
// (those whose latest change is a terminal entity.deleted), reusing
// graphEntityFromChange to recover kind/provider/name from the folded row.
func (r *Rebuilder) foldObservedEntities(ctx context.Context, org int64, eventTime, knownAt time.Time) (entities []store.GraphEntity, stale []string, err error) {
	rows, err := r.store.Pool().Query(ctx,
		`SELECT DISTINCT ON (canonical_id)
		        canonical_id, change_type, attributes, evidence_ids
		   FROM observed_events
		  WHERE org_id = $1
		    AND element_kind = 'entity'
		    AND canonical_id IS NOT NULL
		    AND event_time  <= $2
		    AND recorded_at <= $3
		  ORDER BY canonical_id, event_time DESC, recorded_at DESC, seq DESC`,
		org, eventTime, knownAt)
	if err != nil {
		return nil, nil, fmt.Errorf("replay: fold observed entities: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			canonicalID string
			changeType  string
			attrsJSON   []byte
			evidenceIDs []string
		)
		if err := rows.Scan(&canonicalID, &changeType, &attrsJSON, &evidenceIDs); err != nil {
			return nil, nil, fmt.Errorf("replay: scan observed entity: %w", err)
		}
		if changeType == temporal.ChangeEntityDeleted {
			// A deleted entity becomes a stale node, not an absent one — the
			// incremental path (ApplyRun) created the node and then MarkStale'd
			// it, so the fold must also keep it present-but-stale. The deleted
			// row carries no attributes, so recover kind/provider from the
			// canonical_id to MERGE the node before staling it. (An id whose
			// kind cannot be recovered is dropped: there is nothing to project.)
			stale = append(stale, canonicalID)
			if ge, ok := graphEntityFromDeletedID(canonicalID); ok {
				entities = append(entities, ge)
			}
			continue
		}
		c := temporal.Change{
			ElementKind: "entity",
			ChangeType:  changeType,
			CanonicalID: canonicalID,
			Attributes:  decodeAttrs(attrsJSON),
			EvidenceIDs: evidenceIDs,
		}
		if ge, ok := graphEntityFromChange(c); ok {
			entities = append(entities, ge)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("replay: iterate observed entities: %w", err)
	}
	return entities, stale, nil
}

// foldObservedEdges folds the observed lane to the latest edge row per
// (canonical_id, relation, dst_id) at the cutoff and returns the live edges to
// MERGE. An edge whose latest change is relation.removed is folded out (left
// absent) rather than written, reusing graphEdgeFromChange for the live ones.
func (r *Rebuilder) foldObservedEdges(ctx context.Context, org int64, eventTime, knownAt time.Time) ([]store.GraphEdge, error) {
	rows, err := r.store.Pool().Query(ctx,
		`SELECT DISTINCT ON (canonical_id, relation, dst_id)
		        canonical_id, relation, dst_id, change_type, attributes, evidence_ids
		   FROM observed_events
		  WHERE org_id = $1
		    AND element_kind = 'edge'
		    AND canonical_id IS NOT NULL AND relation IS NOT NULL AND dst_id IS NOT NULL
		    AND event_time  <= $2
		    AND recorded_at <= $3
		  ORDER BY canonical_id, relation, dst_id, event_time DESC, recorded_at DESC, seq DESC`,
		org, eventTime, knownAt)
	if err != nil {
		return nil, fmt.Errorf("replay: fold observed edges: %w", err)
	}
	defer rows.Close()

	var edges []store.GraphEdge
	for rows.Next() {
		var (
			srcID       string
			relation    string
			dstID       string
			changeType  string
			attrsJSON   []byte
			evidenceIDs []string
		)
		if err := rows.Scan(&srcID, &relation, &dstID, &changeType, &attrsJSON, &evidenceIDs); err != nil {
			return nil, fmt.Errorf("replay: scan observed edge: %w", err)
		}
		if changeType == temporal.ChangeRelationRemoved {
			continue // folded out: the edge is absent at the cutoff.
		}
		c := temporal.Change{
			ElementKind: "edge",
			ChangeType:  changeType,
			CanonicalID: srcID,
			Relation:    relation,
			DstID:       dstID,
			Attributes:  decodeAttrs(attrsJSON),
			EvidenceIDs: evidenceIDs,
		}
		if ge, ok := graphEdgeFromChange(c); ok {
			edges = append(edges, ge)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("replay: iterate observed edges: %w", err)
	}
	return edges, nil
}

// foldDerived folds the derived lane to the latest row per element at the
// cutoff and returns the overlay changes: live inferred edges (latest change
// not inferred.removed) and live classifications (latest change not
// declassified). It reuses the DerivedChange shape applyDerived already routes.
func (r *Rebuilder) foldDerived(ctx context.Context, org int64, eventTime, knownAt time.Time) ([]DerivedChange, error) {
	var derived []DerivedChange

	// --- inferred edges ---
	edgeRows, err := r.store.Pool().Query(ctx,
		`SELECT DISTINCT ON (canonical_id, relation, dst_id)
		        canonical_id, relation, dst_id, change_type, confidence, producer, origin, evidence_ids
		   FROM derived_events
		  WHERE org_id = $1
		    AND kind = 'inferred_edge'
		    AND canonical_id IS NOT NULL AND relation IS NOT NULL AND dst_id IS NOT NULL
		    AND event_time  <= $2
		    AND recorded_at <= $3
		  ORDER BY canonical_id, relation, dst_id, event_time DESC, recorded_at DESC, seq DESC`,
		org, eventTime, knownAt)
	if err != nil {
		return nil, fmt.Errorf("replay: fold inferred edges: %w", err)
	}
	for edgeRows.Next() {
		var (
			srcID       string
			relation    string
			dstID       string
			changeType  string
			confidence  float64
			producer    string
			origin      string
			evidenceIDs []string
		)
		if err := edgeRows.Scan(&srcID, &relation, &dstID, &changeType, &confidence, &producer, &origin, &evidenceIDs); err != nil {
			edgeRows.Close()
			return nil, fmt.Errorf("replay: scan inferred edge: %w", err)
		}
		if changeType == derivedInferredRemoved {
			continue // folded out: inferred edge absent at the cutoff.
		}
		rel := model.Relation(relation)
		if !model.ValidRelation(rel) {
			continue
		}
		derived = append(derived, InferredEdge(infer.InferredEdge{
			SrcID:      srcID,
			Relation:   rel,
			DstID:      dstID,
			Confidence: confidence,
			Evidence:   evidenceIDs,
			Origin:     origin,
			Producer:   producer,
		}))
	}
	if err := edgeRows.Err(); err != nil {
		edgeRows.Close()
		return nil, fmt.Errorf("replay: iterate inferred edges: %w", err)
	}
	edgeRows.Close()

	// --- classifications ---
	classRows, err := r.store.Pool().Query(ctx,
		`SELECT DISTINCT ON (canonical_id)
		        canonical_id, change_type, attributes, confidence, producer, evidence_ids
		   FROM derived_events
		  WHERE org_id = $1
		    AND kind = 'classification'
		    AND canonical_id IS NOT NULL
		    AND event_time  <= $2
		    AND recorded_at <= $3
		  ORDER BY canonical_id, event_time DESC, recorded_at DESC, seq DESC`,
		org, eventTime, knownAt)
	if err != nil {
		return nil, fmt.Errorf("replay: fold classifications: %w", err)
	}
	defer classRows.Close()

	for classRows.Next() {
		var (
			canonicalID string
			changeType  string
			attrsJSON   []byte
			confidence  float64
			producer    string
			evidenceIDs []string
		)
		if err := classRows.Scan(&canonicalID, &changeType, &attrsJSON, &confidence, &producer, &evidenceIDs); err != nil {
			return nil, fmt.Errorf("replay: scan classification: %w", err)
		}
		if changeType == derivedDeclassified {
			continue // folded out: no class at the cutoff.
		}
		attrs := decodeAttrs(attrsJSON)
		class, _ := attrs["data_class"].(string)
		derived = append(derived, Classification(classify.DataClass{
			CanonicalID:  canonicalID,
			Class:        class,
			Readers:      stringSliceAttr(attrs, "readers"),
			Writers:      stringSliceAttr(attrs, "writers"),
			Confidence:   confidence,
			Evidence:     evidenceIDs,
			ClassifiedBy: producer,
		}))
	}
	if err := classRows.Err(); err != nil {
		return nil, fmt.Errorf("replay: iterate classifications: %w", err)
	}
	return derived, nil
}

// Derived change_type values whose latest fold means "absent" at the cutoff.
// They mirror the derived_events change_type CHECK (migration 00016).
const (
	derivedInferredRemoved = "inferred.removed"
	derivedDeclassified    = "declassified"
)

// --- rebuild markers (recovery signalling) ---

// markRebuildNeeded records that an org's Memgraph projection is suspect and a
// RebuildAsOf is owed. It is idempotent (the latest reason/time wins) and never
// touches the logs: the committed facts are intact, only the projection needs
// recovery.
func (r *Rebuilder) markRebuildNeeded(org int64, runID, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebuildMarker[org] = rebuildMarker{RunID: runID, Reason: reason, SetAt: time.Now().UTC()}
}

// MarkRunIncomplete flags an org's projection as incomplete for a specific run
// (e.g. the process died mid-overlay), so a later RebuildAsOf can repair the
// graph from the logs. It is the explicit entry point callers use when they
// detect a partial projection outside ApplyRun's own error path.
func (r *Rebuilder) MarkRunIncomplete(org int64, runID, reason string) {
	r.markRebuildNeeded(org, runID, reason)
}

// NeedsRebuild reports whether an org currently has a rebuild marker set, i.e.
// its Memgraph projection is suspect and RebuildAsOf should be run to recover.
func (r *Rebuilder) NeedsRebuild(org int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.rebuildMarker[org]
	return ok
}

// ClearRebuildMarker removes an org's rebuild marker. RebuildAsOf calls it on
// success; callers may also clear it after confirming the projection is sound.
func (r *Rebuilder) ClearRebuildMarker(org int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rebuildMarker, org)
}

// decodeAttrs unmarshals a JSONB attribute column, tolerating null/empty.
func decodeAttrs(b []byte) map[string]any {
	if len(b) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// stringSliceAttr reads a []string attribute by key from a JSON-decoded map,
// tolerating the []any form json.Unmarshal produces for arrays.
func stringSliceAttr(attrs map[string]any, key string) []string {
	v, ok := attrs[key]
	if !ok {
		return nil
	}
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

// graphEntityFromChange turns an observed entity Change into a store.GraphEntity
// for the MERGE path. The observed log does not store kind/provider/name as
// columns, so they are recovered from the canonical_id (which encodes kind and,
// for most kinds, provider) and from the attribute snapshot. An entity whose
// kind cannot be recovered is dropped rather than written with an invalid label
// (the writer rejects unknown kinds).
func graphEntityFromChange(c temporal.Change) (store.GraphEntity, bool) {
	if c.CanonicalID == "" {
		return store.GraphEntity{}, false
	}
	kind, provider := kindProviderFromCanonicalID(c.CanonicalID)
	if attrKind, ok := stringAttr(c.Attributes, "kind"); ok && model.ValidKind(model.Kind(attrKind)) {
		kind = model.Kind(attrKind)
	}
	if !model.ValidKind(kind) {
		return store.GraphEntity{}, false
	}
	if p, ok := stringAttr(c.Attributes, "provider"); ok && p != "" {
		provider = p
	}
	name, _ := stringAttr(c.Attributes, "name")

	return store.GraphEntity{
		CanonicalID: c.CanonicalID,
		Kind:        kind,
		Provider:    provider,
		Name:        name,
		Attributes:  c.Attributes,
		EvidenceIDs: c.EvidenceIDs,
	}, true
}

// graphEntityFromDeletedID reconstructs a store.GraphEntity for an entity whose
// latest folded row is entity.deleted. Such a row carries no attribute snapshot,
// so kind and provider come from the canonical_id alone. The node is MERGE'd so
// it exists (and RebuildAsOf then marks it stale), keeping the full-rebuild
// projection equal to the incremental one, which created-then-staled the node.
// An id whose kind cannot be recovered yields false and is dropped.
func graphEntityFromDeletedID(canonicalID string) (store.GraphEntity, bool) {
	if canonicalID == "" {
		return store.GraphEntity{}, false
	}
	kind, provider := kindProviderFromCanonicalID(canonicalID)
	if !model.ValidKind(kind) {
		return store.GraphEntity{}, false
	}
	return store.GraphEntity{
		CanonicalID: canonicalID,
		Kind:        kind,
		Provider:    provider,
		Attributes:  map[string]any{},
	}, true
}

// graphEdgeFromChange turns an observed relation Change into a store.GraphEdge
// for the MERGE path. The edge is dropped when its relation is not in the
// allow-list or an endpoint id is missing (the writer rejects unknown
// relations).
func graphEdgeFromChange(c temporal.Change) (store.GraphEdge, bool) {
	rel := model.Relation(c.Relation)
	if c.CanonicalID == "" || c.DstID == "" || !model.ValidRelation(rel) {
		return store.GraphEdge{}, false
	}
	conf := 1.0
	if v, ok := floatAttr(c.Attributes, "confidence"); ok {
		conf = v
	}
	return store.GraphEdge{
		SrcID:       c.CanonicalID,
		Relation:    rel,
		DstID:       c.DstID,
		Attributes:  c.Attributes,
		Confidence:  conf,
		EvidenceIDs: c.EvidenceIDs,
	}, true
}

// graphEdgeFromInferred turns an inferred edge into a store.GraphEdge tagged as
// a derivation: inferred=true rides in the edge properties alongside the
// evidence ids and confidence, so the projection keeps derived edges separable
// from asserted ones. Unknown relations or missing endpoints are dropped.
func graphEdgeFromInferred(e infer.InferredEdge) (store.GraphEdge, bool) {
	if e.SrcID == "" || e.DstID == "" || !model.ValidRelation(e.Relation) {
		return store.GraphEdge{}, false
	}
	attrs := map[string]any{"inferred": true}
	if e.Producer != "" {
		attrs["produced_by"] = e.Producer
	}
	if e.Origin != "" {
		attrs["origin"] = e.Origin
	}
	return store.GraphEdge{
		SrcID:       e.SrcID,
		Relation:    e.Relation,
		DstID:       e.DstID,
		Attributes:  attrs,
		Confidence:  e.Confidence,
		EvidenceIDs: e.Evidence,
	}, true
}

// specialPrefixKind maps the non-default canonical_id prefixes (set by
// resolve.CanonicalID for globally-keyed kinds) back to their model.Kind. The
// default canonical form is "kind:provider:native-id" and is handled generically.
var specialPrefixKind = map[string]model.Kind{
	"image":   model.KindContainerImage,
	"package": model.KindPackage,
	"vuln":    model.KindVulnerability,
	"ip":      model.KindNetworkEndpoint,
}

// kindProviderFromCanonicalID recovers the entity kind and provider from a
// canonical_id. For the default form "kind:provider:native-id" the first two
// segments give kind and provider; for the globally-keyed special forms
// (image:/package:/vuln:/ip:) the prefix gives the kind and provider is empty.
// It returns a zero Kind when the id matches no known shape, letting the caller
// drop the entity.
func kindProviderFromCanonicalID(id string) (model.Kind, string) {
	prefix := id
	if i := strings.IndexByte(id, ':'); i >= 0 {
		prefix = id[:i]
	}
	if k, ok := specialPrefixKind[prefix]; ok {
		return k, ""
	}
	// Default form: kind:provider:native-id. Match the lowercased kind segment
	// back to an allow-listed Kind (resolve.CanonicalID lowercases it).
	parts := strings.SplitN(id, ":", 3)
	if len(parts) < 2 {
		return model.Kind(""), ""
	}
	for _, k := range model.AllKinds {
		if strings.ToLower(string(k)) == parts[0] {
			return k, parts[1]
		}
	}
	return model.Kind(""), parts[1]
}

// stringAttr reads a string attribute by key, reporting whether it was present
// and string-typed.
func stringAttr(attrs map[string]any, key string) (string, bool) {
	if len(attrs) == 0 {
		return "", false
	}
	if v, ok := attrs[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

// floatAttr reads a numeric attribute by key, tolerating the float64/int forms
// JSON and Go produce.
func floatAttr(attrs map[string]any, key string) (float64, bool) {
	if len(attrs) == 0 {
		return 0, false
	}
	switch v := attrs[key].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}
