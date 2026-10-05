package aiagent

import (
	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// Derive inspects resolved entities and edges and produces AIAgent entities and
// their edges:
//
//	AIAgent -[:BUILT_FROM]-> Repository   (the repo that showed AI signals)
//	AIAgent -[:RUNS_IN]->   Workload      (via image digest: repo BUILT_FROM
//	                                        image RUNS_IMAGE workload)
//
// A repository is an agent if any package it DEPENDS_ON/USES_MODEL matches a
// framework rule, if it owns an MCP server, or if it has a prompt. Detection is
// evidence-preserving: the AIAgent carries the repository's evidence.
//
// Derive works on canonical IDs (post-resolution) and returns new entities and
// edges to append.
func Derive(rules Rules, entities []model.Entity, edges []model.Edge) ([]model.Entity, []model.Edge) {
	// Index entities by canonical id.
	byID := make(map[string]*model.Entity, len(entities))
	for i := range entities {
		byID[entities[i].CanonicalID] = &entities[i]
	}

	// Build adjacency for the signals we need.
	dependsOrModel := map[string][]string{} // repo/image/pkg -> deps
	builtFromImage := map[string]string{}    // image id -> repo id
	imageRunsIn := map[string][]string{}     // image id -> workloads
	containsOrLinks := map[string][]string{} // src -> dst for CONTAINS/CALLS/USES_MODEL

	for _, e := range edges {
		switch e.Relation {
		case model.RelDependsOn, model.RelUsesModel:
			dependsOrModel[e.SrcCanonicalID] = append(dependsOrModel[e.SrcCanonicalID], e.DstCanonicalID)
			containsOrLinks[e.SrcCanonicalID] = append(containsOrLinks[e.SrcCanonicalID], e.DstCanonicalID)
		case model.RelBuiltFrom:
			// image BUILT_FROM repo
			builtFromImage[e.SrcCanonicalID] = e.DstCanonicalID
		case model.RelRunsImage:
			imageRunsIn[e.DstCanonicalID] = append(imageRunsIn[e.DstCanonicalID], e.SrcCanonicalID)
		case model.RelContains, model.RelCalls:
			containsOrLinks[e.SrcCanonicalID] = append(containsOrLinks[e.SrcCanonicalID], e.DstCanonicalID)
		}
	}

	var newEntities []model.Entity
	var newEdges []model.Edge

	for i := range entities {
		repo := &entities[i]
		if repo.Kind != model.KindRepository {
			continue
		}
		if !repoIsAgent(rules, repo, byID, dependsOrModel) {
			continue
		}

		agentID := "aiagent:" + repo.CanonicalID
		agent := model.Entity{
			Kind:        model.KindAIAgent,
			CanonicalID: agentID,
			Key:         agentID,
			Provider:    repo.Provider,
			Name:        repo.Name,
			Attributes:  map[string]any{"derived_from": repo.CanonicalID},
			Evidence:    repo.Evidence,
		}
		newEntities = append(newEntities, agent)

		// AIAgent BUILT_FROM Repository.
		newEdges = append(newEdges, model.Edge{
			Relation:       model.RelBuiltFrom,
			SrcCanonicalID: agentID,
			DstCanonicalID: repo.CanonicalID,
			SrcKey:         agentID,
			DstKey:         repo.CanonicalID,
			Attributes:     map[string]any{"derived": true},
			Confidence:     1.0,
			Evidence:       repo.Evidence,
		})

		// AIAgent RUNS_IN Workload, following repo <-BUILT_FROM- image
		// -RUNS_IMAGE-> workload.
		for imageID, repoID := range builtFromImage {
			if repoID != repo.CanonicalID {
				continue
			}
			for _, workloadID := range imageRunsIn[imageID] {
				newEdges = append(newEdges, model.Edge{
					Relation:       model.RelRunsIn,
					SrcCanonicalID: agentID,
					DstCanonicalID: workloadID,
					SrcKey:         agentID,
					DstKey:         workloadID,
					Attributes:     map[string]any{"via": "image_digest", "image": imageID},
					Confidence:     0.9,
					Evidence:       repo.Evidence,
				})
			}
		}
	}

	return newEntities, newEdges
}

// repoIsAgent applies the detection rules to a repository.
func repoIsAgent(rules Rules, repo *model.Entity, byID map[string]*model.Entity, deps map[string][]string) bool {
	// 1) Dependency on an agent framework (package or model).
	for _, depID := range deps[repo.CanonicalID] {
		dep := byID[depID]
		if dep == nil {
			continue
		}
		if rules.MatchPurl(dep.Key) || rules.MatchName(dep.Name) {
			return true
		}
		// Models also indicate an agent.
		if dep.Kind == model.KindAIModel {
			return true
		}
	}
	// 2) Owns an MCP server or has a prompt linked.
	if rules.RequireMCP {
		for _, linkedID := range deps[repo.CanonicalID] {
			if e := byID[linkedID]; e != nil && (e.Kind == model.KindMCPServer || e.Kind == model.KindPrompt) {
				return true
			}
		}
	}
	return false
}
