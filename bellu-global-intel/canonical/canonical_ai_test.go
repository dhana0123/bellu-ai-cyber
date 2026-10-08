package canonical

import (
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/atlas"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/avid"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/mcp"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/owasp"
)

// TestATLASCanonicalizeMapping confirms an ATLAS technique maps to a valid
// CanonicalVuln whose affected package carries the model id under an AI-model
// ecosystem marker the Matcher recognizes, with the ATLAS id as the canonical
// id and a member of the aliases (so Validate passes).
func TestATLASCanonicalizeMapping(t *testing.T) {
	out, err := NewATLASCanonicalizer().Canonicalize([]parser.SourceRecord{
		atlas.Technique{
			ID:             "AML.T0051",
			Name:           "LLM Prompt Injection",
			Description:    "subvert the model",
			AffectedModels: []string{"meta-llama/Llama-3-8B", "meta-llama/Llama-3-8B"}, // dup collapsed
			References:     []string{"CVE-2024-9999"},
		},
	})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 vuln, got %d", len(out))
	}
	v := out[0]
	if v.ID != "AML.T0051" {
		t.Errorf("expected ATLAS id as canonical id, got %q", v.ID)
	}
	// References become additional aliases, normalized.
	if !containsAlias(v.Aliases, "CVE-2024-9999") {
		t.Errorf("expected CVE reference carried as alias, got %v", v.Aliases)
	}
	if len(v.Affected) != 1 {
		t.Fatalf("expected 1 affected (dup collapsed), got %+v", v.Affected)
	}
	ap := v.Affected[0]
	if ap.Ecosystem != EcosystemAIModel || ap.Purl != "meta-llama/Llama-3-8B" {
		t.Errorf("expected ai-model ecosystem with model id purl, got %+v", ap)
	}
	if len(ap.Ranges) != 0 {
		t.Errorf("AI correlation uses identity equality, expected no ranges, got %+v", ap.Ranges)
	}
}

// TestOWASPAndAVIDCanonicalizeMapping confirms OWASP GenAI and AVID map to
// AI-model affected packages with the source id as canonical id.
func TestOWASPAndAVIDCanonicalizeMapping(t *testing.T) {
	owaspOut, err := NewOWASPGenAICanonicalizer().Canonicalize([]parser.SourceRecord{
		owasp.Entry{ID: "LLM01", Title: "Prompt Injection", AffectedModels: []string{"openai/gpt-4"}},
	})
	if err != nil {
		t.Fatalf("owasp canonicalize: %v", err)
	}
	if owaspOut[0].ID != "LLM01" || owaspOut[0].Affected[0].Ecosystem != EcosystemAIModel {
		t.Errorf("unexpected owasp mapping: %+v", owaspOut[0])
	}

	avidOut, err := NewAVIDCanonicalizer().Canonicalize([]parser.SourceRecord{
		avid.Entry{ID: "AVID-2023-V001", Title: "Data poisoning", AffectedModels: []string{"some/model"}},
	})
	if err != nil {
		t.Fatalf("avid canonicalize: %v", err)
	}
	if avidOut[0].ID != "AVID-2023-V001" || avidOut[0].Affected[0].Purl != "some/model" {
		t.Errorf("unexpected avid mapping: %+v", avidOut[0])
	}
}

// TestMCPCanonicalizeMapping confirms an MCP advisory maps to an MCP-ecosystem
// affected package the Matcher's MCP path recognizes.
func TestMCPCanonicalizeMapping(t *testing.T) {
	out, err := NewMCPCanonicalizer().Canonicalize([]parser.SourceRecord{
		mcp.Advisory{ID: "MCP-2025-0001", Summary: "path traversal", AffectedServers: []string{"filesystem"}},
	})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	v := out[0]
	if v.ID != "MCP-2025-0001" {
		t.Errorf("expected MCP id as canonical id, got %q", v.ID)
	}
	if v.Affected[0].Ecosystem != EcosystemMCP || v.Affected[0].Purl != "filesystem" {
		t.Errorf("expected mcp ecosystem with server id purl, got %+v", v.Affected[0])
	}
}

// TestAICanonicalizeDeterministic confirms the same records always produce the
// same canonical output (correctness property: determinism).
func TestAICanonicalizeDeterministic(t *testing.T) {
	recs := []parser.SourceRecord{
		atlas.Technique{ID: "AML.T0051", AffectedModels: []string{"a/b", "c/d"}},
		atlas.Technique{ID: "AML.T0043", AffectedModels: []string{"e/f"}},
	}
	first, err := NewATLASCanonicalizer().Canonicalize(recs)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NewATLASCanonicalizer().Canonicalize(recs)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("canonicalize not deterministic:\n%+v\n%+v", first, second)
	}
}

// TestAICanonicalizeRejectsNoAliases confirms a record with no id/aliases is
// rejected (Requirement 4.5 via Validate).
func TestAICanonicalizeRejectsNoAliases(t *testing.T) {
	// An empty-id technique never reaches the canonicalizer in practice (the
	// parser rejects it), but the canonicalizer must still reject it defensively.
	_, err := NewATLASCanonicalizer().Canonicalize([]parser.SourceRecord{atlas.Technique{}})
	if err == nil {
		t.Fatal("expected rejection for record with no aliases")
	}
}

func containsAlias(aliases []string, want string) bool {
	want = NormalizeAlias(want)
	for _, a := range aliases {
		if a == want {
			return true
		}
	}
	return false
}
