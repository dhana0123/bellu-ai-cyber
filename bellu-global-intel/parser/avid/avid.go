// Package avid parses AVID (AI Vulnerability Database) taxonomy / database
// entries into typed source records.
//
// AVID publishes a taxonomy and a database of AI vulnerabilities and incidents.
// Entries are keyed by an AVID id of the form "AVID-YYYY-Vxxx" (vulnerabilities)
// or "AVID-YYYY-Rxxx" (reports). To keep the parser pure and the fetcher
// resilient, this package decodes a clean JSON feed whose shape mirrors the
// AVID records: a top-level "entries" array, each with a stable id, a title, a
// description, and an optional list of affected AI model / agent identifiers.
//
// Parse is a pure function of its input (Requirement 3.2): identical bytes
// always decode to identical records in a deterministic order.
package avid

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/raw"
)

// Source is the stable source key this parser handles.
const Source = "avid"

// Entry is one decoded AVID record. Field tags mirror the JSON feed; unknown
// keys are ignored. Entry satisfies parser.SourceRecord.
type Entry struct {
	// ID is the stable AVID id, e.g. "AVID-2023-V001". It is both the
	// SourceKey and the primary alias.
	ID string `json:"id"`
	// Title is the human-readable entry title.
	Title string `json:"title"`
	// Description is the long-form entry description.
	Description string `json:"description"`
	// AffectedModels is the set of AI model / agent identifiers this entry is
	// scoped to. These drive the canonical mapping to AIModel / AIAgent nodes.
	AffectedModels []string `json:"affected_models"`
	// References are external identifiers (CVE/other) associated with the
	// entry; retained as additional aliases.
	References []string `json:"references"`
}

// Aliases returns every external identifier this entry carries: its AVID id
// first, then any references, in document order.
func (e Entry) Aliases() []string {
	out := make([]string, 0, 1+len(e.References))
	if e.ID != "" {
		out = append(out, e.ID)
	}
	out = append(out, e.References...)
	return out
}

// SourceKey returns the stable per-source id of this entry: its AVID id.
func (e Entry) SourceKey() string { return e.ID }

// compile-time assertion that Entry satisfies parser.SourceRecord.
var _ parser.SourceRecord = Entry{}

// document is the top-level shape of the AVID feed.
type document struct {
	Entries []Entry `json:"entries"`
}

// Parser decodes AVID feed payloads into Entry source records.
type Parser struct{}

// New returns an AVID Parser.
func New() Parser { return Parser{} }

// Source returns the stable source key "avid".
func (Parser) Source() string { return Source }

// Parse decodes an AVID JSON feed into one source record per entry, stably
// sorted by id for determinism. Parse is a pure function of its input.
func (Parser) Parse(_ raw.RawRef, payload []byte) ([]parser.SourceRecord, error) {
	var doc document
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("avid: decode document: %w", err)
	}

	entries := make([]Entry, 0, len(doc.Entries))
	entries = append(entries, doc.Entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	recs := make([]parser.SourceRecord, 0, len(entries))
	for _, e := range entries {
		if e.ID == "" {
			return nil, fmt.Errorf("avid: entry missing required id field")
		}
		recs = append(recs, e)
	}
	return recs, nil
}

// compile-time assertion that Parser satisfies parser.Parser.
var _ parser.Parser = Parser{}
