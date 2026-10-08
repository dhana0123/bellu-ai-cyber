// Package mcp parses Model Context Protocol (MCP) security advisories into
// typed source records.
//
// MCP security intelligence is advisory-style: each advisory names an affected
// MCP server (by a stable server identifier) and carries a stable advisory id
// of the form "MCP-YYYY-NNNN" plus any cross-referenced CVE/GHSA aliases. To
// keep the parser pure and the fetcher resilient, this package decodes a clean
// JSON feed: a top-level "advisories" array, each with an advisory id, a
// summary, the affected MCP server identifier(s), and optional references.
//
// Parse is a pure function of its input (Requirement 3.2): identical bytes
// always decode to identical records in a deterministic order.
package mcp

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bellu/bellu-ai-cyber/global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

// Source is the stable source key this parser handles.
const Source = "mcp"

// Advisory is one decoded MCP security advisory. Field tags mirror the JSON
// feed; unknown keys are ignored. Advisory satisfies parser.SourceRecord.
type Advisory struct {
	// ID is the stable MCP advisory id, e.g. "MCP-2025-0001". It is both the
	// SourceKey and the primary alias.
	ID string `json:"id"`
	// Summary is the human-readable advisory summary.
	Summary string `json:"summary"`
	// AffectedServers is the set of MCP server identifiers this advisory
	// applies to. These drive the canonical mapping to MCPServer nodes.
	AffectedServers []string `json:"affected_servers"`
	// References are external identifiers (CVE/GHSA/other) cross-referenced by
	// the advisory; retained as additional aliases.
	References []string `json:"references"`
}

// Aliases returns every external identifier this advisory carries: its MCP id
// first, then any references, in document order.
func (a Advisory) Aliases() []string {
	out := make([]string, 0, 1+len(a.References))
	if a.ID != "" {
		out = append(out, a.ID)
	}
	out = append(out, a.References...)
	return out
}

// SourceKey returns the stable per-source id of this advisory: its MCP id.
func (a Advisory) SourceKey() string { return a.ID }

// compile-time assertion that Advisory satisfies parser.SourceRecord.
var _ parser.SourceRecord = Advisory{}

// document is the top-level shape of the MCP advisory feed.
type document struct {
	Advisories []Advisory `json:"advisories"`
}

// Parser decodes MCP advisory feed payloads into Advisory source records.
type Parser struct{}

// New returns an MCP Parser.
func New() Parser { return Parser{} }

// Source returns the stable source key "mcp".
func (Parser) Source() string { return Source }

// Parse decodes an MCP advisory JSON feed into one source record per advisory,
// stably sorted by id for determinism. Parse is a pure function of its input.
func (Parser) Parse(_ raw.RawRef, payload []byte) ([]parser.SourceRecord, error) {
	var doc document
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("mcp: decode document: %w", err)
	}

	advisories := make([]Advisory, 0, len(doc.Advisories))
	advisories = append(advisories, doc.Advisories...)
	sort.SliceStable(advisories, func(i, j int) bool {
		return advisories[i].ID < advisories[j].ID
	})

	recs := make([]parser.SourceRecord, 0, len(advisories))
	for _, a := range advisories {
		if a.ID == "" {
			return nil, fmt.Errorf("mcp: advisory missing required id field")
		}
		recs = append(recs, a)
	}
	return recs, nil
}

// compile-time assertion that Parser satisfies parser.Parser.
var _ parser.Parser = Parser{}
