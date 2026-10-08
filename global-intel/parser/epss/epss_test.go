// Golden-file determinism tests for the EPSS parser.
//
// These tests pin a small recorded FIRST EPSS CSV payload (comment header,
// column header, and three data rows) to the expected decoded source records,
// and assert correctness property 3 (parser determinism, Requirement 3.2):
// parsing the same bytes twice yields identical records with input order
// preserved, and a gzip-compressed payload decodes to records identical to the
// plain-CSV variant.
package epss

import (
	"bytes"
	"compress/gzip"
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

// goldenCSV is a recorded EPSS CSV: a "#...score_date:..." comment header, the
// "cve,epss,percentile" column header, and three data rows. Row order matters:
// the parser preserves input order.
const goldenCSV = "#model_version:v2024.03.01,score_date:2024-01-02T00:00:00+0000\n" +
	"cve,epss,percentile\n" +
	"CVE-2024-12345,0.95432,0.99812\n" +
	"CVE-2023-99999,0.01234,0.42100\n" +
	"CVE-2022-00001,0.00042,0.01000\n"

func TestParseCSVGolden(t *testing.T) {
	recs, err := New().Parse(raw.RawRef{Source: Source}, []byte(goldenCSV))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("Parse: got %d records, want 3", len(recs))
	}

	wantRows := []Row{
		{CVE: "CVE-2024-12345", EPSS: 0.95432, Percentile: 0.99812, ScoredOn: "2024-01-02T00:00:00+0000"},
		{CVE: "CVE-2023-99999", EPSS: 0.01234, Percentile: 0.42100, ScoredOn: "2024-01-02T00:00:00+0000"},
		{CVE: "CVE-2022-00001", EPSS: 0.00042, Percentile: 0.01000, ScoredOn: "2024-01-02T00:00:00+0000"},
	}

	for i, want := range wantRows {
		got, ok := recs[i].(Row)
		if !ok {
			t.Fatalf("record[%d] is %T, want Row", i, recs[i])
		}
		if got != want {
			t.Errorf("record[%d] = %+v, want %+v", i, got, want)
		}
		// Alias is the bare CVE id; the source key combines CVE and score date.
		if a := got.Aliases(); !reflect.DeepEqual(a, []string{want.CVE}) {
			t.Errorf("record[%d].Aliases() = %v, want [%q]", i, a, want.CVE)
		}
		wantKey := want.CVE + "@" + want.ScoredOn
		if k := got.SourceKey(); k != wantKey {
			t.Errorf("record[%d].SourceKey() = %q, want %q", i, k, wantKey)
		}
	}
}

// TestParseGzipMatchesPlain asserts that a gzip-compressed payload decodes to
// records identical to the plain-CSV variant (transparent decompression).
func TestParseGzipMatchesPlain(t *testing.T) {
	p := New()

	plain, err := p.Parse(raw.RawRef{Source: Source}, []byte(goldenCSV))
	if err != nil {
		t.Fatalf("parse plain: %v", err)
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(goldenCSV)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	gz, err := p.Parse(raw.RawRef{Source: Source}, buf.Bytes())
	if err != nil {
		t.Fatalf("parse gzip: %v", err)
	}

	if !reflect.DeepEqual(plain, gz) {
		t.Errorf("gzip records differ from plain records:\nplain=%+v\ngzip=%+v", plain, gz)
	}
}

// TestParseDeterminism asserts property 3: parsing identical bytes twice yields
// deeply-equal records.
func TestParseDeterminism(t *testing.T) {
	p := New()
	first, err := p.Parse(raw.RawRef{Source: Source}, []byte(goldenCSV))
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	second, err := p.Parse(raw.RawRef{Source: Source}, []byte(goldenCSV))
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("non-deterministic parse: first=%+v second=%+v", first, second)
	}
}
