// Package parser defines the Parser abstraction that decodes raw payloads of
// one source into typed source records. A Parser is a pure function of its
// input: the same bytes always produce the same records.
//
// Subpackages implement per-source parsing: osv, kev, epss (Phase 1) and nvd,
// ghsa, atlas, avid, mcp (reserved for later phases, see stubs.go). Concrete
// source packages define their own structs (e.g. osv.Record, kev.Entry,
// epss.Row) that satisfy SourceRecord via a small adapter.
package parser

import (
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/raw"
)

// Parser decodes raw payloads of one source into typed source records.
type Parser interface {
	// Source returns the stable source key this parser handles, e.g. "osv".
	Source() string
	// Parse decodes a raw payload into zero or more source records. It is a
	// pure function of its input: same bytes -> same records.
	Parse(ref raw.RawRef, payload []byte) ([]SourceRecord, error)
}

// SourceRecord is the loosely-typed per-source intermediate produced by a
// Parser. Concrete source packages define their own structs that satisfy this
// via a small adapter.
type SourceRecord interface {
	// Aliases returns every external identifier this record carries
	// (CVE-..., GHSA-..., OSV id, etc.) used for dedup.
	Aliases() []string
	// SourceKey returns the stable per-source id of this record.
	SourceKey() string
}
