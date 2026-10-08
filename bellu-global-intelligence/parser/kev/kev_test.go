// Golden-file determinism tests for the KEV parser.
//
// These tests pin a small recorded CISA KEV catalog payload (a two-entry
// "vulnerabilities" array) to the expected decoded source records, and assert
// correctness property 3 (parser determinism, Requirement 3.2): parsing the
// same bytes twice yields identical records, with catalog order preserved.
package kev

import (
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/raw"
)

// goldenCatalog is a recorded KEV catalog JSON with two vulnerabilities. Order
// matters: the parser must preserve the catalog's input order.
const goldenCatalog = `{
  "title": "CISA Catalog of Known Exploited Vulnerabilities",
  "catalogVersion": "2024.01.02",
  "dateReleased": "2024-01-02T12:00:00.000Z",
  "count": 2,
  "vulnerabilities": [
    {
      "cveID": "CVE-2024-12345",
      "vendorProject": "DemoCorp",
      "product": "DemoServer",
      "vulnerabilityName": "DemoServer Remote Code Execution",
      "dateAdded": "2024-01-02",
      "shortDescription": "A remote code execution flaw.",
      "requiredAction": "Apply updates per vendor instructions.",
      "dueDate": "2024-01-23",
      "knownRansomwareCampaignUse": "Known"
    },
    {
      "cveID": "CVE-2023-99999",
      "vendorProject": "WidgetInc",
      "product": "WidgetKit",
      "vulnerabilityName": "WidgetKit Authentication Bypass",
      "dateAdded": "2023-12-15",
      "shortDescription": "An authentication bypass vulnerability.",
      "requiredAction": "Apply updates per vendor instructions.",
      "dueDate": "2024-01-05",
      "knownRansomwareCampaignUse": "Unknown"
    }
  ]
}`

func TestParseCatalogGolden(t *testing.T) {
	recs, err := New().Parse(raw.RawRef{Source: Source}, []byte(goldenCatalog))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("Parse: got %d records, want 2", len(recs))
	}

	// Catalog order must be preserved; for KEV the alias and the source key
	// are both the CVE id.
	wantKeys := []string{"CVE-2024-12345", "CVE-2023-99999"}
	for i, want := range wantKeys {
		if got := recs[i].SourceKey(); got != want {
			t.Errorf("record[%d].SourceKey() = %q, want %q", i, got, want)
		}
		if got := recs[i].Aliases(); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("record[%d].Aliases() = %v, want [%q]", i, got, want)
		}
	}

	// Key decoded fields on the first entry.
	first, ok := recs[0].(Entry)
	if !ok {
		t.Fatalf("record[0] is %T, want Entry", recs[0])
	}
	if got, want := first.VendorProject, "DemoCorp"; got != want {
		t.Errorf("vendorProject = %q, want %q", got, want)
	}
	if got, want := first.Product, "DemoServer"; got != want {
		t.Errorf("product = %q, want %q", got, want)
	}
	if got, want := first.DateAdded, "2024-01-02"; got != want {
		t.Errorf("dateAdded = %q, want %q", got, want)
	}
	if got, want := first.KnownRansomwareCampaignUse, "Known"; got != want {
		t.Errorf("knownRansomwareCampaignUse = %q, want %q", got, want)
	}
}

// TestParseDeterminism asserts property 3: parsing identical bytes twice yields
// deeply-equal records.
func TestParseDeterminism(t *testing.T) {
	p := New()
	first, err := p.Parse(raw.RawRef{Source: Source}, []byte(goldenCatalog))
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	second, err := p.Parse(raw.RawRef{Source: Source}, []byte(goldenCatalog))
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("non-deterministic parse: first=%+v second=%+v", first, second)
	}
}
