package aiagent

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

func ent(kind model.Kind, id, key, name string) model.Entity {
	return model.Entity{Kind: kind, CanonicalID: id, Key: key, Name: name,
		Attributes: map[string]any{}, Evidence: []model.Evidence{{RawEventID: "e-" + id}}}
}

func TestDeriveAgentFromFrameworkDep(t *testing.T) {
	ents := []model.Entity{
		ent(model.KindRepository, "repository:repo:https://github.com/bellu/agent-api", "https://github.com/bellu/agent-api", "agent-api"),
		ent(model.KindPackage, "package:pkg:pypi/langchain@0.2.1", "pkg:pypi/langchain@0.2.1", "langchain"),
	}
	edges := []model.Edge{
		{Relation: model.RelDependsOn, SrcCanonicalID: ents[0].CanonicalID, DstCanonicalID: ents[1].CanonicalID},
	}
	newE, newEdges := Derive(DefaultRules(), ents, edges)
	if len(newE) != 1 || newE[0].Kind != model.KindAIAgent {
		t.Fatalf("expected 1 AIAgent, got %+v", newE)
	}
	// AIAgent BUILT_FROM Repository.
	var builtFrom bool
	for _, e := range newEdges {
		if e.Relation == model.RelBuiltFrom && e.DstCanonicalID == ents[0].CanonicalID {
			builtFrom = true
		}
	}
	if !builtFrom {
		t.Fatalf("expected AIAgent BUILT_FROM repo; edges=%+v", newEdges)
	}
}

func TestDeriveAgentRunsInWorkload(t *testing.T) {
	repo := ent(model.KindRepository, "repository:repo:r", "r", "agent-api")
	pkg := ent(model.KindPackage, "package:pkg:pypi/openai@1", "pkg:pypi/openai@1", "openai")
	img := ent(model.KindContainerImage, "image:sha256:abc", "sha256:abc", "img")
	wl := ent(model.KindWorkload, "workload:k8s:pod1", "k8s:pod1", "pod1")
	ents := []model.Entity{repo, pkg, img, wl}
	edges := []model.Edge{
		{Relation: model.RelDependsOn, SrcCanonicalID: repo.CanonicalID, DstCanonicalID: pkg.CanonicalID},
		{Relation: model.RelBuiltFrom, SrcCanonicalID: img.CanonicalID, DstCanonicalID: repo.CanonicalID},
		{Relation: model.RelRunsImage, SrcCanonicalID: wl.CanonicalID, DstCanonicalID: img.CanonicalID},
	}
	_, newEdges := Derive(DefaultRules(), ents, edges)
	var runsIn bool
	for _, e := range newEdges {
		if e.Relation == model.RelRunsIn && e.DstCanonicalID == wl.CanonicalID {
			runsIn = true
		}
	}
	if !runsIn {
		t.Fatalf("expected AIAgent RUNS_IN workload via image digest; edges=%+v", newEdges)
	}
}

func TestDeriveAgentFromMCP(t *testing.T) {
	repo := ent(model.KindRepository, "repository:repo:r", "r", "agent-api")
	mcp := ent(model.KindMCPServer, "mcpserver:repo:mcp", "service:mcp", "filesystem")
	ents := []model.Entity{repo, mcp}
	edges := []model.Edge{
		// repo links to MCP server (treated as a dependency-like link).
		{Relation: model.RelDependsOn, SrcCanonicalID: repo.CanonicalID, DstCanonicalID: mcp.CanonicalID},
	}
	newE, _ := Derive(DefaultRules(), ents, edges)
	if len(newE) != 1 {
		t.Fatalf("expected agent from MCP ownership, got %d", len(newE))
	}
}

func TestNonAgentRepoNotDerived(t *testing.T) {
	repo := ent(model.KindRepository, "repository:repo:r", "r", "plain-app")
	pkg := ent(model.KindPackage, "package:pkg:pypi/requests@2", "pkg:pypi/requests@2", "requests")
	ents := []model.Entity{repo, pkg}
	edges := []model.Edge{
		{Relation: model.RelDependsOn, SrcCanonicalID: repo.CanonicalID, DstCanonicalID: pkg.CanonicalID},
	}
	newE, _ := Derive(DefaultRules(), ents, edges)
	if len(newE) != 0 {
		t.Fatalf("expected no agent for non-AI repo, got %d", len(newE))
	}
}

func TestLoadRulesDefaultsWhenAbsent(t *testing.T) {
	r, err := LoadRules("/nonexistent/rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !r.MatchPurl("pkg:pypi/langchain@0.2.1") {
		t.Fatal("default rules should match langchain")
	}
}
