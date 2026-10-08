package canonical

import (
	"fmt"
	"strings"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/atlas"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/avid"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/mcp"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/owasp"
)

// This file holds the Phase-3 AI-specific canonicalizers that map MITRE ATLAS
// techniques, OWASP GenAI / LLM Top 10 risks, AVID entries, and MCP advisories
// into CanonicalVuln (Requirement 15.1, 15.2). AI intelligence reuses the exact
// same CanonicalVuln shape the Phase-1 sources use; the only AI-specific twist
// is how it correlates to graph assets.
//
// Correlation mechanism (so these records join to AIAgent / AIModel / MCPServer
// via the existing Intelligence Matcher, Requirement 8.3): the Matcher's
// matchAIModelVulns scans AffectedPackage entries whose Ecosystem is an AI
// marker (isAIModelEcosystem recognizes "ai"/"aimodel"/"ai-model"/
// "huggingface"/"model") and compares AffectedPackage.Purl against the asset's
// model identifier with correlation.MatchAIModel (case-insensitive equality).
// matchMCPVulns does the same for Ecosystem "mcp"/"mcpserver"/"mcp-server" via
// correlation.MatchMCPServer. So each affected AI model identifier is emitted
// as an AffectedPackage{Ecosystem: EcosystemAIModel, Purl: <modelID>} and each
// affected MCP server identifier as {Ecosystem: EcosystemMCP, Purl: <mcpID>}.
// No semver ranges are attached: AI correlation is identity equality, not
// version containment.
//
// Canonicalization is a pure function of its inputs: the same records always
// produce the same CanonicalVulns in the same input-preserving order, with no
// clock or map-iteration-order dependence. Every produced record is validated
// (CanonicalVuln.Validate), so the stable canonical id (the ATLAS/OWASP/AVID/MCP
// id) is always present among the normalized aliases.

// Ecosystem markers the Intelligence Matcher recognizes for AI correlation.
// They mirror internal/intelcorrelate.isAIModelEcosystem / isMCPEcosystem.
const (
	// EcosystemAIModel marks an AffectedPackage whose Purl is an AI model /
	// agent identifier, so the Matcher joins it to AIModel / AIAgent nodes.
	EcosystemAIModel = "ai-model"
	// EcosystemMCP marks an AffectedPackage whose Purl is an MCP server
	// identifier, so the Matcher joins it to MCPServer nodes.
	EcosystemMCP = "mcp"
)

// aiModelAffected builds one AffectedPackage per non-empty model identifier,
// each marked with EcosystemAIModel and carrying the identifier verbatim in
// Purl (trimmed). Empty/whitespace identifiers and duplicates are dropped so
// the output is a deterministic function of the input. No ranges are attached:
// AI-model correlation is identity equality, not version containment.
func aiModelAffected(modelIDs []string) []AffectedPackage {
	return aiAffected(modelIDs, EcosystemAIModel)
}

// mcpAffected builds one AffectedPackage per non-empty MCP server identifier,
// each marked with EcosystemMCP, with the same determinism/dedup rules.
func mcpAffected(serverIDs []string) []AffectedPackage {
	return aiAffected(serverIDs, EcosystemMCP)
}

// aiAffected is the shared builder for identity-matched AI affected packages.
func aiAffected(ids []string, ecosystem string) []AffectedPackage {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]AffectedPackage, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, AffectedPackage{Purl: id, Ecosystem: ecosystem})
	}
	return out
}

// aiPreferredID selects the canonical id for an AI record: the normalized
// primary source id when present among the aliases, else the first alias. The
// primary id (ATLAS/OWASP/AVID/MCP id) is always the first alias produced by
// each parser record, so this keeps the source id as the canonical id while
// guaranteeing the id is a member of the alias set (so Validate passes).
func aiPreferredID(aliases []string, sourceID string) string {
	n := NormalizeAlias(sourceID)
	for _, a := range aliases {
		if a == n {
			return n
		}
	}
	if len(aliases) > 0 {
		return aliases[0]
	}
	return n
}

// --- MITRE ATLAS -------------------------------------------------------------

// ATLASCanonicalizer maps MITRE ATLAS technique records (atlas.Technique) into
// CanonicalVuln. A technique becomes an AITechnique-style threat affecting the
// AI model / agent identifiers it lists; those identifiers are emitted as
// EcosystemAIModel affected packages so the Matcher joins them to AIModel /
// AIAgent nodes. The canonical id is the ATLAS technique id (AML.Txxxx).
type ATLASCanonicalizer struct{}

// NewATLASCanonicalizer returns an ATLAS canonicalizer.
func NewATLASCanonicalizer() ATLASCanonicalizer { return ATLASCanonicalizer{} }

// Source returns the stable source key "atlas".
func (ATLASCanonicalizer) Source() string { return "atlas" }

// Canonicalize maps each atlas.Technique into a validated CanonicalVuln.
func (ATLASCanonicalizer) Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error) {
	out := make([]CanonicalVuln, 0, len(recs))
	for i, sr := range recs {
		t, ok := sr.(atlas.Technique)
		if !ok {
			return nil, fmt.Errorf("canonical: atlas canonicalizer got %T, want atlas.Technique (record %d)", sr, i)
		}
		aliases := DedupeAliases(t.Aliases())
		if len(aliases) == 0 {
			return nil, fmt.Errorf("canonical: atlas record %d has no aliases", i)
		}
		v := CanonicalVuln{
			ID:       aiPreferredID(aliases, t.ID),
			Aliases:  aliases,
			Summary:  aiSummary(t.Name, t.Description),
			Affected: aiModelAffected(t.AffectedModels),
			Severity: SevInfo,
			Sources:  []string{"atlas"},
		}
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: atlas record %q invalid: %w", t.ID, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// --- OWASP GenAI -------------------------------------------------------------

// OWASPGenAICanonicalizer maps OWASP GenAI / LLM Top 10 risk records
// (owasp.Entry) into CanonicalVuln. Each risk affects the AI model / agent
// identifiers it lists, emitted as EcosystemAIModel affected packages. The
// canonical id is the OWASP risk id (e.g. LLM01).
type OWASPGenAICanonicalizer struct{}

// NewOWASPGenAICanonicalizer returns an OWASP GenAI canonicalizer.
func NewOWASPGenAICanonicalizer() OWASPGenAICanonicalizer { return OWASPGenAICanonicalizer{} }

// Source returns the stable source key "owasp-genai".
func (OWASPGenAICanonicalizer) Source() string { return "owasp-genai" }

// Canonicalize maps each owasp.Entry into a validated CanonicalVuln.
func (OWASPGenAICanonicalizer) Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error) {
	out := make([]CanonicalVuln, 0, len(recs))
	for i, sr := range recs {
		e, ok := sr.(owasp.Entry)
		if !ok {
			return nil, fmt.Errorf("canonical: owasp canonicalizer got %T, want owasp.Entry (record %d)", sr, i)
		}
		aliases := DedupeAliases(e.Aliases())
		if len(aliases) == 0 {
			return nil, fmt.Errorf("canonical: owasp record %d has no aliases", i)
		}
		v := CanonicalVuln{
			ID:       aiPreferredID(aliases, e.ID),
			Aliases:  aliases,
			Summary:  aiSummary(e.Title, e.Description),
			Affected: aiModelAffected(e.AffectedModels),
			Severity: SevInfo,
			Sources:  []string{"owasp-genai"},
		}
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: owasp record %q invalid: %w", e.ID, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// --- AVID --------------------------------------------------------------------

// AVIDCanonicalizer maps AVID records (avid.Entry) into CanonicalVuln. Each
// entry affects the AI model / agent identifiers it lists, emitted as
// EcosystemAIModel affected packages. The canonical id is the AVID id.
type AVIDCanonicalizer struct{}

// NewAVIDCanonicalizer returns an AVID canonicalizer.
func NewAVIDCanonicalizer() AVIDCanonicalizer { return AVIDCanonicalizer{} }

// Source returns the stable source key "avid".
func (AVIDCanonicalizer) Source() string { return "avid" }

// Canonicalize maps each avid.Entry into a validated CanonicalVuln.
func (AVIDCanonicalizer) Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error) {
	out := make([]CanonicalVuln, 0, len(recs))
	for i, sr := range recs {
		e, ok := sr.(avid.Entry)
		if !ok {
			return nil, fmt.Errorf("canonical: avid canonicalizer got %T, want avid.Entry (record %d)", sr, i)
		}
		aliases := DedupeAliases(e.Aliases())
		if len(aliases) == 0 {
			return nil, fmt.Errorf("canonical: avid record %d has no aliases", i)
		}
		v := CanonicalVuln{
			ID:       aiPreferredID(aliases, e.ID),
			Aliases:  aliases,
			Summary:  aiSummary(e.Title, e.Description),
			Affected: aiModelAffected(e.AffectedModels),
			Severity: SevInfo,
			Sources:  []string{"avid"},
		}
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: avid record %q invalid: %w", e.ID, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// --- MCP ---------------------------------------------------------------------

// MCPCanonicalizer maps MCP security advisories (mcp.Advisory) into
// CanonicalVuln. Each advisory affects the MCP server identifiers it names,
// emitted as EcosystemMCP affected packages so the Matcher joins them to
// MCPServer nodes. The canonical id is the MCP advisory id.
type MCPCanonicalizer struct{}

// NewMCPCanonicalizer returns an MCP canonicalizer.
func NewMCPCanonicalizer() MCPCanonicalizer { return MCPCanonicalizer{} }

// Source returns the stable source key "mcp".
func (MCPCanonicalizer) Source() string { return "mcp" }

// Canonicalize maps each mcp.Advisory into a validated CanonicalVuln.
func (MCPCanonicalizer) Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error) {
	out := make([]CanonicalVuln, 0, len(recs))
	for i, sr := range recs {
		a, ok := sr.(mcp.Advisory)
		if !ok {
			return nil, fmt.Errorf("canonical: mcp canonicalizer got %T, want mcp.Advisory (record %d)", sr, i)
		}
		aliases := DedupeAliases(a.Aliases())
		if len(aliases) == 0 {
			return nil, fmt.Errorf("canonical: mcp record %d has no aliases", i)
		}
		v := CanonicalVuln{
			ID:       aiPreferredID(aliases, a.ID),
			Aliases:  aliases,
			Summary:  strings.TrimSpace(a.Summary),
			Affected: mcpAffected(a.AffectedServers),
			Severity: SevInfo,
			Sources:  []string{"mcp"},
		}
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: mcp record %q invalid: %w", a.ID, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// aiSummary joins a short title and a long description into a single summary,
// preferring the title and appending the description when both are present.
func aiSummary(title, description string) string {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	switch {
	case title != "" && description != "":
		return title + ": " + description
	case title != "":
		return title
	default:
		return description
	}
}

// Compile-time assertions that each AI canonicalizer satisfies Canonicalizer.
var (
	_ Canonicalizer = ATLASCanonicalizer{}
	_ Canonicalizer = OWASPGenAICanonicalizer{}
	_ Canonicalizer = AVIDCanonicalizer{}
	_ Canonicalizer = MCPCanonicalizer{}
)
