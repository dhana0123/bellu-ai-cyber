// Package atlas parses MITRE ATLAS (Adversarial Threat Landscape for
// Artificial-Intelligence Systems) technique data into typed source records.
//
// MITRE publishes the ATLAS matrix as a single data document (ATLAS.yaml,
// mirrored as JSON) on GitHub. The document carries a top-level "matrices"
// (or "techniques") collection; each technique has a stable id of the form
// "AML.Txxxx", a name, a description, and an optional list of AI-model /
// AI-agent identifiers the technique is known to affect. Because the upstream
// shape is YAML-or-JSON and loosely specified, this parser decodes the superset
// of fields Bellu needs and tolerates both the nested "matrices[].techniques"
// layout and a flat top-level "techniques" list.
//
// Parse is a pure function of its input (Requirement 3.2): the same bytes
// always decode into the same records in the same order. It performs no I/O,
// reads no clock, and uses no randomness. Decoding uses gopkg.in/yaml.v3, which
// also accepts JSON (JSON is a subset of YAML), so a JSON mirror parses too.
package atlas

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/bellu/bellu-ai-cyber/global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

// Source is the stable source key this parser handles.
const Source = "atlas"

// Technique is one decoded ATLAS technique. The field tags mirror the ATLAS
// data schema key names so yaml.v3 maps them directly; unknown keys are
// ignored. Technique satisfies parser.SourceRecord.
type Technique struct {
	// ID is the stable ATLAS technique id, e.g. "AML.T0051". It is both the
	// SourceKey and the primary alias.
	ID string `yaml:"id" json:"id"`
	// Name is the human-readable technique name.
	Name string `yaml:"name" json:"name"`
	// Description is the long-form technique description.
	Description string `yaml:"description" json:"description"`
	// Tactics are the ATLAS tactic ids this technique belongs to (contextual).
	Tactics []string `yaml:"tactics" json:"tactics"`
	// AffectedModels is the set of AI model / AI agent identifiers this
	// technique is known to apply to. These are the identifiers the canonical
	// mapping emits so the Intelligence Matcher can join to AIModel / AIAgent
	// graph nodes.
	AffectedModels []string `yaml:"affected_models" json:"affected_models"`
	// References are external identifiers (CVE/CWE/other) associated with the
	// technique; retained as additional aliases.
	References []string `yaml:"references" json:"references"`
}

// Aliases returns every external identifier this technique carries: its ATLAS
// id first, then any declared references, in document order. No normalization
// or de-duplication is performed here (that is the canonicalizer's job), so the
// result is a pure function of the decoded fields.
func (t Technique) Aliases() []string {
	out := make([]string, 0, 1+len(t.References))
	if t.ID != "" {
		out = append(out, t.ID)
	}
	out = append(out, t.References...)
	return out
}

// SourceKey returns the stable per-source id of this technique: its ATLAS id.
func (t Technique) SourceKey() string { return t.ID }

// compile-time assertion that Technique satisfies parser.SourceRecord.
var _ parser.SourceRecord = Technique{}

// document is the superset of top-level ATLAS layouts this parser accepts. A
// document may list techniques flat under "techniques" or nested under
// "matrices[].techniques"; both are decoded and concatenated in document order.
type document struct {
	Techniques []Technique `yaml:"techniques" json:"techniques"`
	Matrices   []struct {
		ID         string      `yaml:"id" json:"id"`
		Name       string      `yaml:"name" json:"name"`
		Techniques []Technique `yaml:"techniques" json:"techniques"`
	} `yaml:"matrices" json:"matrices"`
}

// Parser decodes ATLAS data payloads into Technique source records.
type Parser struct{}

// New returns an ATLAS Parser.
func New() Parser { return Parser{} }

// Source returns the stable source key "atlas".
func (Parser) Source() string { return Source }

// Parse decodes an ATLAS data payload (YAML or JSON) into one source record per
// technique. Flat and matrix-nested layouts are both supported; techniques are
// returned in a deterministic order (document order within each list, flat
// first then matrices, then a stable sort by id so re-ordered upstream input
// still yields identical output). Parse is a pure function of its input.
func (Parser) Parse(_ raw.RawRef, payload []byte) ([]parser.SourceRecord, error) {
	var doc document
	if err := yaml.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("atlas: decode document: %w", err)
	}

	techniques := make([]Technique, 0, len(doc.Techniques))
	techniques = append(techniques, doc.Techniques...)
	for _, m := range doc.Matrices {
		techniques = append(techniques, m.Techniques...)
	}

	// Stable sort by id so identical sets of techniques in any upstream order
	// decode to identical output (determinism beyond mere input-order
	// preservation). SliceStable keeps the relative order of equal ids.
	sort.SliceStable(techniques, func(i, j int) bool {
		return techniques[i].ID < techniques[j].ID
	})

	recs := make([]parser.SourceRecord, 0, len(techniques))
	for _, t := range techniques {
		if t.ID == "" {
			return nil, fmt.Errorf("atlas: technique missing required id field")
		}
		recs = append(recs, t)
	}
	return recs, nil
}

// compile-time assertion that Parser satisfies parser.Parser.
var _ parser.Parser = Parser{}
