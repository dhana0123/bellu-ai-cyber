package intelcorrelate

import (
	"context"
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// fakeIntel is an in-memory IntelSource backed by a fixed corpus.
type fakeIntel struct {
	vulns []canonical.CanonicalVuln
}

func (f fakeIntel) VulnsByPurl(_ context.Context, purlName string) ([]canonical.CanonicalVuln, error) {
	var out []canonical.CanonicalVuln
	for _, v := range f.vulns {
		for _, ap := range v.Affected {
			if ap.Purl == purlName {
				out = append(out, v)
				break
			}
		}
	}
	return out, nil
}

func (f fakeIntel) AllVulns(_ context.Context) ([]canonical.CanonicalVuln, error) {
	return f.vulns, nil
}

// fakeGraph returns canned rows per query string, mimicking QueryRows.
type fakeGraph struct {
	rows map[string][]map[string]any
}

func (f fakeGraph) QueryRows(_ context.Context, cypher string, _ map[string]any) ([]map[string]any, error) {
	return f.rows[cypher], nil
}

func pkgVuln(id, purl, introduced, fixed string) canonical.CanonicalVuln {
	return canonical.CanonicalVuln{
		ID:      id,
		Aliases: []string{id},
		Affected: []canonical.AffectedPackage{{
			Purl:      purl,
			Ecosystem: "PyPI",
			Ranges:    []canonical.SemverRange{{Introduced: introduced, Fixed: fixed}},
		}},
	}
}

func TestMatchPackageVulns_VersionInRangeEmits(t *testing.T) {
	vulns := []canonical.CanonicalVuln{
		pkgVuln("CVE-2024-0001", "pkg:pypi/requests", "2.0.0", "2.31.0"),
	}
	got := matchPackageVulns("package:pkg:pypi/requests@2.30.0", "pkg:pypi/requests", "2.30.0", vulns)
	want := []Candidate{{
		AssetID:    "package:pkg:pypi/requests@2.30.0",
		AssetKind:  model.KindPackage,
		VulnID:     "CVE-2024-0001",
		IntelRef:   "CVE-2024-0001",
		MatchBasis: BasisPurlRange,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMatchPackageVulns_VersionOutsideRangeNotEmitted(t *testing.T) {
	// Req 8.5: a version outside every declared range must NOT be emitted.
	vulns := []canonical.CanonicalVuln{
		pkgVuln("CVE-2024-0001", "pkg:pypi/requests", "2.0.0", "2.31.0"),
	}
	// 2.31.0 is the exclusive upper bound: it is fixed, so NOT affected.
	if got := matchPackageVulns("asset", "pkg:pypi/requests", "2.31.0", vulns); len(got) != 0 {
		t.Fatalf("expected no candidates for fixed version, got %+v", got)
	}
	// A version below the introduced bound is also not affected.
	if got := matchPackageVulns("asset", "pkg:pypi/requests", "1.9.0", vulns); len(got) != 0 {
		t.Fatalf("expected no candidates for pre-introduced version, got %+v", got)
	}
}

func TestMatchPackageVulns_PurlNameMismatch(t *testing.T) {
	vulns := []canonical.CanonicalVuln{
		pkgVuln("CVE-2024-0001", "pkg:pypi/requests", "0", ""),
	}
	if got := matchPackageVulns("asset", "pkg:pypi/urllib3", "1.0.0", vulns); len(got) != 0 {
		t.Fatalf("expected no candidates for different purl, got %+v", got)
	}
}

func TestCorrelate_PackageEndToEndWithFakes(t *testing.T) {
	intel := fakeIntel{vulns: []canonical.CanonicalVuln{
		pkgVuln("CVE-2024-0001", "pkg:pypi/requests", "2.0.0", "2.31.0"),
		pkgVuln("CVE-2024-9999", "pkg:pypi/urllib3", "0", ""), // different purl, no graph asset
	}}
	g := fakeGraph{rows: map[string][]map[string]any{
		cypherPackages: {
			{"id": "package:pkg:pypi/requests@2.30.0", "purl": "pkg:pypi/requests@2.30.0"},
			{"id": "package:pkg:pypi/requests@2.31.0", "purl": "pkg:pypi/requests@2.31.0"}, // fixed -> no match
		},
		// images / AI empty
	}}

	cands, err := New(g, intel).Correlate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{
		AssetID:    "package:pkg:pypi/requests@2.30.0",
		AssetKind:  model.KindPackage,
		VulnID:     "CVE-2024-0001",
		IntelRef:   "CVE-2024-0001",
		MatchBasis: BasisPurlRange,
	}}
	if !reflect.DeepEqual(cands, want) {
		t.Fatalf("got %+v, want %+v", cands, want)
	}
}

func TestMatchImageVulns_DigestAndPackage(t *testing.T) {
	digestVuln := canonical.CanonicalVuln{
		ID:      "CVE-IMG-1",
		Aliases: []string{"CVE-IMG-1"},
		Affected: []canonical.AffectedPackage{{
			Purl:      "sha256:abc123",
			Ecosystem: "oci",
		}},
	}
	pkgInImage := pkgVuln("CVE-IMG-2", "pkg:deb/openssl", "0", "3.0.0")

	got := matchImageVulns(
		"image:sha256:abc123",
		"sha256:ABC123", // case-insensitive digest match
		[]string{"pkg:deb/openssl@1.1.1"},
		[]canonical.CanonicalVuln{digestVuln, pkgInImage},
	)
	// Expect one image-digest and one image-package candidate.
	if len(got) != 2 {
		t.Fatalf("expected 2 image candidates, got %+v", got)
	}
	var sawDigest, sawPkg bool
	for _, c := range got {
		if c.AssetKind != model.KindContainerImage {
			t.Fatalf("wrong asset kind: %+v", c)
		}
		switch c.MatchBasis {
		case BasisImageDigest:
			sawDigest = c.VulnID == "CVE-IMG-1"
		case BasisImagePackage:
			sawPkg = c.VulnID == "CVE-IMG-2"
		}
	}
	if !sawDigest || !sawPkg {
		t.Fatalf("missing expected image candidates: %+v", got)
	}
}

func TestMatchAIAndMCPVulns(t *testing.T) {
	modelVuln := canonical.CanonicalVuln{
		ID:       "AI-1",
		Aliases:  []string{"AI-1"},
		Affected: []canonical.AffectedPackage{{Purl: "meta-llama/Llama-3-8B", Ecosystem: "huggingface"}},
	}
	mcpVuln := canonical.CanonicalVuln{
		ID:       "MCP-1",
		Aliases:  []string{"MCP-1"},
		Affected: []canonical.AffectedPackage{{Purl: "filesystem", Ecosystem: "mcp"}},
	}

	aiGot := matchAIModelVulns("ai:model:1", "meta-llama/Llama-3-8B", []canonical.CanonicalVuln{modelVuln, mcpVuln})
	if len(aiGot) != 1 || aiGot[0].VulnID != "AI-1" || aiGot[0].MatchBasis != BasisAIModel {
		t.Fatalf("unexpected AI model candidates: %+v", aiGot)
	}
	mcpGot := matchMCPVulns("mcp:1", "filesystem", []canonical.CanonicalVuln{modelVuln, mcpVuln})
	if len(mcpGot) != 1 || mcpGot[0].VulnID != "MCP-1" || mcpGot[0].MatchBasis != BasisMCP {
		t.Fatalf("unexpected MCP candidates: %+v", mcpGot)
	}
}

func TestCorrelate_DeterministicOrder(t *testing.T) {
	intel := fakeIntel{vulns: []canonical.CanonicalVuln{
		pkgVuln("CVE-B", "pkg:pypi/x", "0", ""),
		pkgVuln("CVE-A", "pkg:pypi/x", "0", ""),
	}}
	g := fakeGraph{rows: map[string][]map[string]any{
		cypherPackages: {{"id": "package:pkg:pypi/x@1.0.0", "purl": "pkg:pypi/x@1.0.0"}},
	}}
	cands, err := New(g, intel).Correlate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 || cands[0].VulnID != "CVE-A" || cands[1].VulnID != "CVE-B" {
		t.Fatalf("candidates not sorted deterministically: %+v", cands)
	}
}
