// Package epss parses the FIRST Exploit Prediction Scoring System (EPSS) CSV
// feed into typed source records.
//
// The EPSS feed is a CSV with one row per CVE carrying its exploit-prediction
// probability and percentile. The feed is prefixed by a comment line of the
// form "#model_version:v...,score_date:YYYY-MM-DDTHH:MM:SS+ZZ:ZZ" that records
// the model version and the date the scores were computed, followed by a header
// line "cve,epss,percentile" and then the data rows. The feed is commonly
// served gzip-compressed.
//
// This package decodes that feed into one source record per data row; each
// record exposes its CVE id as its alias and a stable per-source key of
// "CVE@scoreDate" (falling back to the bare CVE id when no score date is
// present in the comment header).
//
// Parse is a pure function of its input: the same bytes always decode into the
// same records in the same order (the input order is preserved). It performs no
// time.Now() calls and no randomness. gzip-compressed payloads are detected by
// their magic bytes and transparently decompressed; any other payload is
// treated as plain CSV.
package epss

import (
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/raw"
)

// Source is the stable source key this parser handles.
const Source = "epss"

// Row is one EPSS score for a single CVE on a given scoring date.
type Row struct {
	CVE        string
	EPSS       float64
	Percentile float64
	// ScoredOn is the score date parsed from the CSV comment header
	// (score_date), in whatever form the header carried it (e.g.
	// "2024-01-02"). It may be empty when the header is absent.
	ScoredOn string
}

// Aliases returns every external identifier this row carries. An EPSS row is
// keyed solely by its CVE id, so that is the only alias.
func (r Row) Aliases() []string {
	if r.CVE == "" {
		return nil
	}
	return []string{r.CVE}
}

// SourceKey returns the stable per-source id of this row. EPSS scores are a
// daily series, so the key combines the CVE id with the score date when one is
// known; otherwise it is the bare CVE id.
func (r Row) SourceKey() string {
	if r.ScoredOn == "" {
		return r.CVE
	}
	return r.CVE + "@" + r.ScoredOn
}

// compile-time assertion that Row satisfies parser.SourceRecord.
var _ parser.SourceRecord = Row{}

// Parser decodes FIRST EPSS CSV payloads into Row source records.
type Parser struct{}

// New returns an EPSS Parser.
func New() Parser { return Parser{} }

// Source returns the stable source key for the EPSS parser.
func (Parser) Source() string { return Source }

// gzipMagic is the two-byte magic prefix of a gzip stream (RFC 1952).
var gzipMagic = []byte{0x1f, 0x8b}

// Parse decodes an EPSS CSV payload (optionally gzip-compressed) into one
// source record per data row. It is a pure function of its input: the same
// bytes always yield identical records, and the input row order is preserved.
func (Parser) Parse(_ raw.RawRef, payload []byte) ([]parser.SourceRecord, error) {
	data := payload
	if bytes.HasPrefix(payload, gzipMagic) {
		zr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("epss: open gzip: %w", err)
		}
		defer zr.Close()
		decompressed, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("epss: decompress: %w", err)
		}
		data = decompressed
	}

	scoredOn := scoreDate(data)

	r := csv.NewReader(bytes.NewReader(data))
	// Rows have a fixed 3-column shape (cve,epss,percentile); comment lines are
	// handled by skipping any record whose first field begins with '#'.
	r.FieldsPerRecord = -1
	r.Comment = '#'

	var recs []parser.SourceRecord
	seenHeader := false
	for {
		fields, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("epss: read csv: %w", err)
		}
		if len(fields) < 3 {
			continue
		}
		// Skip the column header line ("cve,epss,percentile"), once.
		if !seenHeader && strings.EqualFold(strings.TrimSpace(fields[0]), "cve") {
			seenHeader = true
			continue
		}

		cve := strings.TrimSpace(fields[0])
		if cve == "" {
			continue
		}
		score, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("epss: parse epss score for %q: %w", cve, err)
		}
		pct, err := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("epss: parse percentile for %q: %w", cve, err)
		}

		recs = append(recs, Row{
			CVE:        cve,
			EPSS:       score,
			Percentile: pct,
			ScoredOn:   scoredOn,
		})
	}
	return recs, nil
}

// scoreDate extracts the score_date value from the EPSS CSV comment header. The
// header looks like "#model_version:v2024.03.01,score_date:2024-01-02T00:00:00+0000".
// It returns the empty string when no score_date is present. This is a pure
// function of the input bytes.
func scoreDate(data []byte) string {
	// The comment header is the first line of the (decompressed) payload.
	line := data
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		line = data[:i]
	}
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "#") {
		return ""
	}
	// Split the comment into comma-separated "key:value" (or "key=value")
	// tokens and return the score_date value.
	for _, tok := range strings.Split(strings.TrimPrefix(s, "#"), ",") {
		tok = strings.TrimSpace(tok)
		var key, val string
		if i := strings.IndexAny(tok, ":="); i >= 0 {
			key = strings.TrimSpace(tok[:i])
			val = strings.TrimSpace(tok[i+1:])
		} else {
			continue
		}
		if strings.EqualFold(key, "score_date") {
			return val
		}
	}
	return ""
}

// compile-time assertion that Parser satisfies parser.Parser.
var _ parser.Parser = Parser{}
