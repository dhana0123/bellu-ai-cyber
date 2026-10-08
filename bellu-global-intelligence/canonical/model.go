// Package canonical defines Bellu's provider-independent intelligence model:
// the canonical vulnerability and its supporting value types, plus the
// validation and alias-normalization rules every source must satisfy before a
// record is persisted into the intel_* tables.
package canonical

import (
	"fmt"
	"strings"
	"time"
)

// CanonicalVuln is Bellu's provider-independent view of one vulnerability,
// merged from every source that references it.
type CanonicalVuln struct {
	// ID is the stable canonical id, derived deterministically from the
	// preferred alias (CVE if present, else GHSA, else OSV id).
	ID string
	// Aliases are all external identifiers that resolve to this vuln,
	// e.g. ["CVE-2024-1234", "GHSA-xxxx", "PYSEC-2024-1"].
	Aliases []string
	Summary string
	// Affected package ranges: purl ecosystem + version ranges.
	Affected []AffectedPackage
	CVSS     CVSS
	Severity Severity
	// KnownExploited is true if present in CISA KEV.
	KnownExploited bool
	// EPSS is the latest exploit-prediction score in [0,1].
	EPSS      float64
	Published time.Time
	Modified  time.Time
	// Sources lists which intel sources contributed, for provenance.
	Sources []string
}

// AffectedPackage names a vulnerable package (by purl, without a version
// qualifier) and the version ranges in which it is affected.
type AffectedPackage struct {
	Purl      string // "pkg:pypi/requests" (name without version)
	Ecosystem string // "PyPI", "npm", "Go", ...
	Ranges    []SemverRange
	FixedIn   []string // explicit fixed versions when provided
}

// SemverRange is a half-open version interval [Introduced, Fixed).
// Introduced is the inclusive lower bound ("0" or "" means from the start);
// Fixed is the exclusive upper bound ("" means unbounded above).
type SemverRange struct {
	Introduced string
	Fixed      string
}

// CVSS captures a Common Vulnerability Scoring System result.
type CVSS struct {
	Vector  string
	Score   float64 // 0..10
	Version string  // "3.1", "4.0"
}

// Severity is the qualitative severity bucket for a vulnerability.
type Severity string

const (
	SevCritical Severity = "critical"
	SevHigh     Severity = "high"
	SevMedium   Severity = "medium"
	SevLow      Severity = "low"
	SevInfo     Severity = "info"
)

// NormalizeAlias canonicalizes an external identifier so aliases compare
// consistently across sources: it trims surrounding whitespace and uppercases
// the identifier (CVE, GHSA, PYSEC, OSV ids, etc. are all upper-cased forms).
func NormalizeAlias(a string) string {
	return strings.ToUpper(strings.TrimSpace(a))
}

// DedupeAliases normalizes every alias, drops empty/whitespace-only entries,
// and removes duplicates while preserving first-seen order. The result is the
// de-duplicated, normalized alias set.
func DedupeAliases(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, a := range in {
		n := NormalizeAlias(a)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// aliasScheme returns the identifier scheme prefix (the portion before the
// first '-'), e.g. "CVE" for "CVE-2024-1234" or "GHSA" for
// "GHSA-xxxx-yyyy-zzzz". When there is no '-', the whole alias is the scheme.
func aliasScheme(a string) string {
	if i := strings.IndexByte(a, '-'); i >= 0 {
		return a[:i]
	}
	return a
}

// Validate enforces the canonical-model invariants:
//   - ID is non-empty and present among Aliases.
//   - Aliases is non-empty, with every alias normalized (NormalizeAlias is
//     idempotent on it) and de-duplicated.
//   - EPSS in [0,1]; CVSS.Score in [0,10].
//   - Every AffectedPackage.Purl is non-empty and carries no version qualifier
//     (no '@'); the version lives in Ranges.
func (v CanonicalVuln) Validate() error {
	if strings.TrimSpace(v.ID) == "" {
		return fmt.Errorf("canonical: ID is empty")
	}
	if len(v.Aliases) == 0 {
		return fmt.Errorf("canonical: aliases is empty")
	}

	seen := make(map[string]struct{}, len(v.Aliases))
	idFound := false
	for _, a := range v.Aliases {
		if a == "" {
			return fmt.Errorf("canonical: empty alias")
		}
		if a != strings.TrimSpace(a) {
			return fmt.Errorf("canonical: alias %q is not trimmed", a)
		}
		if scheme := aliasScheme(a); scheme != strings.ToUpper(scheme) {
			return fmt.Errorf("canonical: alias %q has a non-normalized scheme", a)
		}
		if _, ok := seen[a]; ok {
			return fmt.Errorf("canonical: duplicate alias %q", a)
		}
		seen[a] = struct{}{}
		if a == v.ID {
			idFound = true
		}
	}
	if !idFound {
		return fmt.Errorf("canonical: ID %q is not among aliases", v.ID)
	}

	if v.EPSS < 0 || v.EPSS > 1 {
		return fmt.Errorf("canonical: EPSS %v out of range [0,1]", v.EPSS)
	}
	if v.CVSS.Score < 0 || v.CVSS.Score > 10 {
		return fmt.Errorf("canonical: CVSS score %v out of range [0,10]", v.CVSS.Score)
	}

	for i, ap := range v.Affected {
		if strings.TrimSpace(ap.Purl) == "" {
			return fmt.Errorf("canonical: affected[%d] has empty purl", i)
		}
		if strings.Contains(ap.Purl, "@") {
			return fmt.Errorf("canonical: affected[%d] purl %q carries a version qualifier", i, ap.Purl)
		}
	}

	return nil
}
