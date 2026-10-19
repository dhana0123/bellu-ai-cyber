package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/normalize/aiagent"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/resolve"
	"github.com/bellu/bellu-ai-cyber/internal/store"
	"github.com/bellu/bellu-ai-cyber/internal/temporal"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/classify"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/export"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/infer"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/replay"
)

// Processor runs the normalize -> resolve -> derive-agents -> persist stage of
// the pipeline (everything after raw events are in Postgres) and, optionally,
// syncs the resolved graph into Memgraph. It is shared by the `bellu` CLI and
// the control-panel API's connector discovery runner so both produce identical
// normalized entities/edges in `normalized_entities` (which GET /api/entities
// reads) without duplicating the wiring.
//
// Optional rule/mapping files (resolver repo->image mappings, AI-agent rules)
// are loaded defensively: when absent (e.g. in a service image that ships only
// the binary) sensible defaults are used, so the processor works regardless of
// the working directory.
type Processor struct {
	norm     *normalize.Registry
	resolver *resolve.Deterministic
	agents   aiagent.Rules
	// engine is the temporal change engine. When set, Process runs it after
	// normalize+resolve and before PersistNormalized, so it diffs the run
	// against the prior projection cache and appends observed_events. nil means
	// the temporal lane is disabled (e.g. the CLI run without a configured
	// temporal store), and Process behaves exactly as before.
	engine *temporal.Engine
	// infer and classify are the derived-lane engines. When both are set (via
	// WithDerived) Process runs them AFTER the change engine over the same
	// normalized run, persists their output to derived_events, and returns the
	// derived changes so the projection can overlay them. They read only
	// observed/normalized facts, never the derived lane, preserving the
	// facts/derived separation. nil disables the derived lane (Process behaves
	// exactly as before).
	infer    *infer.Engine
	classify classify.Classifier
	// rebuilder is the temporal replay/overlay projector. When set, Sync hands
	// the run's observed changes and derived changes to its incremental ApplyRun
	// so inferred edges and classification properties are overlaid on the base
	// graph (inferred=true + confidence + evidence on edges; data_class /
	// data_class_confidence / classified_by on nodes). nil leaves the derived
	// overlay unprojected (the base-graph sync still runs).
	rebuilder *replay.Rebuilder
	// exporter is the OPTIONAL OTLP change exporter (Req 15). When enabled,
	// Process hands the run's observed-lane changes to it after the change
	// engine records them, emitting them as OTLP entity events. It is fully
	// failure-isolated: a nil exporter, a disabled exporter, or a failing sink
	// never fails ingest or projection (the Exporter.Export call always returns
	// nil). nil leaves ingest/projection completing without exporting (Req 15.2).
	exporter *export.Exporter
}

// WithExporter attaches the OPTIONAL OTLP change exporter so Process emits the
// run's observed-lane changes as OTLP entity events after the change engine
// records them. It returns the receiver for chaining. A nil or disabled
// exporter is a no-op: ingest and projection complete without exporting
// (Req 15.2), and because Export is failure-isolated a failing exporter never
// fails the run (Req 15.1).
func (p *Processor) WithExporter(exp *export.Exporter) *Processor {
	p.exporter = exp
	return p
}

// WithEngine attaches a temporal change engine so Process records observed
// events for every ingest. It returns the receiver for chaining and is a no-op
// when eng is nil.
func (p *Processor) WithEngine(eng *temporal.Engine) *Processor {
	p.engine = eng
	return p
}

// WithDerived attaches the derived-lane inference and classification engines so
// Process runs them after the change engine, persists their output to
// derived_events, and returns the derived changes for the projection overlay.
// It returns the receiver for chaining. The derived lane stays disabled unless
// both engines are non-nil, keeping the facts-only path unchanged for callers
// (e.g. the CLI) that do not configure it.
func (p *Processor) WithDerived(inf *infer.Engine, cls classify.Classifier) *Processor {
	p.infer = inf
	p.classify = cls
	return p
}

// WithRebuilder attaches the temporal replay/overlay projector so Sync overlays
// the run's derived changes (inferred edges + classification properties) on the
// base graph through the Rebuilder's incremental ApplyRun. It returns the
// receiver for chaining and is a no-op when rb is nil.
func (p *Processor) WithRebuilder(rb *replay.Rebuilder) *Processor {
	p.rebuilder = rb
	return p
}

// NewProcessor builds a Processor with the full normalizer registry and the
// deterministic resolver. resolveMappingsPath and agentRulesPath are optional
// on-disk override files; pass "" to use defaults.
func NewProcessor(resolveMappingsPath, agentRulesPath string) *Processor {
	r := normalize.NewRegistry()
	r.Register(normalize.CycloneDX{})
	r.Register(normalize.AWS{})
	r.Register(normalize.AWSNetwork{})
	r.Register(normalize.AWSEntryPoint{})
	r.Register(normalize.Azure{})
	r.Register(normalize.GCP{})
	r.Register(normalize.K8s{})
	r.Register(normalize.Network{})

	mappings, _ := resolve.LoadMappings(resolveMappingsPath)
	rules, _ := aiagent.LoadRules(agentRulesPath)

	return &Processor{
		norm:     r,
		resolver: resolve.NewDeterministic(mappings),
		agents:   rules,
	}
}

// ProcessResult reports what a Process run produced.
type ProcessResult struct {
	Entities   int
	Edges      int
	Aliases    int
	Conflicts  int
	Agents     int
	Unresolved int
	Dropped    int
	// CanonicalIDs are the canonical ids of every entity persisted in this run,
	// so callers can tag them (e.g. map to a project).
	CanonicalIDs []string
	// Changes are the classified observed-lane changes the temporal engine
	// produced for this run (empty when no engine is attached). The caller hands
	// them to the Memgraph projection / replay step. Threading them here keeps
	// the change-engine run between normalize and projection without the
	// projection re-diffing.
	Changes []temporal.Change
	// DerivedChanges are the derived-lane overlay elements (inferred edges and
	// datastore classifications) produced for this run (empty when the derived
	// lane is disabled). They are already persisted to derived_events; the
	// caller hands them to the replay overlay alongside Changes.
	DerivedChanges []replay.DerivedChange
}

// Process normalizes the given raw events, resolves canonical ids, derives AI
// agents, runs the temporal change engine (when attached), and persists the
// normalized entities/edges plus resolution aliases and conflicts to Postgres.
// It mirrors the normalize/resolve/persist block of `bellu run` exactly so the
// output is identical.
//
// org/runID/source identify the run for the temporal change engine. When no
// engine is attached they are unused and Process behaves exactly as before.
func (p *Processor) Process(ctx context.Context, st *store.Store, events []rawevent.RawEvent, org int64, runID, source string) (ProcessResult, error) {
	var out ProcessResult

	nres, err := p.norm.Normalize(events)
	if err != nil {
		return out, fmt.Errorf("normalize: %w", err)
	}
	rres, err := p.resolver.Resolve(nres)
	if err != nil {
		return out, fmt.Errorf("resolve: %w", err)
	}

	agentEnts, agentEdges := aiagent.Derive(p.agents, rres.Entities, rres.Edges)
	rres.Entities = append(rres.Entities, agentEnts...)
	rres.Edges = append(rres.Edges, agentEdges...)

	// Change engine (observed lane): diff this run against the current
	// projection cache (normalized_entities/normalized_edges) and append
	// observed_events BEFORE PersistNormalized overwrites that cache. Running it
	// after persist would compare the run against itself and classify everything
	// unchanged. The returned changes flow to the Memgraph projection via
	// ProcessResult.Changes. The engine resolves canonical ids, so it runs after
	// resolve. A nil engine disables the lane entirely.
	var changes []temporal.Change
	if p.engine != nil {
		changes, err = p.engine.Apply(ctx, org, runID, source, rres, nil)
		if err != nil {
			return out, fmt.Errorf("change engine: %w", err)
		}
	}

	// OPTIONAL OTLP export (Req 15): once the observed facts are committed, hand
	// the run's changes to the OTLP exporter. This is strictly best-effort and
	// fully failure-isolated — Export always returns nil, so a disabled exporter
	// is a no-op (ingest/projection complete without exporting, Req 15.2) and a
	// failing exporter never fails ingest or projection (Req 15.1). A nil
	// exporter skips the call entirely.
	if p.exporter != nil {
		_ = p.exporter.Export(ctx, changes)
	}

	// Derived lane: after the change engine records the observed facts, run the
	// inference and classification engines over the SAME normalized run. They
	// read only observed/normalized data (never the derived lane), so running
	// them here keeps the hard facts/derived separation. Their output is
	// persisted to derived_events and returned as the projection overlay. The
	// derived lane is skipped entirely when either engine is unset.
	var derived []replay.DerivedChange
	if p.infer != nil && p.classify != nil {
		derived, err = p.runDerived(ctx, st, rres, org, runID)
		if err != nil {
			return out, fmt.Errorf("derived lane: %w", err)
		}
	}

	if err := st.PersistNormalized(ctx, rres.Entities, rres.Edges); err != nil {
		return out, fmt.Errorf("persist normalized: %w", err)
	}
	if err := p.persistResolution(ctx, st); err != nil {
		return out, fmt.Errorf("persist resolution: %w", err)
	}

	ids := make([]string, 0, len(rres.Entities))
	for _, e := range rres.Entities {
		if e.CanonicalID != "" {
			ids = append(ids, e.CanonicalID)
		}
	}

	out = ProcessResult{
		Entities:       len(rres.Entities),
		Edges:          len(rres.Edges),
		Aliases:        len(p.resolver.Aliases),
		Conflicts:      len(p.resolver.Conflicts),
		Agents:         len(agentEnts),
		Unresolved:     len(p.resolver.Unresolved),
		Dropped:        len(p.resolver.Dropped),
		CanonicalIDs:   ids,
		Changes:        changes,
		DerivedChanges: derived,
	}
	return out, nil
}

// runDerived runs the inference and classification engines over a normalized
// run, persists their output to the derived_events log, and returns the derived
// overlay changes. It reads only the normalized Result (observed facts) and
// never the derived lane, so inference/classification can never corrupt the
// facts (Req 4.3). The derived rows are persisted first so the log — the source
// of truth — carries them even if the projection overlay later fails; the
// returned changes are the same values handed to the replay overlay.
func (p *Processor) runDerived(ctx context.Context, st *store.Store, res normalize.Result, org int64, runID string) ([]replay.DerivedChange, error) {
	// Evidence linkage keyed by entity canonical id, built from the normalized
	// entities so inferred edges and classifications carry provenance.
	evidence := evidenceByCanonicalID(res.Entities)

	// Inference: candidate cross-source edges destined solely for the derived
	// lane (Req 8.1).
	inferred, err := p.infer.Infer(ctx, infer.InputFromResult(org, runID, res, evidence))
	if err != nil {
		return nil, fmt.Errorf("infer: %w", err)
	}

	// Classification: datastore data-class + reader/writer sets from metadata
	// only (Req 9.1).
	classes, err := p.classify.Classify(ctx, classify.ClassifyInput{Entities: res.Entities, Edges: res.Edges})
	if err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}

	eventTime := runEventTime(res)

	// Persist to derived_events (Req 4.4, 8.1, 9.2), then return the overlay
	// changes for the projection.
	rows := make([]store.DerivedEventRow, 0, len(inferred)+len(classes))
	changes := make([]replay.DerivedChange, 0, len(inferred)+len(classes))
	for _, e := range inferred {
		rows = append(rows, store.DerivedEventRow{
			Kind:        "inferred_edge",
			ChangeType:  "inferred.added",
			Origin:      e.Origin,
			Producer:    e.Producer,
			CanonicalID: e.SrcID,
			Relation:    string(e.Relation),
			DstID:       e.DstID,
			Confidence:  e.Confidence,
			EvidenceIDs: e.Evidence,
			EventTime:   eventTime,
		})
		changes = append(changes, replay.InferredEdge(e))
	}
	for _, c := range classes {
		rows = append(rows, store.DerivedEventRow{
			Kind:        "classification",
			ChangeType:  "classified",
			Origin:      classifyOrigin(p.classify),
			Producer:    c.ClassifiedBy,
			CanonicalID: c.CanonicalID,
			Attributes: map[string]any{
				"data_class": c.Class,
				"readers":    c.Readers,
				"writers":    c.Writers,
			},
			Confidence:  c.Confidence,
			EvidenceIDs: c.Evidence,
			EventTime:   eventTime,
		})
		changes = append(changes, replay.Classification(c))
	}

	if err := st.InsertDerivedEvents(ctx, org, runID, rows); err != nil {
		return nil, fmt.Errorf("persist derived events: %w", err)
	}
	return changes, nil
}

// evidenceByCanonicalID builds the per-entity evidence linkage the inference
// engine reads: canonical id -> the raw-event ids that justify the entity.
func evidenceByCanonicalID(entities []model.Entity) map[string][]string {
	evidence := make(map[string][]string, len(entities))
	for _, e := range entities {
		if e.CanonicalID == "" || len(e.Evidence) == 0 {
			continue
		}
		ids := make([]string, 0, len(e.Evidence))
		for _, ev := range e.Evidence {
			if ev.RawEventID != "" {
				ids = append(ids, ev.RawEventID)
			}
		}
		if len(ids) > 0 {
			evidence[e.CanonicalID] = ids
		}
	}
	return evidence
}

// runEventTime returns the time the run's derivations become true: the latest
// evidence observed_at across the normalized result, falling back to now when
// the run carries no evidence times. It mirrors the change engine's event_time
// axis so the derived lane folds on the same timeline as the observed lane.
func runEventTime(res normalize.Result) time.Time {
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
		return time.Now().UTC()
	}
	return latest.UTC()
}

// classifyOrigin reports the derived_events origin for a classifier ("rule" or
// "model"), reading it from the classifier's Origin so the derived row records
// which stage produced it.
func classifyOrigin(c classify.Classifier) string {
	if c == nil {
		return "rule"
	}
	return c.Origin()
}

// Sync loads the resolved graph from Postgres and writes it into Memgraph via
// the given writer. It is a no-op (nil error) when w is nil, so callers can run
// discovery even when Memgraph is unavailable — the inventory list reads from
// Postgres and does not require the graph.
//
// changes carries the observed-lane change engine's output and derived the
// derived-lane overlay (inferred edges + classifications) for this run. When a
// Rebuilder is attached (WithRebuilder) Sync hands both to the incremental
// replay overlay (ApplyRun) so inferred edges land with inferred=true +
// confidence + evidence and classified datastores get the three namespaced node
// properties. Sync still performs the full base-graph sync so the inventory
// (which reads normalized_entities from Postgres, not Memgraph) stays current
// and the base graph is populated before the overlay attaches to it.
//
// org scopes the overlay to the tenant. A nil Rebuilder leaves the derived
// overlay unprojected (the base-graph sync alone runs), preserving the prior
// behavior for callers that do not configure the derived lane.
func (p *Processor) Sync(ctx context.Context, st *store.Store, w *graph.Writer, org int64, changes []temporal.Change, derived []replay.DerivedChange) (nodes int, edges int, err error) {
	if w == nil {
		return 0, 0, nil
	}
	if err := w.EnsureSchema(ctx); err != nil {
		return 0, 0, fmt.Errorf("ensure schema: %w", err)
	}
	ents, err := st.LoadGraphEntities(ctx)
	if err != nil {
		return 0, 0, err
	}
	eds, err := st.LoadGraphEdges(ctx)
	if err != nil {
		return 0, 0, err
	}
	ne, err := w.SyncEntities(ctx, ents)
	if err != nil {
		return 0, 0, fmt.Errorf("sync entities: %w", err)
	}
	nr, gaps, err := w.SyncEdgesWithGaps(ctx, eds)
	if err != nil {
		return 0, 0, fmt.Errorf("sync edges: %w", err)
	}
	// Visibility (R2.3): persist any graph-write gaps. The graph package stays
	// free of a Postgres dependency; the pipeline owns persistence.
	if len(gaps) > 0 {
		rows := make([]store.GraphWriteGapRow, 0, len(gaps))
		for _, g := range gaps {
			rows = append(rows, store.GraphWriteGapRow{
				Relation: g.Relation, SyncRunID: g.SyncRunID,
				BatchSize: g.BatchSize, Observed: g.Observed,
			})
		}
		if err := st.SaveGraphWriteGaps(ctx, rows); err != nil {
			return ne, nr, fmt.Errorf("persist graph write gaps: %w", err)
		}
	}

	// Derived overlay: once the base graph is current, overlay the run's derived
	// changes (inferred edges + classification node properties) through the
	// replay Rebuilder's incremental ApplyRun. ApplyRun re-applies the run's
	// observed changes too, but every write is a MERGE/SET so re-touching the
	// base graph that the full sync just wrote is idempotent — the overlay just
	// attaches inferred=true edges and the three namespaced classification props
	// to the nodes already present. A nil Rebuilder (derived lane not wired)
	// skips the overlay entirely. The overlay is best-effort relative to the
	// inventory: the facts live in the two logs, so if it fails the Rebuilder
	// marks the org for a full RebuildAsOf rather than losing anything.
	if p.rebuilder != nil && (len(changes) > 0 || len(derived) > 0) {
		if err := p.rebuilder.ApplyRun(ctx, org, changes, derived); err != nil {
			return ne, nr, fmt.Errorf("apply derived overlay: %w", err)
		}
	}

	return ne, nr, nil
}

func (p *Processor) persistResolution(ctx context.Context, st *store.Store) error {
	aliases := make([]store.AliasRow, 0, len(p.resolver.Aliases))
	for _, a := range p.resolver.Aliases {
		aliases = append(aliases, store.AliasRow{AliasKey: a.AliasKey, CanonicalID: a.CanonicalID, Source: a.Source})
	}
	if err := st.SaveAliases(ctx, aliases); err != nil {
		return err
	}
	conflicts := make([]store.ConflictRow, 0, len(p.resolver.Conflicts))
	for _, c := range p.resolver.Conflicts {
		conflicts = append(conflicts, store.ConflictRow{Key: c.Key, Reason: c.Reason, Detail: c.Detail})
	}
	if err := st.SaveConflicts(ctx, conflicts); err != nil {
		return err
	}

	// Visibility records (R1.5, R1.6): persist unresolved edges and dropped
	// entities so silent link loss becomes queryable.
	unresolved := make([]store.UnresolvedEdgeRow, 0, len(p.resolver.Unresolved))
	for _, u := range p.resolver.Unresolved {
		unresolved = append(unresolved, store.UnresolvedEdgeRow{
			Relation: u.Relation, SrcKey: u.SrcKey, DstKey: u.DstKey,
			UnmatchedSide: u.UnmatchedSide, EvidenceID: u.EvidenceID, RunID: u.RunID,
		})
	}
	if err := st.SaveUnresolvedEdges(ctx, unresolved); err != nil {
		return err
	}
	dropped := make([]store.DroppedEntityRow, 0, len(p.resolver.Dropped))
	for _, dd := range p.resolver.Dropped {
		dropped = append(dropped, store.DroppedEntityRow{
			Kind: dd.Kind, Provider: dd.Provider, EvidenceID: dd.EvidenceID, RunID: dd.RunID,
		})
	}
	return st.SaveDroppedEntities(ctx, dropped)
}

var _ = model.Entity{}
