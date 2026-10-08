package canonical

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser/epss"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser/kev"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/parser/osv"
)

// TestOSVCanonicalizeMapping confirms an OSV record maps to a valid
// CanonicalVuln: CVE preferred as the id, aliases normalized+deduped, purl
// version stripped, and introduced/fixed events collapsed to a range.
func TestOSVCanonicalizeMapping(t *testing.T) {
	rec := &osv.Record{
		ID:       "GHSA-xxxx-yyyy-zzzz",
		Aliases_: []string{"CVE-2024-1234", "ghsa-xxxx-yyyy-zzzz"}, // dup of ID (lowercased)
		Summary:  "example",
		Affected: []osv.Affected{{
			Package: osv.Package{Ecosystem: "PyPI", Name: "requests", Purl: "pkg:pypi/requests@2.30.0"},
			Ranges: []osv.Range{{
				Type:   "ECOSYSTEM",
				Events: []osv.Event{{Introduced: "0"}, {Fixed: "2.31.0"}},
			}},
		}},
		Severity: []osv.Severity{{Type: "CVSS_V3", Score: "9.8"}},
	}

	out, err := OSVCanonicalizer{}.Canonicalize([]parser.SourceRecord{rec})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 vuln, got %d", len(out))
	}
	v := out[0]
	if v.ID != "CVE-2024-1234" {
		t.Errorf("expected CVE preferred as id, got %q", v.ID)
	}
	if v.Affected[0].Purl != "pkg:pypi/requests" {
		t.Errorf("expected version-stripped purl, got %q", v.Affected[0].Purl)
	}
	if len(v.Affected[0].Ranges) != 1 || v.Affected[0].Ranges[0].Fixed != "2.31.0" {
		t.Errorf("expected one range fixed at 2.31.0, got %+v", v.Affected[0].Ranges)
	}
	if v.CVSS.Score != 9.8 || v.Severity != SevCritical {
		t.Errorf("expected critical/9.8, got %v/%v", v.Severity, v.CVSS.Score)
	}
}

// TestOSVPrefersOSVIDWhenNoCVEorGHSA confirms the OSV id is the fallback id.
func TestOSVPrefersOSVIDWhenNoCVEorGHSA(t *testing.T) {
	rec := &osv.Record{ID: "PYSEC-2024-1"}
	out, err := OSVCanonicalizer{}.Canonicalize([]parser.SourceRecord{rec})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if out[0].ID != "PYSEC-2024-1" {
		t.Errorf("expected OSV id fallback, got %q", out[0].ID)
	}
}

// TestKEVCanonicalizeMapping confirms a KEV entry maps to a KEV-flagged vuln.
func TestKEVCanonicalizeMapping(t *testing.T) {
	out, err := KEVCanonicalizer{}.Canonicalize([]parser.SourceRecord{
		kev.Entry{CveID: "CVE-2024-1234", ShortDescription: "x"},
	})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if out[0].ID != "CVE-2024-1234" || !out[0].KnownExploited {
		t.Errorf("expected KEV-flagged CVE, got %+v", out[0])
	}
}

// TestEPSSCanonicalizeMapping confirms an EPSS row maps and carries EPSS.
func TestEPSSCanonicalizeMapping(t *testing.T) {
	out, err := EPSSCanonicalizer{}.Canonicalize([]parser.SourceRecord{
		epss.Row{CVE: "CVE-2024-1234", EPSS: 0.42},
	})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if out[0].EPSS != 0.42 {
		t.Errorf("expected EPSS 0.42, got %v", out[0].EPSS)
	}
}

// TestEPSSRejectsOutOfRange confirms Requirement 4.7 (EPSS in [0,1]).
func TestEPSSRejectsOutOfRange(t *testing.T) {
	_, err := EPSSCanonicalizer{}.Canonicalize([]parser.SourceRecord{
		epss.Row{CVE: "CVE-2024-1234", EPSS: 1.5},
	})
	if err == nil {
		t.Fatal("expected rejection for EPSS out of range")
	}
}
