package parser

import (
	"errors"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// ErrReservedSource is returned by a reserved-for-later Parser stub when its
// Parse method is invoked. These sources are defined as interface stubs now
// (Requirement 1.7) and implemented in a later phase.
var ErrReservedSource = errors.New("parser: source reserved for a later phase, not yet implemented")

// reservedParser is a stub Parser for sources that are planned but not yet
// implemented. It satisfies the Parser interface so the source key is
// addressable, but every Parse returns ErrReservedSource rather than decoding
// any payload.
type reservedParser struct {
	source string
}

// Source returns the stable source key for this reserved source.
func (p reservedParser) Source() string { return p.source }

// Parse always returns ErrReservedSource: the source is reserved for a later
// phase and decodes nothing.
func (p reservedParser) Parse(_ raw.RawRef, _ []byte) ([]SourceRecord, error) {
	return nil, ErrReservedSource
}

// Reserved-for-later Parser stubs (Requirement 1.7). NVD, GHSA, ATLAS, OWASP
// GenAI, AVID, MCP intelligence, and vendor advisories are exposed as Parser
// stubs now and implemented in a later phase. Each returns a Parser whose
// Source() is addressable and whose Parse() returns ErrReservedSource.
func NewNVDParser() Parser            { return reservedParser{source: "nvd"} }
func NewGHSAParser() Parser           { return reservedParser{source: "ghsa"} }
func NewATLASParser() Parser          { return reservedParser{source: "atlas"} }
func NewOWASPGenAIParser() Parser     { return reservedParser{source: "owasp-genai"} }
func NewAVIDParser() Parser           { return reservedParser{source: "avid"} }
func NewMCPParser() Parser            { return reservedParser{source: "mcp"} }
func NewVendorAdvisoryParser() Parser { return reservedParser{source: "vendor-advisory"} }

// Compile-time assertion that the stub satisfies the Parser interface.
var _ Parser = reservedParser{}
