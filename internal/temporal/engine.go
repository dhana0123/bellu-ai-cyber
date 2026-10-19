// Package temporal implements the event-sourced temporal graph: the change
// engine (observed lane), reference-counted deletion, and the as-of/asKnownAt
// query layer. The change engine diffs a normalized run against the current
// projection cache and appends exactly-once-classified rows to the
// observed_events facts log.
package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// Observed-lane change types. These mirror the observed_events change_type
// CHECK constraint (migration 00015).
const (
	ChangeEntityCreated          = "entity.created"
	ChangeEntityAttributeUpdated = "entity.attribute_updated"
	ChangeEntityStateChanged     = "entity.state_changed"
	ChangeEntityDeleted          = "entity.deleted"
	ChangeEntityUnchanged        = "entity.unchanged"
	ChangeRelationAdded          = "relation.added"
	ChangeRelationRemoved        = "relation.removed"
	ChangeRelationAttrChanged    = "relation.attribute_changed"
)

const (
	elementEntity = "entity"
	elementEdge   = "edge"
)

// stateFieldKeys is the small set of lifecycle/exposure attributes whose change
// promotes an element to state_changed rather than a generic attribute_updated.
// The vocabulary is provider-neutral and lives in the Attributes map.
var stateFieldKeys = []string{
	"public",
	"public_access",
	"publicly_accessible",
	"enabled",
	"disabled",
	"status",
	"state",
	"lifecycle_state",
	"exposure",
	"exposed",
}

// Change is one classified difference for the observed lane.
type Change struct {
	ElementKind string         // "entity" | "edge"
	ChangeType  string         // observed_events.change_type value
	CanonicalID string         // entity canonical_id, or edge src for edge changes
	Relation    string         // edge only
	DstID       string         // edge only
	Attributes  map[string]any // new full snapshot (empty for deleted)
	Diff        Diff           // added/removed/changed attribute keys
	EvidenceIDs []string
	EventTime   time.Time // from the raw events' observed_at
}

// Diff records the attribute keys that changed between the prior and current
// snapshot of an element. It is informational payload stored alongside the row.
type Diff struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

// empty reports whether the diff carries no key changes.
func (d Diff) empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Engine diffs a run against current state and appends observed_events. It
// reuses the normalized_entities/normalized_edges tables as a fast "last known
// state" cache for the diff.
type Engine struct {
	store *store.Store
	// now is injectable for deterministic tests; defaults to time.Now.
	now func() time.Time
}

// NewEngine constructs a change engine over the given store.
func NewEngine(st *store.Store) *Engine {
	return &Engine{store: st, now: time.Now}
}

// Apply classifies every element in res against current state for (org, source),
// writes the resulting observed_events in one transaction, and updates the
// entity_assertions reference counts. It returns the changes for the caller to
// hand to the replay step. Unchanged elements are coalesced (no row written).
//
// evidence maps an element's identity key to the raw-event ids that justify it:
// an entity's CanonicalID, or an edge's "src|relation|dst" key. Missing entries
// yield an empty evidence slice.
//
// All observed events and entity_assertions updates for the run are persisted
// in a single transaction; on any failure the transaction rolls back entirely
// and the error is returned without proceeding to projection. Writes are
// serialized per-org via a transaction-scoped advisory lock.
func (e *Engine) Apply(ctx context.Context, org int64, runID, source string, res normalize.Result, evidence map[string][]string) ([]Change, error) {
	recordedAt := e.now().UTC()
	eventTime := runEventTime(res, recordedAt)

	// Load the current projection cache for only the elements this run touches,
	// so the diff cost is bounded by change volume, not inventory size.
	current, err := e.loadCurrent(ctx, res)
	if err != nil {
		return nil, fmt.Errorf("temporal: load current state: %w", err)
	}

	changes := classifyChanges(source, res, current, evidence, eventTime)

	tx, err := e.store.Pool().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("temporal: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	// Serialize per-org so concurrent runs for the same tenant do not interleave
	// their observed_events / entity_assertions writes. The lock is released
	// automatically when the transaction ends.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, org); err != nil {
		return nil, fmt.Errorf("temporal: acquire per-org lock: %w", err)
	}

	// Reference-counted deletions share this transaction. They are emitted here
	// so a failed/aborted run retains assertions and emits no deletions.
	// (Full implementation lands in task 9.4; the hook keeps Apply's contract
	// stable so deletions compose into the same transaction and change set.)
	deletions, err := e.referenceCountedDeletions(ctx, tx, org, runID, source, res, recordedAt)
	if err != nil {
		return nil, fmt.Errorf("temporal: reference-counted deletions: %w", err)
	}
	changes = append(changes, deletions...)

	// Write observed-event rows only for added/changed/removed elements.
	// Unchanged elements are coalesced: no row, but the assertion is refreshed.
	for _, c := range changes {
		if c.ChangeType == ChangeEntityUnchanged {
			continue
		}
		if err := e.writeObservedEvent(ctx, tx, org, runID, source, recordedAt, c); err != nil {
			return nil, fmt.Errorf("temporal: write observed event: %w", err)
		}
	}

	// Upsert an entity_assertions row for every entity this run asserted, so the
	// reference set reflects the run (deletions above already dropped the ones
	// this source no longer asserts).
	for _, ent := range res.Entities {
		if err := e.upsertAssertion(ctx, tx, org, ent.CanonicalID, source, runID, recordedAt); err != nil {
			return nil, fmt.Errorf("temporal: upsert assertion %s: %w", ent.CanonicalID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("temporal: commit: %w", err)
	}
	committed = true
	return changes, nil
}

// runEventTime returns the time the run's facts became true, taken from the
// run's raw-event observed times (the latest evidence observed_at across the
// result). It falls back to recordedAt when the run carries no evidence times.
func runEventTime(res normalize.Result, recordedAt time.Time) time.Time {
	var latest time.Time
	consider := func(evs []model.Evidence) {
		for _, ev := range evs {
			if ev.ObservedAt.After(latest) {
				latest = ev.ObservedAt
			}
		}
	}
	for _, en := range res.Entities {
		consider(en.Evidence)
	}
	for _, ed := range res.Edges {
		consider(ed.Evidence)
	}
	if latest.IsZero() {
		return recordedAt
	}
	return latest.UTC()
}

// currentState is the subset of the projection cache relevant to a run.
type currentState struct {
	entity map[string]map[string]any  // canonical_id -> attributes
	edge   map[edgeKey]map[string]any // (src,rel,dst) -> attributes
}

type edgeKey struct {
	src string
	rel string
	dst string
}

// loadCurrent reads the attributes of only the entities and edges referenced by
// the run from normalized_entities/normalized_edges. Elements absent from the
// cache are treated as new (prev = NULL).
func (e *Engine) loadCurrent(ctx context.Context, res normalize.Result) (currentState, error) {
	cs := currentState{
		entity: make(map[string]map[string]any, len(res.Entities)),
		edge:   make(map[edgeKey]map[string]any, len(res.Edges)),
	}
	pool := e.store.Pool()

	for _, en := range res.Entities {
		if en.CanonicalID == "" {
			continue
		}
		if _, seen := cs.entity[en.CanonicalID]; seen {
			continue
		}
		var attrs []byte
		err := pool.QueryRow(ctx,
			`SELECT attributes FROM normalized_entities WHERE canonical_id = $1`,
			en.CanonicalID).Scan(&attrs)
		if err == pgx.ErrNoRows {
			continue // prev = NULL -> created
		}
		if err != nil {
			return cs, err
		}
		cs.entity[en.CanonicalID] = decodeAttrs(attrs)
	}

	for _, ed := range res.Edges {
		k := edgeKey{src: ed.SrcCanonicalID, rel: string(ed.Relation), dst: ed.DstCanonicalID}
		if k.src == "" || k.dst == "" {
			continue
		}
		if _, seen := cs.edge[k]; seen {
			continue
		}
		var attrs []byte
		err := pool.QueryRow(ctx,
			`SELECT attributes FROM normalized_edges WHERE src_id = $1 AND relation = $2 AND dst_id = $3`,
			k.src, k.rel, k.dst).Scan(&attrs)
		if err == pgx.ErrNoRows {
			continue // prev = NULL -> added
		}
		if err != nil {
			return cs, err
		}
		cs.edge[k] = decodeAttrs(attrs)
	}
	return cs, nil
}

// classifyChanges diffs the run's entities and edges against current state,
// emitting exactly one classification per element (first matching branch wins).
// Deletions are reference-counted and handled separately in Apply.
func classifyChanges(source string, res normalize.Result, current currentState, evidence map[string][]string, eventTime time.Time) []Change {
	changes := make([]Change, 0, len(res.Entities)+len(res.Edges))

	// ---- Entities ----
	for _, en := range res.Entities {
		if en.CanonicalID == "" {
			continue
		}
		attrs := cloneAttrs(en.Attributes)
		c := Change{
			ElementKind: elementEntity,
			CanonicalID: en.CanonicalID,
			Attributes:  attrs,
			EvidenceIDs: evidenceFor(evidence, en.CanonicalID, en.Evidence),
			EventTime:   eventTime,
		}
		prev, exists := current.entity[en.CanonicalID]
		switch {
		case !exists:
			c.ChangeType = ChangeEntityCreated
			c.Diff = Diff{Added: sortedKeys(attrs)}
		case !equalAttrs(stateFields(attrs), stateFields(prev)):
			c.ChangeType = ChangeEntityStateChanged
			c.Diff = diffAttrs(prev, attrs)
		case !equalAttrs(attrs, prev):
			c.ChangeType = ChangeEntityAttributeUpdated
			c.Diff = diffAttrs(prev, attrs)
		default:
			c.ChangeType = ChangeEntityUnchanged
		}
		changes = append(changes, c)
	}

	// ---- Edges ----
	for _, ed := range res.Edges {
		if ed.SrcCanonicalID == "" || ed.DstCanonicalID == "" {
			continue
		}
		attrs := cloneAttrs(ed.Attributes)
		k := edgeKey{src: ed.SrcCanonicalID, rel: string(ed.Relation), dst: ed.DstCanonicalID}
		c := Change{
			ElementKind: elementEdge,
			CanonicalID: ed.SrcCanonicalID,
			Relation:    string(ed.Relation),
			DstID:       ed.DstCanonicalID,
			Attributes:  attrs,
			EvidenceIDs: evidenceFor(evidence, edgeEvidenceKey(k), ed.Evidence),
			EventTime:   eventTime,
		}
		prev, exists := current.edge[k]
		switch {
		case !exists:
			c.ChangeType = ChangeRelationAdded
			c.Diff = Diff{Added: sortedKeys(attrs)}
		case !equalAttrs(attrs, prev):
			c.ChangeType = ChangeRelationAttrChanged
			c.Diff = diffAttrs(prev, attrs)
		default:
			c.ChangeType = ChangeEntityUnchanged // coalesced; no row written for edges either
		}
		changes = append(changes, c)
	}

	return changes
}

// referenceCountedDeletions compares the entities this run (for this source)
// asserted against the entity_assertions this source held before the run. Any
// entity the source previously asserted but did not assert this run has its
// assertion row removed; if that leaves zero assertion rows for the entity
// across ALL sources, an entity.deleted change is emitted and returned.
//
// It runs inside Apply's transaction (tx), so the decrement, the remaining-count
// check, and the emitted deletion commit or roll back atomically with the rest
// of the run: a failed or aborted run retains this source's assertions and
// emits no reference-counted deletions (Req 5.5, 5.6).
func (e *Engine) referenceCountedDeletions(ctx context.Context, tx pgx.Tx, org int64, runID, source string, res normalize.Result, recordedAt time.Time) ([]Change, error) {
	_ = runID

	// priorIDs: canonical_ids this source currently asserts, read inside the
	// transaction (and under the per-org advisory lock Apply already holds).
	rows, err := tx.Query(ctx,
		`SELECT canonical_id FROM entity_assertions WHERE org_id = $1 AND source = $2`,
		org, source)
	if err != nil {
		return nil, fmt.Errorf("select prior assertions: %w", err)
	}
	var priorIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan prior assertion: %w", err)
		}
		priorIDs = append(priorIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prior assertions: %w", err)
	}

	// assertedIDs: the set of entities this run asserted for this source.
	assertedIDs := make(map[string]struct{}, len(res.Entities))
	for _, en := range res.Entities {
		if en.CanonicalID == "" {
			continue
		}
		assertedIDs[en.CanonicalID] = struct{}{}
	}

	// dropped = priorIDs \ assertedIDs: entities this source previously asserted
	// but no longer does.
	var deletions []Change
	for _, id := range priorIDs {
		if _, still := assertedIDs[id]; still {
			continue
		}

		// Decrement: drop this source's assertion row for the dropped entity.
		if _, err := tx.Exec(ctx,
			`DELETE FROM entity_assertions
			 WHERE org_id = $1 AND canonical_id = $2 AND source = $3`,
			org, id, source); err != nil {
			return nil, fmt.Errorf("delete dropped assertion %s: %w", id, err)
		}

		// Count remaining assertions for this entity across ALL sources.
		var remaining int64
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM entity_assertions WHERE org_id = $1 AND canonical_id = $2`,
			org, id).Scan(&remaining); err != nil {
			return nil, fmt.Errorf("count remaining assertions %s: %w", id, err)
		}

		// Only emit entity.deleted when no source asserts the entity anymore.
		if remaining == 0 {
			deletions = append(deletions, Change{
				ElementKind: elementEntity,
				ChangeType:  ChangeEntityDeleted,
				CanonicalID: id,
				Attributes:  map[string]any{},
				EventTime:   recordedAt,
			})
		}
	}
	return deletions, nil
}

// writeObservedEvent inserts one observed_events row for a classified change.
func (e *Engine) writeObservedEvent(ctx context.Context, tx pgx.Tx, org int64, runID, source string, recordedAt time.Time, c Change) error {
	attrs, err := json.Marshal(nonNilMap(c.Attributes))
	if err != nil {
		return err
	}
	diff, err := json.Marshal(c.Diff)
	if err != nil {
		return err
	}
	var relation, dstID any
	if c.ElementKind == elementEdge {
		relation = c.Relation
		dstID = c.DstID
	}
	evidenceIDs := c.EvidenceIDs
	if evidenceIDs == nil {
		evidenceIDs = []string{}
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO observed_events
		   (org_id, run_id, source, element_kind, change_type, canonical_id,
		    relation, dst_id, attributes, diff, evidence_ids, event_time, recorded_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		org, runID, source, c.ElementKind, c.ChangeType, c.CanonicalID,
		relation, dstID, attrs, diff, evidenceIDs, c.EventTime, recordedAt)
	return err
}

// upsertAssertion refreshes the reference-counting row for an asserted entity.
func (e *Engine) upsertAssertion(ctx context.Context, tx pgx.Tx, org int64, canonicalID, source, runID string, seen time.Time) error {
	if canonicalID == "" {
		return nil
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO entity_assertions (org_id, canonical_id, source, last_run_id, last_seen)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (org_id, canonical_id, source) DO UPDATE SET
		   last_run_id = EXCLUDED.last_run_id,
		   last_seen   = EXCLUDED.last_seen`,
		org, canonicalID, source, runID, seen)
	return err
}

// --- attribute helpers ---

// stateFields extracts the lifecycle/exposure subset of an attribute map so a
// toggle of one of those keys classifies as state_changed.
func stateFields(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any)
	for _, k := range stateFieldKeys {
		if v, ok := attrs[k]; ok {
			out[k] = v
		}
	}
	return out
}

// equalAttrs reports deep equality of two attribute maps, treating nil and
// empty as equal.
func equalAttrs(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// diffAttrs computes the added/removed/changed attribute keys between a prior
// and current snapshot.
func diffAttrs(prev, cur map[string]any) Diff {
	var d Diff
	for k, cv := range cur {
		pv, ok := prev[k]
		if !ok {
			d.Added = append(d.Added, k)
			continue
		}
		if !reflect.DeepEqual(pv, cv) {
			d.Changed = append(d.Changed, k)
		}
	}
	for k := range prev {
		if _, ok := cur[k]; !ok {
			d.Removed = append(d.Removed, k)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	if d.empty() {
		return Diff{}
	}
	return d
}

// sortedKeys returns the attribute map's keys in deterministic order.
func sortedKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cloneAttrs returns a shallow copy so the Change does not alias the model's map.
func cloneAttrs(m map[string]any) map[string]any {
	if len(m) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
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

// edgeEvidenceKey is the evidence-map key for an edge: "src|relation|dst".
func edgeEvidenceKey(k edgeKey) string {
	return k.src + "|" + k.rel + "|" + k.dst
}

// evidenceFor returns the explicitly supplied evidence ids for an element key,
// falling back to the ids carried on the element's own Evidence slice.
func evidenceFor(evidence map[string][]string, key string, fallback []model.Evidence) []string {
	if ids, ok := evidence[key]; ok {
		return ids
	}
	if len(fallback) == 0 {
		return nil
	}
	ids := make([]string, 0, len(fallback))
	for _, ev := range fallback {
		if ev.RawEventID != "" {
			ids = append(ids, ev.RawEventID)
		}
	}
	return ids
}
