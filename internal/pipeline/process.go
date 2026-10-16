package pipeline

import (
	"context"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/normalize/aiagent"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/resolve"
	"github.com/bellu/bellu-ai-cyber/internal/store"
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
	Entities  int
	Edges     int
	Aliases    int
	Conflicts  int
	Agents     int
	Unresolved int
	Dropped    int
	// CanonicalIDs are the canonical ids of every entity persisted in this run,
	// so callers can tag them (e.g. map to a project).
	CanonicalIDs []string
}

// Process normalizes the given raw events, resolves canonical ids, derives AI
// agents, and persists the normalized entities/edges plus resolution aliases
// and conflicts to Postgres. It mirrors the normalize/resolve/persist block of
// `bellu run` exactly so the output is identical.
func (p *Processor) Process(ctx context.Context, st *store.Store, events []rawevent.RawEvent) (ProcessResult, error) {
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
		Entities:     len(rres.Entities),
		Edges:        len(rres.Edges),
		Aliases:      len(p.resolver.Aliases),
		Conflicts:    len(p.resolver.Conflicts),
		Agents:       len(agentEnts),
		Unresolved:   len(p.resolver.Unresolved),
		Dropped:      len(p.resolver.Dropped),
		CanonicalIDs: ids,
	}
	return out, nil
}

// Sync loads the resolved graph from Postgres and writes it into Memgraph via
// the given writer. It is a no-op (nil error) when w is nil, so callers can run
// discovery even when Memgraph is unavailable — the inventory list reads from
// Postgres and does not require the graph.
func (p *Processor) Sync(ctx context.Context, st *store.Store, w *graph.Writer) (nodes int, edges int, err error) {
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
