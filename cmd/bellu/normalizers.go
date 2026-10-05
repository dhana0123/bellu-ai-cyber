package main

import (
	"context"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/normalize/aiagent"
	"github.com/bellu/bellu-ai-cyber/internal/resolve"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// buildNormalizers wires the normalizer registry. Cloud/k8s/zeek normalizers
// are added in later tasks.
func buildNormalizers() *normalize.Registry {
	r := normalize.NewRegistry()
	r.Register(normalize.CycloneDX{})
	r.Register(normalize.AWS{})
	r.Register(normalize.Azure{})
	r.Register(normalize.GCP{})
	r.Register(normalize.K8s{})
	r.Register(normalize.Network{})
	return r
}

// buildResolver returns the deterministic multi-key resolver, loading optional
// repo->image fallback mappings.
func buildResolver() *resolve.Deterministic {
	return resolve.NewDeterministic(loadRepoImageMappings())
}

// loadRepoImageMappings reads internal/resolve/mappings.yaml if present.
func loadRepoImageMappings() map[string]string {
	m, _ := resolve.LoadMappings("internal/resolve/mappings.yaml")
	return m
}

// deriveAgents runs AI-agent detection over the resolved graph, loading rules
// from internal/normalize/aiagent/rules.yaml (defaults if absent).
func deriveAgents(entities []model.Entity, edges []model.Edge) ([]model.Entity, []model.Edge) {
	rules, _ := aiagent.LoadRules("internal/normalize/aiagent/rules.yaml")
	return aiagent.Derive(rules, entities, edges)
}

// persistResolution writes the resolver's aliases and conflicts to Postgres.
func persistResolution(ctx context.Context, st *store.Store, r *resolve.Deterministic) error {
	aliases := make([]store.AliasRow, 0, len(r.Aliases))
	for _, a := range r.Aliases {
		aliases = append(aliases, store.AliasRow{AliasKey: a.AliasKey, CanonicalID: a.CanonicalID, Source: a.Source})
	}
	if err := st.SaveAliases(ctx, aliases); err != nil {
		return err
	}
	conflicts := make([]store.ConflictRow, 0, len(r.Conflicts))
	for _, c := range r.Conflicts {
		conflicts = append(conflicts, store.ConflictRow{Key: c.Key, Reason: c.Reason, Detail: c.Detail})
	}
	return st.SaveConflicts(ctx, conflicts)
}
