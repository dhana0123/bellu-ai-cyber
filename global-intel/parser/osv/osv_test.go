// Golden-file determinism tests for the OSV parser.
//
// These tests pin small, representative recorded OSV payloads (a single entry
// and a two-entry batch) to the expected decoded source records, and assert
// correctness property 3 (parser determinism, Requirement 3.2): parsing the
// same bytes twice yields identical records, with document order preserved.
package osv

import (
	"archive/zip"
	"bytes"
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

// osvSource is the stable source key for the OSV parser. The package-level
// const is unexported, so the tests use a literal that mirrors it.
const osvSource = "osv"

// goldenSingle is a single recorded OSV JSON entry: an id, aliases carrying a
// CVE and a GHSA, an affected package with a purl and introduced/fixed ranges,
// and a CVSS severity vector.
const goldenSingle = `{
  "id": "PYSEC-2024-48",
  "aliases": ["CVE-2024-12345", "GHSA-abcd-efgh-ijkl"],
  "summary": "Example vulnerability in demo package",
  "details": "A longer description of the issue.",
  "affected": [
    {
      "package": {
        "ecosystem": "PyPI",
        "name": "demo",
        "purl": "pkg:pypi/demo"
      },
      "ranges": [
        {
          "type": "ECOSYSTEM",
          "events": [
            {"introduced": "1.0.0"},
            {"fixed": "1.2.3"}
          ]
        }
      ]
    }
  ],
  "severity": [
    {"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}
  ],
  "published": "2024-01-02T00:00:00Z",
  "modified": "2024-01-03T00:00:00Z"
}`

// goldenBatch is a two-entry OSV array batch. Order matters: the parser must
// preserve document order.
const goldenBatch = `[
  {
    "id": "PYSEC-2024-48",
    "aliases": ["CVE-2024-12345", "GHSA-abcd-efgh-ijkl"],
    "affected": [
      {
        "package": {"ecosystem": "PyPI", "name": "demo", "purl": "pkg:pypi/demo"},
        "ranges": [{"type": "ECOSYSTEM", "events": [{"introduced": "1.0.0"}, {"fixed": "1.2.3"}]}]
      }
    ],
    "severity": [{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}]
  },
  {
    "id": "GHSA-1111-2222-3333",
    "aliases": ["CVE-2023-99999"],
    "affected": [
      {
        "package": {"ecosystem": "npm", "name": "widget", "purl": "pkg:npm/widget"},
        "ranges": [{"type": "SEMVER", "events": [{"introduced": "0"}, {"fixed": "2.0.0"}]}]
      }
    ],
    "severity": [{"type": "CVSS_V4", "score": "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"}]
  }
]`

func TestParseSingleGolden(t *testing.T) {
	recs, err := New().Parse(raw.RawRef{Source: osvSource}, []byte(goldenSingle))
	if err != nil {
		t.Fatalf("Parse single: unexpected error: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("Parse single: got %d records, want 1", len(recs))
	}

	rec, ok := recs[0].(*Record)
	if !ok {
		t.Fatalf("Parse single: record is %T, want *Record", recs[0])
	}

	if got, want := rec.SourceKey(), "PYSEC-2024-48"; got != want {
		t.Errorf("SourceKey() = %q, want %q", got, want)
	}
	// Aliases are the OSV id followed by declared aliases, in document order.
	wantAliases := []string{"PYSEC-2024-48", "CVE-2024-12345", "GHSA-abcd-efgh-ijkl"}
	if got := rec.Aliases(); !reflect.DeepEqual(got, wantAliases) {
		t.Errorf("Aliases() = %v, want %v", got, wantAliases)
	}

	// Key decoded fields: affected package purl and introduced/fixed events.
	if len(rec.Affected) != 1 {
		t.Fatalf("got %d affected entries, want 1", len(rec.Affected))
	}
	aff := rec.Affected[0]
	if got, want := aff.Package.Purl, "pkg:pypi/demo"; got != want {
		t.Errorf("affected purl = %q, want %q", got, want)
	}
	if len(aff.Ranges) != 1 || len(aff.Ranges[0].Events) != 2 {
		t.Fatalf("unexpected ranges/events shape: %+v", aff.Ranges)
	}
	if got, want := aff.Ranges[0].Events[0].Introduced, "1.0.0"; got != want {
		t.Errorf("introduced = %q, want %q", got, want)
	}
	if got, want := aff.Ranges[0].Events[1].Fixed, "1.2.3"; got != want {
		t.Errorf("fixed = %q, want %q", got, want)
	}

	// CVSS severity vector.
	if len(rec.Severity) != 1 {
		t.Fatalf("got %d severity entries, want 1", len(rec.Severity))
	}
	if got, want := rec.Severity[0].Type, "CVSS_V3"; got != want {
		t.Errorf("severity type = %q, want %q", got, want)
	}
	wantVector := "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
	if got := rec.Severity[0].Score; got != wantVector {
		t.Errorf("severity vector = %q, want %q", got, wantVector)
	}
}

func TestParseBatchGoldenPreservesOrder(t *testing.T) {
	recs, err := New().Parse(raw.RawRef{Source: osvSource}, []byte(goldenBatch))
	if err != nil {
		t.Fatalf("Parse batch: unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("Parse batch: got %d records, want 2", len(recs))
	}

	// Document order must be preserved.
	wantKeys := []string{"PYSEC-2024-48", "GHSA-1111-2222-3333"}
	for i, want := range wantKeys {
		if got := recs[i].SourceKey(); got != want {
			t.Errorf("record[%d].SourceKey() = %q, want %q", i, got, want)
		}
	}

	// Spot-check the second record's alias set and purl.
	second, ok := recs[1].(*Record)
	if !ok {
		t.Fatalf("record[1] is %T, want *Record", recs[1])
	}
	wantAliases := []string{"GHSA-1111-2222-3333", "CVE-2023-99999"}
	if got := second.Aliases(); !reflect.DeepEqual(got, wantAliases) {
		t.Errorf("record[1].Aliases() = %v, want %v", got, wantAliases)
	}
	if got, want := second.Affected[0].Package.Purl, "pkg:npm/widget"; got != want {
		t.Errorf("record[1] purl = %q, want %q", got, want)
	}
}

// TestParseDeterminism asserts property 3: parsing identical bytes twice yields
// deeply-equal records for both the single and batch payloads.
func TestParseDeterminism(t *testing.T) {
	cases := map[string]string{
		"single": goldenSingle,
		"batch":  goldenBatch,
	}
	p := New()
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			first, err := p.Parse(raw.RawRef{Source: osvSource}, []byte(payload))
			if err != nil {
				t.Fatalf("first parse: %v", err)
			}
			second, err := p.Parse(raw.RawRef{Source: osvSource}, []byte(payload))
			if err != nil {
				t.Fatalf("second parse: %v", err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Errorf("non-deterministic parse: first=%+v second=%+v", first, second)
			}
		})
	}
}

// TestParseZip verifies the parser decodes OSV's bulk "all.zip" format: a ZIP
// archive of one JSON document per advisory. This mirrors the real upstream
// payload the fetcher stores, which previously failed to parse because the
// parser assumed raw JSON and choked on the ZIP magic bytes.
func TestParseZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, name := range []string{"PYSEC-2024-48.json", "PYSEC-2024-49.json"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		// Reuse the golden single entry; the second gets a distinct id so both
		// decode to separate records.
		doc := goldenSingle
		if i == 1 {
			doc = `{"id":"PYSEC-2024-49","summary":"second"}`
		}
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	recs, err := New().Parse(raw.RawRef{}, buf.Bytes())
	if err != nil {
		t.Fatalf("Parse(zip) error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("Parse(zip): got %d records, want 2", len(recs))
	}
	if recs[0].SourceKey() != "PYSEC-2024-48" || recs[1].SourceKey() != "PYSEC-2024-49" {
		t.Fatalf("Parse(zip): unexpected ids %q, %q", recs[0].SourceKey(), recs[1].SourceKey())
	}
}
