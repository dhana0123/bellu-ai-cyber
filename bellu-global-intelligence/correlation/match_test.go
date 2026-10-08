package correlation

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
)

func TestSplitPurl(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantVer  string
	}{
		{"pkg:pypi/requests@2.30.0", "pkg:pypi/requests", "2.30.0"},
		{"pkg:pypi/requests", "pkg:pypi/requests", ""},
		{"  pkg:npm/left-pad@1.0.0  ", "pkg:npm/left-pad", "1.0.0"},
		{"pkg:golang/github.com/foo/bar@v1.2.3", "pkg:golang/github.com/foo/bar", "v1.2.3"},
		{"", "", ""},
		{"pkg:pypi/requests@", "pkg:pypi/requests", ""},
	}
	for _, c := range cases {
		gotName, gotVer := SplitPurl(c.in)
		if gotName != c.wantName || gotVer != c.wantVer {
			t.Errorf("SplitPurl(%q)=(%q,%q) want (%q,%q)", c.in, gotName, gotVer, c.wantName, c.wantVer)
		}
	}
}

// TestMatchPackageRangeBoundaries verifies the matcher reuses the half-open
// [Introduced, Fixed) containment: a version at the introduced edge matches, a
// version at the fixed edge does not, and a version outside every declared
// range is never reported.
//
// **Validates: Requirements 8.1, 8.5**
func TestMatchPackageRangeBoundaries(t *testing.T) {
	affected := []canonical.AffectedPackage{
		{Purl: "pkg:pypi/requests", Ecosystem: "PyPI", Ranges: []canonical.SemverRange{{Introduced: "2.0.0", Fixed: "2.31.0"}}},
	}
	cases := []struct {
		name    string
		purl    string
		version string
		want    bool
	}{
		{"introduced edge inclusive", "pkg:pypi/requests", "2.0.0", true},
		{"inside range", "pkg:pypi/requests", "2.30.0", true},
		{"just below fixed", "pkg:pypi/requests", "2.30.999", true},
		{"fixed edge exclusive", "pkg:pypi/requests", "2.31.0", false},
		{"above fixed", "pkg:pypi/requests", "3.0.0", false},
		{"below introduced", "pkg:pypi/requests", "1.9.9", false},
		{"wrong purl name", "pkg:pypi/urllib3", "2.30.0", false},
		{"empty version never matches", "pkg:pypi/requests", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchesPackage(affected, c.purl, c.version); got != c.want {
				t.Errorf("MatchesPackage(%q,%q)=%v want %v", c.purl, c.version, got, c.want)
			}
		})
	}
}

// TestMatchPackageMultipleRanges verifies that a version matches when it falls
// in any one of several declared ranges, and does not match when it falls in
// the gap between them (Requirement 8.5).
func TestMatchPackageMultipleRanges(t *testing.T) {
	affected := []canonical.AffectedPackage{
		{Purl: "pkg:npm/foo", Ecosystem: "npm", Ranges: []canonical.SemverRange{
			{Introduced: "1.0.0", Fixed: "1.2.0"},
			{Introduced: "2.0.0", Fixed: "2.5.0"},
		}},
	}
	cases := []struct {
		version string
		want    bool
	}{
		{"1.1.0", true},  // first range
		{"2.4.0", true},  // second range
		{"1.5.0", false}, // gap between ranges
		{"2.5.0", false}, // fixed edge of second range
	}
	for _, c := range cases {
		if got := MatchesPackage(affected, "pkg:npm/foo", c.version); got != c.want {
			t.Errorf("version %q: got %v want %v", c.version, got, c.want)
		}
	}
}

// TestMatchPackageNoRangesMatchesWholePackage verifies the documented choice
// that an AffectedPackage with zero declared ranges is treated as "all versions
// affected", so a purl-name match alone is sufficient.
func TestMatchPackageNoRangesMatchesWholePackage(t *testing.T) {
	affected := []canonical.AffectedPackage{
		{Purl: "pkg:pypi/leftpad", Ecosystem: "PyPI"}, // no ranges
	}
	if !MatchesPackage(affected, "pkg:pypi/leftpad", "9.9.9") {
		t.Error("expected whole-package match when no ranges declared")
	}
	if MatchesPackage(affected, "pkg:pypi/other", "1.0.0") {
		t.Error("different purl must not match")
	}
}

// TestMatchPackageReturnsMatchedEntry verifies the matched AffectedPackage is
// returned so callers can carry its provenance.
func TestMatchPackageReturnsMatchedEntry(t *testing.T) {
	affected := []canonical.AffectedPackage{
		{Purl: "pkg:pypi/a", Ecosystem: "PyPI", Ranges: []canonical.SemverRange{{Introduced: "0", Fixed: "1.0.0"}}},
		{Purl: "pkg:pypi/b", Ecosystem: "PyPI", Ranges: []canonical.SemverRange{{Introduced: "0", Fixed: "2.0.0"}}},
	}
	ok, ap := MatchPackage(affected, "pkg:pypi/b", "1.5.0")
	if !ok {
		t.Fatal("expected match")
	}
	if ap.Purl != "pkg:pypi/b" {
		t.Errorf("matched wrong entry: %+v", ap)
	}
}

func TestMatchContainerImageDigest(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"sha256:abc", "sha256:abc", true},
		{"sha256:ABC", "sha256:abc", true},   // case-insensitive
		{" sha256:abc ", "sha256:abc", true}, // trimmed
		{"sha256:abc", "sha256:def", false},
		{"", "sha256:abc", false},
		{"sha256:abc", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := MatchContainerImageDigest(c.a, c.b); got != c.want {
			t.Errorf("MatchContainerImageDigest(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestMatchContainerImagePackages verifies image-package matching delegates to
// MatchPackage over embedded versioned purls (Requirement 8.2).
func TestMatchContainerImagePackages(t *testing.T) {
	affected := []canonical.AffectedPackage{
		{Purl: "pkg:pypi/requests", Ecosystem: "PyPI", Ranges: []canonical.SemverRange{{Introduced: "0", Fixed: "2.31.0"}}},
	}
	imagePkgs := []string{
		"pkg:pypi/urllib3@2.0.0",
		"pkg:pypi/requests@2.30.0", // affected
		"pkg:pypi/flask@3.0.0",
	}
	ok, purl, ap := MatchContainerImagePackages(affected, imagePkgs)
	if !ok {
		t.Fatal("expected an embedded package to match")
	}
	if purl != "pkg:pypi/requests@2.30.0" {
		t.Errorf("matched wrong purl: %q", purl)
	}
	if ap.Purl != "pkg:pypi/requests" {
		t.Errorf("matched wrong affected entry: %+v", ap)
	}

	// A patched image (requests above the fixed bound) must not match.
	patched := []string{"pkg:pypi/requests@2.31.0"}
	if ok, _, _ := MatchContainerImagePackages(affected, patched); ok {
		t.Error("patched image must not match (version at fixed edge)")
	}

	// No embedded packages: no match.
	if ok, _, _ := MatchContainerImagePackages(affected, nil); ok {
		t.Error("empty image package set must not match")
	}
}

func TestMatchAIModel(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"gpt-4o", "gpt-4o", true},
		{"GPT-4o", "gpt-4o", true}, // case-insensitive
		{" claude-3 ", "claude-3", true},
		{"gpt-4o", "gpt-3.5", false},
		{"", "gpt-4o", false},
		{"gpt-4o", "", false},
	}
	for _, c := range cases {
		if got := MatchAIModel(c.a, c.b); got != c.want {
			t.Errorf("MatchAIModel(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestMatchMCPServer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"filesystem-mcp", "filesystem-mcp", true},
		{"Filesystem-MCP", "filesystem-mcp", true},
		{"github-mcp", "slack-mcp", false},
		{"", "github-mcp", false},
	}
	for _, c := range cases {
		if got := MatchMCPServer(c.a, c.b); got != c.want {
			t.Errorf("MatchMCPServer(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestMatchPackageDeterministicFirstWins verifies evaluation is in slice order
// and deterministic: the first matching entry is returned.
func TestMatchPackageDeterministicFirstWins(t *testing.T) {
	affected := []canonical.AffectedPackage{
		{Purl: "pkg:pypi/x", Ecosystem: "PyPI", Ranges: []canonical.SemverRange{{Introduced: "0", Fixed: "5.0.0"}}},
		{Purl: "pkg:pypi/x", Ecosystem: "PyPI", Ranges: []canonical.SemverRange{{Introduced: "0", Fixed: "10.0.0"}}},
	}
	for i := 0; i < 5; i++ {
		ok, ap := MatchPackage(affected, "pkg:pypi/x", "1.0.0")
		if !ok || ap.Ranges[0].Fixed != "5.0.0" {
			t.Fatalf("expected first entry (Fixed=5.0.0) to win deterministically, got %+v ok=%v", ap, ok)
		}
	}
}
