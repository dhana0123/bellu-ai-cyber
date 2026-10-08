package main

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// TestAICriticality pins the asset-kind -> AI-criticality mapping used to feed
// the risk engine. Non-AI assets must contribute no AI boost (0), AI kinds must
// be ordered agent > model > mcp, and every value must stay in [0,1] so it is a
// valid risk.RiskContext.AICriticality.
func TestAICriticality(t *testing.T) {
	cases := []struct {
		kind model.Kind
		want float64
	}{
		{model.KindAIAgent, 1.0},
		{model.KindAIModel, 0.75},
		{model.KindMCPServer, 0.5},
		{model.KindPackage, 0},
		{model.KindContainerImage, 0},
		{model.KindDataStore, 0},
		{model.Kind("Unknown"), 0},
	}
	for _, c := range cases {
		if got := aiCriticality(c.kind); got != c.want {
			t.Errorf("aiCriticality(%q) = %v, want %v", c.kind, got, c.want)
		}
	}
}

// TestAICriticalityBoundsAndOrdering asserts the invariants the risk engine
// relies on: every value is in [0,1] and the AI kinds are strictly ordered by
// blast radius (agent > model > mcp > non-AI).
func TestAICriticalityBoundsAndOrdering(t *testing.T) {
	for _, k := range model.AllKinds {
		v := aiCriticality(k)
		if v < 0 || v > 1 {
			t.Errorf("aiCriticality(%q) = %v, out of [0,1]", k, v)
		}
	}

	agent := aiCriticality(model.KindAIAgent)
	aiModel := aiCriticality(model.KindAIModel)
	mcp := aiCriticality(model.KindMCPServer)
	nonAI := aiCriticality(model.KindPackage)

	if !(agent > aiModel && aiModel > mcp && mcp > nonAI) {
		t.Errorf("AI criticality not strictly ordered agent>model>mcp>non-AI: %v,%v,%v,%v",
			agent, aiModel, mcp, nonAI)
	}
}
