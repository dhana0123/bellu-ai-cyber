package search

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
)

// FromCanonical projects a canonical vulnerability into the index document
// shape. It flattens affected purls for fast package lookups and preserves the
// per-package half-open ranges for version lookups. The projection is a pure
// function of its input.
func FromCanonical(v canonical.CanonicalVuln) Document {
	doc := Document{
		ID:             v.ID,
		Aliases:        append([]string(nil), v.Aliases...),
		Text:           v.Summary,
		Severity:       string(v.Severity),
		KnownExploited: v.KnownExploited,
		EPSS:           v.EPSS,
		CVSS:           v.CVSS.Score,
		Sources:        append([]string(nil), v.Sources...),
	}

	seenPurl := make(map[string]struct{}, len(v.Affected))
	for _, ap := range v.Affected {
		purl := strings.TrimSpace(ap.Purl)
		if purl == "" {
			continue
		}
		ranges := make([]AffectedRange, 0, len(ap.Ranges))
		for _, r := range ap.Ranges {
			ranges = append(ranges, AffectedRange{Introduced: r.Introduced, Fixed: r.Fixed})
		}
		doc.Affected = append(doc.Affected, AffectedPackage{
			Purl:      purl,
			Ecosystem: ap.Ecosystem,
			Ranges:    ranges,
		})
		if _, ok := seenPurl[purl]; !ok {
			seenPurl[purl] = struct{}{}
			doc.Purls = append(doc.Purls, purl)
		}
	}
	return doc
}

// FromCanonicalMany projects a slice of canonical vulns.
func FromCanonicalMany(vulns []canonical.CanonicalVuln) []Document {
	out := make([]Document, 0, len(vulns))
	for _, v := range vulns {
		out = append(out, FromCanonical(v))
	}
	return out
}

// matchesVersion reports whether the document is affected at purl@version:
// it has an affected package for purl with at least one range containing the
// version, using the canonical half-open [introduced, fixed) semantics via the
// shared, authoritative comparator in the canonical package. This is applied as
// a post-filter on candidate documents returned by the purl term query, so the
// index never has to encode version-range logic itself.
func matchesVersion(doc Document, purl, version string) bool {
	purl = strings.TrimSpace(purl)
	version = strings.TrimSpace(version)
	if purl == "" || version == "" {
		return false
	}
	for _, ap := range doc.Affected {
		if ap.Purl != purl {
			continue
		}
		if len(ap.Ranges) == 0 {
			// No ranges declared: the whole package is affected (mirrors the
			// matcher's MatchPackage convention).
			return true
		}
		for _, r := range ap.Ranges {
			if (canonical.SemverRange{Introduced: r.Introduced, Fixed: r.Fixed}).Contains(version) {
				return true
			}
		}
	}
	return false
}
