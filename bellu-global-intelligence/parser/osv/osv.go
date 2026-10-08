// Package osv decodes raw OSV (Open Source Vulnerabilities) payloads into
// typed source records that satisfy parser.SourceRecord.
//
// The OSV schema (https://ossf.github.io/osv-schema/) describes one
// vulnerability per JSON document: a stable `id`, an `aliases` array of other
// identifiers (CVE/GHSA/PYSEC/...), human-readable `summary`/`details`, an
// `affected` array of packages (purl/ecosystem/name plus version `ranges` with
// `introduced`/`fixed` events), `severity` entries (CVSS vectors),
// `database_specific` free-form metadata, and `published`/`modified`
// timestamps.
//
// Parsing is a pure function of the input bytes (Requirement 3.2, correctness
// property 3): the same payload always decodes to identical records. Decoding
// uses encoding/json only, performs no I/O, reads no clock, and never iterates
// a map into a slice without a stable order, so output is deterministic. This
// package parses only — canonicalization lives in the canonical package.
package osv

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// source is the stable source key this parser handles.
const source = "osv"

// Record is a single decoded OSV entry capturing the schema fields Bellu
// needs. Field tags mirror the OSV schema key names so encoding/json maps them
// directly. Unknown keys are ignored. Record satisfies parser.SourceRecord.
type Record struct {
	// ID is the stable OSV identifier for this entry, e.g. "PYSEC-2024-1" or
	// "GHSA-xxxx-yyyy-zzzz". It is the SourceKey.
	ID string `json:"id"`
	// Aliases are the other identifiers this entry carries (CVE/GHSA/...).
	Aliases_ []string `json:"aliases"`
	// Related are loosely-associated identifiers (not merged as aliases).
	Related []string `json:"related"`
	// Summary is a one-line human-readable title.
	Summary string `json:"summary"`
	// Details is the long-form human-readable description (markdown).
	Details string `json:"details"`
	// Affected lists the packages and version ranges this entry affects.
	Affected []Affected `json:"affected"`
	// Severity holds machine-readable severity scores (e.g. CVSS vectors).
	Severity []Severity `json:"severity"`
	// DatabaseSpecific is free-form, source-database-defined metadata. It is
	// retained verbatim as raw JSON so no information is lost before
	// canonicalization.
	DatabaseSpecific json.RawMessage `json:"database_specific"`
	// Published is the RFC 3339 timestamp the entry was first published.
	Published string `json:"published"`
	// Modified is the RFC 3339 timestamp the entry was last modified.
	Modified string `json:"modified"`
	// Withdrawn, when set, is the RFC 3339 timestamp the entry was withdrawn.
	Withdrawn string `json:"withdrawn"`
}

// Affected describes one affected package and the versions in which it is
// vulnerable, per the OSV `affected[]` schema.
type Affected struct {
	Package           Package         `json:"package"`
	Ranges            []Range         `json:"ranges"`
	Versions          []string        `json:"versions"`
	Severity          []Severity      `json:"severity"`
	EcosystemSpecific json.RawMessage `json:"ecosystem_specific"`
	DatabaseSpecific  json.RawMessage `json:"database_specific"`
}

// Package identifies an affected package by ecosystem, name, and purl.
type Package struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Purl      string `json:"purl"`
}

// Range is a version range of the form described by OSV: a Type ("SEMVER",
// "ECOSYSTEM", or "GIT"), an optional Repo (for GIT ranges), and an ordered
// list of Events marking where the vulnerability was introduced/fixed.
type Range struct {
	Type             string          `json:"type"`
	Repo             string          `json:"repo"`
	Events           []Event         `json:"events"`
	DatabaseSpecific json.RawMessage `json:"database_specific"`
}

// Event is a single boundary in a Range. Exactly one of its fields is set in a
// well-formed OSV document: Introduced (inclusive lower bound, "0" means from
// the start), Fixed (exclusive upper bound), LastAffected (inclusive upper
// bound), or Limit (git range limit).
type Event struct {
	Introduced   string `json:"introduced"`
	Fixed        string `json:"fixed"`
	LastAffected string `json:"last_affected"`
	Limit        string `json:"limit"`
}

// Severity is a machine-readable severity score, e.g. a CVSS vector string.
type Severity struct {
	// Type is the scoring scheme, e.g. "CVSS_V3", "CVSS_V4".
	Type string `json:"type"`
	// Score is the scheme-specific value, e.g. a CVSS vector string.
	Score string `json:"score"`
}

// Aliases returns every external identifier this record carries: the OSV id
// first, followed by the entry's declared aliases, used for dedup. The order
// is the OSV id followed by aliases in document order; no deduplication or
// normalization is performed here (that is the canonicalizer's job), so the
// result is a pure function of the decoded fields.
func (r *Record) Aliases() []string {
	out := make([]string, 0, 1+len(r.Aliases_))
	if r.ID != "" {
		out = append(out, r.ID)
	}
	out = append(out, r.Aliases_...)
	return out
}

// SourceKey returns the stable per-source id of this record: the OSV id.
func (r *Record) SourceKey() string { return r.ID }

// Parser decodes OSV payloads into source records.
type Parser struct{}

// New returns an OSV Parser.
func New() Parser { return Parser{} }

// Source returns the stable source key "osv".
func (Parser) Source() string { return source }

// Parse decodes an OSV payload into one or more source records. It accepts
// either a single OSV JSON object (the common case: one entry per payload) or a
// JSON array of OSV objects (a batch). Parse is a pure function of its input:
// identical bytes always yield identical records.
//
// The ref is accepted to satisfy the parser.Parser interface and is not used
// to influence decoding, preserving purity.
func (Parser) Parse(_ raw.RawRef, payload []byte) ([]parser.SourceRecord, error) {
	trimmed := bytes.TrimLeft(payload, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("osv: empty payload")
	}

	// A batch payload begins with '['; a single entry begins with '{'.
	if trimmed[0] == '[' {
		return parseArray(payload)
	}
	return parseSingle(payload)
}

// parseSingle decodes a single OSV JSON object.
func parseSingle(payload []byte) ([]parser.SourceRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	var rec Record
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("osv: decode entry: %w", err)
	}
	if rec.ID == "" {
		return nil, fmt.Errorf("osv: entry is missing required id field")
	}
	return []parser.SourceRecord{&rec}, nil
}

// parseArray decodes a JSON array of OSV objects into source records,
// preserving document order so output is deterministic.
func parseArray(payload []byte) ([]parser.SourceRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	var recs []Record
	if err := dec.Decode(&recs); err != nil {
		return nil, fmt.Errorf("osv: decode batch: %w", err)
	}
	out := make([]parser.SourceRecord, 0, len(recs))
	for i := range recs {
		if recs[i].ID == "" {
			return nil, fmt.Errorf("osv: batch entry %d is missing required id field", i)
		}
		out = append(out, &recs[i])
	}
	return out, nil
}

// Compile-time assertions that this package satisfies the parser contracts.
var (
	_ parser.Parser       = Parser{}
	_ parser.SourceRecord = (*Record)(nil)
)
