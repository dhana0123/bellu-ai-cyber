package intelcorrelate

import (
	"context"
	"testing"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser/atlas"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser/mcp"
	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// These tests verify Phase-3 AI canonical mappings (task 21.1): an ATLAS
// technique canonicalizes to a CanonicalVuln the Matcher's matchAIModelVulns
// correlates to an AIModel/AIAgent asset, and an MCP advisory canonicalizes to
// one matchMCPVulns correlates to an MCPServer asset. They are pure/offline:
// no graph, DB, or network — the matcher runs against in-memory fakes.

// TestATLASCanonicalMatchesAIModel confirms an ATLAS technique's affected model
// identifier flows through the canonicalizer into an AI-ecosystem
// AffectedPackage that matchAIModelVulns matches.
func TestATLASCanonicalMatchesAIModel(t *testing.T) {
	const modelID = "meta-llama/Llama-3-8B"

	recs := []parser.SourceRecord{
		atlas.Technique{
			ID:             "AML.T0051",
			Name:           "LLM Prompt Injection",
			Description:    "Adversary crafts prompts to subvert the model.",
			AffectedModels: []string{modelID},
		},
	}
	vulns, err := canonical.NewATLASCanonicalizer().Canonicalize(recs)
	if err != nil {
		t.Fatalf("canonicalize atlas: %v", err)
	}
	if len(vulns) != 1 {
		t.Fatalf("expected 1 canonical vuln, got %d", len(vulns))
	}
	v := vulns[0]
	if v.ID != "AML.T0051" {
		t.Errorf("expected canonical id AML.T0051, got %q", v.ID)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("canonical vuln invalid: %v", err)
	}

	// The matcher's pure core must emit a candidate for the matching model id.
	got := matchAIModelVulns("ai:model:1", modelID, vulns)
	if len(got) != 1 {
		t.Fatalf("expected 1 AI-model candidate, got %+v", got)
	}
	if got[0].VulnID != "AML.T0051" || got[0].AssetKind != model.KindAIModel || got[0].MatchBasis != BasisAIModel {
		t.Errorf("unexpected candidate: %+v", got[0])
	}

	// A non-matching model id must not match.
	if other := matchAIModelVulns("ai:model:2", "openai/gpt-4", vulns); len(other) != 0 {
		t.Errorf("expected no candidate for unrelated model, got %+v", other)
	}
}

// TestMCPCanonicalMatchesMCPServer confirms an MCP advisory's affected server
// identifier flows through the canonicalizer into an MCP-ecosystem
// AffectedPackage that matchMCPVulns matches.
func TestMCPCanonicalMatchesMCPServer(t *testing.T) {
	const serverID = "filesystem-mcp-server"

	recs := []parser.SourceRecord{
		mcp.Advisory{
			ID:              "MCP-2025-0001",
			Summary:         "Path traversal in the filesystem MCP server.",
			AffectedServers: []string{serverID},
		},
	}
	vulns, err := canonical.NewMCPCanonicalizer().Canonicalize(recs)
	if err != nil {
		t.Fatalf("canonicalize mcp: %v", err)
	}
	if len(vulns) != 1 {
		t.Fatalf("expected 1 canonical vuln, got %d", len(vulns))
	}
	v := vulns[0]
	if v.ID != "MCP-2025-0001" {
		t.Errorf("expected canonical id MCP-2025-0001, got %q", v.ID)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("canonical vuln invalid: %v", err)
	}

	got := matchMCPVulns("mcp:1", serverID, vulns)
	if len(got) != 1 {
		t.Fatalf("expected 1 MCP candidate, got %+v", got)
	}
	if got[0].VulnID != "MCP-2025-0001" || got[0].AssetKind != model.KindMCPServer || got[0].MatchBasis != BasisMCP {
		t.Errorf("unexpected candidate: %+v", got[0])
	}

	// A different MCP server id must not match.
	if other := matchMCPVulns("mcp:2", "github-mcp-server", vulns); len(other) != 0 {
		t.Errorf("expected no candidate for unrelated server, got %+v", other)
	}
}

// TestAICanonicalEndToEndThroughMatcher drives the real Matcher over fake graph
// rows, confirming ATLAS and MCP canonical vulns correlate to AIModel and
// MCPServer graph assets end-to-end.
func TestAICanonicalEndToEndThroughMatcher(t *testing.T) {
	atlasVulns, err := canonical.NewATLASCanonicalizer().Canonicalize([]parser.SourceRecord{
		atlas.Technique{ID: "AML.T0051", Name: "Prompt Injection", AffectedModels: []string{"meta-llama/Llama-3-8B"}},
	})
	if err != nil {
		t.Fatalf("atlas canonicalize: %v", err)
	}
	mcpVulns, err := canonical.NewMCPCanonicalizer().Canonicalize([]parser.SourceRecord{
		mcp.Advisory{ID: "MCP-2025-0001", Summary: "x", AffectedServers: []string{"filesystem-mcp-server"}},
	})
	if err != nil {
		t.Fatalf("mcp canonicalize: %v", err)
	}

	intel := fakeIntel{vulns: append(append([]canonical.CanonicalVuln{}, atlasVulns...), mcpVulns...)}
	g := fakeGraph{rows: map[string][]map[string]any{
		cypherAIModels: {
			{"id": "ai:model:llama", "model_id": "meta-llama/Llama-3-8B"},
		},
		cypherMCPServers: {
			{"id": "mcp:fs", "mcp_id": "filesystem-mcp-server"},
		},
	}}

	cands, err := New(g, intel).Correlate(context.Background())
	if err != nil {
		t.Fatalf("correlate: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %+v", cands)
	}

	var sawAI, sawMCP bool
	for _, c := range cands {
		switch c.MatchBasis {
		case BasisAIModel:
			sawAI = c.VulnID == "AML.T0051" && c.AssetID == "ai:model:llama"
		case BasisMCP:
			sawMCP = c.VulnID == "MCP-2025-0001" && c.AssetID == "mcp:fs"
		}
	}
	if !sawAI || !sawMCP {
		t.Fatalf("missing expected AI/MCP candidates: %+v", cands)
	}
}
