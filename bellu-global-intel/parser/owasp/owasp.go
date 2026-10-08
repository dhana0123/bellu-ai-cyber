// Package owasp parses OWASP GenAI / LLM Top 10 risk entries into typed source
// records.
//
// The OWASP GenAI project publishes its Top 10 for LLM Applications as a
// release/document artifact rather than a strict machine schema. To keep the
// fetcher resilient and the parser pure, this package decodes a clean JSON feed
// whose shape mirrors the published entries: a top-level "entries" (or
// "risks") array, each element carrying a stable id of the form "LLM01".."LLM10"
// (optionally suffixed, e.g. "LLM01:2025"), a title, a description, and an
// optional list of affected AI model / agent identifiers.
//
// Parse is a pure function of its input (Requirement 3.2): identical bytes
// always decode to identical records in a deterministic order. It performs no
// I/O, reads no clock, and uses no randomness.
package owasp

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/raw"
)

// Source is the stable source key this parser handles.
const Source = "owasp-genai"

// Entry is one decoded OWASP GenAI / LLM Top 10 risk. Field tags mirror the
// JSON feed; unknown keys are ignored. Entry satisfies parser.SourceRecord.
type Entry struct {
	// ID is the stable OWASP risk id, e.g. "LLM01" or "LLM01:2025". It is both
	// the SourceKey and the primary alias.
	ID string `json:"id"`
	// Title is the human-readable risk title, e.g. "Prompt Injection".
	Title string `json:"title"`
	// Description is the long-form risk description.
	Description string `json:"description"`
	// AffectedModels is the set of AI model / agent identifiers this risk is
	// scoped to. These drive the canonical mapping to AIModel / AIAgent nodes.
	AffectedModels []string `json:"affected_models"`
	// References are external identifiers associated with the risk; retained as
	// additional aliases.
	References []string `json:"references"`
}

// Aliases returns every external identifier this entry carries: its OWASP id
// first, then any references, in document order. No normalization or
// de-duplication is performed here.
func (e Entry) Aliases() []string {
	out := make([]string, 0, 1+len(e.References))
	if e.ID != "" {
		out = append(out, e.ID)
	}
	out = append(out, e.References...)
	return out
}

// SourceKey returns the stable per-source id of this entry: its OWASP id.
func (e Entry) SourceKey() string { return e.ID }

// compile-time assertion that Entry satisfies parser.SourceRecord.
var _ parser.SourceRecord = Entry{}

// document is the top-level shape of the OWASP GenAI feed. Entries may appear
// under "entries" or the equivalent "risks" key; both are accepted.
type document struct {
	Entries []Entry `json:"entries"`
	Risks   []Entry `json:"risks"`
}

// Parser decodes OWASP GenAI feed payloads into Entry source records.
type Parser struct{}

// New returns an OWASP GenAI Parser.
func New() Parser { return Parser{} }

// Source returns the stable source key "owasp-genai".
func (Parser) Source() string { return Source }

// Parse decodes an OWASP GenAI JSON feed into one source record per risk entry.
// Entries under "entries" and "risks" are concatenated then stably sorted by id
// so identical sets in any order decode identically. Parse is pure.
func (Parser) Parse(_ raw.RawRef, payload []byte) ([]parser.SourceRecord, error) {
	var doc document
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("owasp: decode document: %w", err)
	}

	entries := make([]Entry, 0, len(doc.Entries)+len(doc.Risks))
	entries = append(entries, doc.Entries...)
	entries = append(entries, doc.Risks...)

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	recs := make([]parser.SourceRecord, 0, len(entries))
	for _, e := range entries {
		if e.ID == "" {
			return nil, fmt.Errorf("owasp: entry missing required id field")
		}
		recs = append(recs, e)
	}
	return recs, nil
}

// compile-time assertion that Parser satisfies parser.Parser.
var _ parser.Parser = Parser{}
