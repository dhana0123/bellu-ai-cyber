// Package kev parses the CISA Known Exploited Vulnerabilities (KEV) catalog
// into typed source records.
//
// The KEV catalog is a JSON document with a top-level "vulnerabilities" array,
// each element describing one known-exploited vulnerability. This package
// decodes that document into one source record per entry; each record exposes
// its CVE id both as its alias and as its stable per-source key.
//
// Parse is a pure function of its input: the same bytes always decode into the
// same records in the same order (the input order is preserved). It performs no
// time.Now() calls and no randomness.
package kev

import (
	"encoding/json"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// Source is the stable source key this parser handles.
const Source = "kev"

// Entry is one vulnerability from the CISA KEV catalog. The JSON field names
// match the upstream catalog schema.
type Entry struct {
	CveID                      string `json:"cveID"`
	VendorProject              string `json:"vendorProject"`
	Product                    string `json:"product"`
	VulnerabilityName          string `json:"vulnerabilityName"`
	DateAdded                  string `json:"dateAdded"`
	ShortDescription           string `json:"shortDescription"`
	RequiredAction             string `json:"requiredAction"`
	DueDate                    string `json:"dueDate"`
	KnownRansomwareCampaignUse string `json:"knownRansomwareCampaignUse"`
}

// Aliases returns every external identifier this entry carries. A KEV entry is
// keyed solely by its CVE id, so that is the only alias.
func (e Entry) Aliases() []string {
	if e.CveID == "" {
		return nil
	}
	return []string{e.CveID}
}

// SourceKey returns the stable per-source id of this entry: its CVE id.
func (e Entry) SourceKey() string { return e.CveID }

// compile-time assertion that Entry satisfies parser.SourceRecord.
var _ parser.SourceRecord = Entry{}

// catalog is the top-level shape of the KEV JSON document. Only the
// vulnerabilities array is needed to produce source records.
type catalog struct {
	Vulnerabilities []Entry `json:"vulnerabilities"`
}

// Parser decodes CISA KEV catalog payloads into Entry source records.
type Parser struct{}

// New returns a KEV Parser.
func New() Parser { return Parser{} }

// Source returns the stable source key for the KEV parser.
func (Parser) Source() string { return Source }

// Parse decodes a KEV catalog JSON payload into one source record per entry in
// the top-level "vulnerabilities" array. It is a pure function of its input:
// the same bytes always yield identical records, and the catalog's input order
// is preserved.
func (Parser) Parse(_ raw.RawRef, payload []byte) ([]parser.SourceRecord, error) {
	var cat catalog
	if err := json.Unmarshal(payload, &cat); err != nil {
		return nil, fmt.Errorf("kev: decode catalog: %w", err)
	}

	recs := make([]parser.SourceRecord, 0, len(cat.Vulnerabilities))
	for _, e := range cat.Vulnerabilities {
		recs = append(recs, e)
	}
	return recs, nil
}

// compile-time assertion that Parser satisfies parser.Parser.
var _ parser.Parser = Parser{}
