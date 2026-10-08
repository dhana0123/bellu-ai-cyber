package canonical

import "testing"

func validVuln() CanonicalVuln {
	return CanonicalVuln{
		ID:      "CVE-2024-1234",
		Aliases: []string{"CVE-2024-1234", "GHSA-xxxx-yyyy-zzzz"},
		CVSS:    CVSS{Score: 7.5, Version: "3.1"},
		EPSS:    0.42,
		Affected: []AffectedPackage{
			{Purl: "pkg:pypi/requests", Ecosystem: "PyPI", Ranges: []SemverRange{{Introduced: "0", Fixed: "2.31.0"}}},
		},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validVuln().Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateRejectsEmptyID(t *testing.T) {
	v := validVuln()
	v.ID = ""
	if err := v.Validate(); err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestValidateRejectsNoAliases(t *testing.T) {
	v := validVuln()
	v.Aliases = nil
	if err := v.Validate(); err == nil {
		t.Fatal("expected error for empty aliases")
	}
}

func TestValidateRejectsIDNotInAliases(t *testing.T) {
	v := validVuln()
	v.ID = "CVE-9999-0000"
	if err := v.Validate(); err == nil {
		t.Fatal("expected error when ID not among aliases")
	}
}

func TestValidateRejectsUnnormalizedAlias(t *testing.T) {
	v := validVuln()
	v.Aliases = []string{"cve-2024-1234"} // lowercase, not normalized
	v.ID = "cve-2024-1234"
	if err := v.Validate(); err == nil {
		t.Fatal("expected error for unnormalized alias")
	}
}

func TestValidateRejectsDuplicateAlias(t *testing.T) {
	v := validVuln()
	v.Aliases = []string{"CVE-2024-1234", "CVE-2024-1234"}
	if err := v.Validate(); err == nil {
		t.Fatal("expected error for duplicate alias")
	}
}

func TestValidateRejectsBadEPSS(t *testing.T) {
	v := validVuln()
	v.EPSS = 1.5
	if err := v.Validate(); err == nil {
		t.Fatal("expected error for EPSS > 1")
	}
}

func TestValidateRejectsBadCVSS(t *testing.T) {
	v := validVuln()
	v.CVSS.Score = 11
	if err := v.Validate(); err == nil {
		t.Fatal("expected error for CVSS > 10")
	}
}

func TestValidateRejectsPurlWithVersion(t *testing.T) {
	v := validVuln()
	v.Affected[0].Purl = "pkg:pypi/requests@2.30.0"
	if err := v.Validate(); err == nil {
		t.Fatal("expected error for purl with version qualifier")
	}
}

func TestNormalizeAlias(t *testing.T) {
	cases := map[string]string{
		"cve-2024-1234":   "CVE-2024-1234",
		" GHSA-ab-cd-ef ": "GHSA-AB-CD-EF",
		"PYSEC-2024-1":    "PYSEC-2024-1",
	}
	for in, want := range cases {
		if got := NormalizeAlias(in); got != want {
			t.Errorf("NormalizeAlias(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDedupeAliases(t *testing.T) {
	got := DedupeAliases([]string{"CVE-2024-1", "cve-2024-1", "GHSA-x", " "})
	if len(got) != 2 {
		t.Fatalf("expected 2 unique aliases, got %v", got)
	}
}

func TestSemverRangeContains(t *testing.T) {
	cases := []struct {
		rng  SemverRange
		v    string
		want bool
	}{
		{SemverRange{Introduced: "0", Fixed: "2.31.0"}, "2.30.0", true},
		{SemverRange{Introduced: "0", Fixed: "2.31.0"}, "2.31.0", false}, // exclusive upper
		{SemverRange{Introduced: "2.0.0", Fixed: "2.31.0"}, "1.9.9", false},
		{SemverRange{Introduced: "2.0.0", Fixed: ""}, "9.9.9", true},     // unbounded upper
		{SemverRange{Introduced: "", Fixed: "1.0.0"}, "0.9.0", true},     // unbounded lower
		{SemverRange{Introduced: "2.0.0", Fixed: ""}, "2.0.0", true},     // inclusive lower
		{SemverRange{Introduced: "0", Fixed: "1.2.3"}, "1.10.0", false},  // numeric compare (10 > 2)
		{SemverRange{Introduced: "0", Fixed: "1.20.0"}, "1.3.0", true},   // numeric compare
	}
	for _, c := range cases {
		if got := c.rng.Contains(c.v); got != c.want {
			t.Errorf("%+v Contains(%q)=%v want %v", c.rng, c.v, got, c.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("1.10.0", "1.9.0") <= 0 {
		t.Error("1.10.0 should be > 1.9.0 (numeric)")
	}
	if compareVersions("1.0.0", "1.0.0") != 0 {
		t.Error("equal versions")
	}
	if compareVersions("1.0.0", "1.0.0rc1") <= 0 {
		t.Error("release should be > pre-release")
	}
}

// TestSemverRangeContainsBoundaries exercises the half-open [Introduced, Fixed)
// semantics of SemverRange.Contains at and around each edge, for release
// versions only. The introduced bound is inclusive, the fixed bound exclusive;
// empty/"0" introduced opens the lower bound and empty fixed opens the upper
// bound. Exact pre-release ordering is a documented future refinement and is
// deliberately not asserted here.
//
// **Validates: Requirements 8.1, 8.5**
func TestSemverRangeContainsBoundaries(t *testing.T) {
	cases := []struct {
		name string
		rng  SemverRange
		v    string
		want bool
	}{
		// Exact introduced boundary is inclusive.
		{"introduced edge is inclusive", SemverRange{Introduced: "2.0.0", Fixed: "3.0.0"}, "2.0.0", true},
		// Just inside the lower edge.
		{"just above introduced", SemverRange{Introduced: "2.0.0", Fixed: "3.0.0"}, "2.0.1", true},
		// Just below the lower edge is excluded.
		{"just below introduced", SemverRange{Introduced: "2.0.0", Fixed: "3.0.0"}, "1.9.9", false},

		// Exact fixed boundary is exclusive.
		{"fixed edge is exclusive", SemverRange{Introduced: "2.0.0", Fixed: "3.0.0"}, "3.0.0", false},
		// Just below the upper edge is included.
		{"just below fixed", SemverRange{Introduced: "2.0.0", Fixed: "3.0.0"}, "2.9.9", true},
		// Just above the upper edge is excluded.
		{"just above fixed", SemverRange{Introduced: "2.0.0", Fixed: "3.0.0"}, "3.0.1", false},

		// Empty introduced is an open lower bound: everything below fixed matches.
		{"empty introduced matches very low version", SemverRange{Introduced: "", Fixed: "1.0.0"}, "0.0.1", true},
		{"empty introduced still respects fixed", SemverRange{Introduced: "", Fixed: "1.0.0"}, "1.0.0", false},

		// "0" introduced behaves identically to empty (from the start).
		{"zero introduced matches very low version", SemverRange{Introduced: "0", Fixed: "1.0.0"}, "0.0.1", true},
		{"zero introduced at fixed edge excluded", SemverRange{Introduced: "0", Fixed: "1.0.0"}, "1.0.0", false},

		// Empty fixed is an open upper bound: everything at/above introduced matches.
		{"empty fixed matches high version", SemverRange{Introduced: "2.0.0", Fixed: ""}, "99.0.0", true},
		{"empty fixed still respects introduced", SemverRange{Introduced: "2.0.0", Fixed: ""}, "1.9.9", false},
		{"empty fixed at introduced edge inclusive", SemverRange{Introduced: "2.0.0", Fixed: ""}, "2.0.0", true},

		// Fully open range matches any non-empty release version.
		{"fully open matches", SemverRange{Introduced: "", Fixed: ""}, "1.2.3", true},
		{"fully open (zero) matches", SemverRange{Introduced: "0", Fixed: ""}, "0.0.0", true},

		// Empty version never matches, regardless of bounds.
		{"empty version never matches", SemverRange{Introduced: "", Fixed: ""}, "", false},
		{"blank version never matches", SemverRange{Introduced: "", Fixed: ""}, "   ", false},

		// Numeric (not lexical) component comparison at the edges.
		{"numeric compare below fixed", SemverRange{Introduced: "1.0.0", Fixed: "1.10.0"}, "1.9.0", true},
		{"numeric compare at fixed", SemverRange{Introduced: "1.0.0", Fixed: "1.10.0"}, "1.10.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rng.Contains(c.v); got != c.want {
				t.Errorf("%+v Contains(%q)=%v want %v", c.rng, c.v, got, c.want)
			}
		})
	}
}
