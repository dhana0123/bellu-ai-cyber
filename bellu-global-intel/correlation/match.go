package correlation

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
)

// This file holds the pure matching helpers that bridge canonical intelligence
// to customer graph assets. They operate only on plain inputs (no graph or DB
// handles) so they are unit-testable in isolation and reusable by the
// Intelligence Matcher (internal/intelcorrelate, task 14.2) which supplies the
// graph-read values.
//
// Every helper is deterministic: identical inputs always yield identical
// results, with no reliance on map iteration order or wall-clock time. Version
// containment reuses canonical.SemverRange.Contains (introduced <= v < fixed)
// so there is exactly one implementation of version comparison in the codebase.

// SplitPurl splits a package URL into its name (the purl without any version
// qualifier) and its version. Graph Package nodes are keyed by a versioned purl
// such as "pkg:pypi/requests@2.30.0"; canonical AffectedPackage.Purl is the
// name only ("pkg:pypi/requests"), with the version carried in its ranges. This
// helper reconciles the two forms.
//
// The version is the substring after the final '@'. If the purl carries no '@'
// the whole string is the name and the version is empty. Surrounding
// whitespace is trimmed from both results.
//
// Examples:
//
//	SplitPurl("pkg:pypi/requests@2.30.0") => "pkg:pypi/requests", "2.30.0"
//	SplitPurl("pkg:pypi/requests")        => "pkg:pypi/requests", ""
func SplitPurl(purl string) (name, version string) {
	purl = strings.TrimSpace(purl)
	if i := strings.LastIndexByte(purl, '@'); i >= 0 {
		name = strings.TrimSpace(purl[:i])
		version = strings.TrimSpace(purl[i+1:])
		return name, version
	}
	return purl, ""
}

// MatchPackage reports whether a graph Package (identified by its purl name and
// version) is affected by any of the given canonical AffectedPackage entries,
// and returns the matched entry.
//
// A package matches an AffectedPackage only when the purl names are equal AND
// one of the following holds:
//
//   - the AffectedPackage declares one or more SemverRanges and the package
//     version lies within at least one of them, tested with
//     canonical.SemverRange.Contains (introduced <= v < fixed). A version that
//     falls outside every declared range does NOT match (Requirement 8.5).
//   - the AffectedPackage declares NO ranges at all, in which case the whole
//     package is treated as affected and the purl-name match alone is
//     sufficient. This is the conservative, deterministic reading for an
//     "all versions" advisory: ranges are the mechanism by which a source
//     narrows the affected set, so their absence means the source did not
//     narrow it. (Requirement 8.5 constrains only the case where ranges exist
//     and none contain the version.)
//
// Entries are evaluated in slice order and the first match wins, so the result
// is deterministic for a given input. When nothing matches, matched is false
// and the returned AffectedPackage is the zero value.
//
// pkgPurlName must be the purl name (no version qualifier); use SplitPurl to
// derive it from a versioned graph purl.
func MatchPackage(vulnAffected []canonical.AffectedPackage, pkgPurlName, pkgVersion string) (matched bool, ap canonical.AffectedPackage) {
	name := strings.TrimSpace(pkgPurlName)
	version := strings.TrimSpace(pkgVersion)
	for _, cand := range vulnAffected {
		if strings.TrimSpace(cand.Purl) != name {
			continue
		}
		if len(cand.Ranges) == 0 {
			// No ranges declared: the whole package is affected.
			return true, cand
		}
		for _, rng := range cand.Ranges {
			if rng.Contains(version) {
				return true, cand
			}
		}
		// Purl name matched but the version fell outside every declared
		// range: per Requirement 8.5 this is NOT a match. Keep scanning in
		// case another entry for the same purl covers this version.
	}
	return false, canonical.AffectedPackage{}
}

// MatchesPackage is the boolean-only convenience form of MatchPackage for
// callers that do not need the matched AffectedPackage.
func MatchesPackage(vulnAffected []canonical.AffectedPackage, pkgPurlName, pkgVersion string) bool {
	ok, _ := MatchPackage(vulnAffected, pkgPurlName, pkgVersion)
	return ok
}

// MatchContainerImageDigest reports whether a canonical vulnerability's intel
// digest matches a graph ContainerImage digest. Digests are compared after
// trimming surrounding whitespace and lower-casing, so "sha256:ABC" and
// "sha256:abc" are the same image; an empty digest on either side never
// matches. ContainerImage nodes are keyed by digest in the graph, so digest
// equality is the primary image-level correlation basis (Requirement 8.2).
func MatchContainerImageDigest(vulnDigest, imageDigest string) bool {
	a := strings.ToLower(strings.TrimSpace(vulnDigest))
	b := strings.ToLower(strings.TrimSpace(imageDigest))
	if a == "" || b == "" {
		return false
	}
	return a == b
}

// MatchContainerImagePackages reports whether any package embedded in a
// container image is affected by the given canonical AffectedPackage set, and
// returns the first matching embedded package together with its matched
// AffectedPackage. Each embedded package is a versioned purl (e.g.
// "pkg:pypi/requests@2.30.0"); it is split with SplitPurl and tested with
// MatchPackage, so the same (introduced <= v < fixed) containment and
// Requirement 8.5 "outside every range does not match" rules apply (Req 8.2).
//
// Packages are evaluated in slice order and the first match wins, so the result
// is deterministic. When nothing matches, matched is false and both returned
// strings/structs are their zero values.
func MatchContainerImagePackages(vulnAffected []canonical.AffectedPackage, imagePackagePurls []string) (matched bool, matchedPurl string, ap canonical.AffectedPackage) {
	for _, p := range imagePackagePurls {
		name, version := SplitPurl(p)
		if name == "" {
			continue
		}
		if ok, cand := MatchPackage(vulnAffected, name, version); ok {
			return true, strings.TrimSpace(p), cand
		}
	}
	return false, "", canonical.AffectedPackage{}
}

// MatchAIModel reports whether an AI-specific intel model identifier matches a
// graph AIModel / AIAgent identifier by case-insensitive string equality after
// trimming whitespace. Empty on either side never matches. AI intel carries a
// model identifier string and AIModel/AIAgent nodes carry the same identifier,
// so equality is the correlation basis for AI model assets (Requirement 8.3).
func MatchAIModel(vulnModelID, assetModelID string) bool {
	return equalFoldIdentifier(vulnModelID, assetModelID)
}

// MatchMCPServer reports whether an AI-specific intel MCP-server identifier
// matches a graph MCPServer identifier by case-insensitive string equality
// after trimming whitespace. Empty on either side never matches. (Req 8.3)
func MatchMCPServer(vulnMCPServerID, assetMCPServerID string) bool {
	return equalFoldIdentifier(vulnMCPServerID, assetMCPServerID)
}

// equalFoldIdentifier compares two identifier strings for equality after
// trimming whitespace and folding case. Empty identifiers never match, so a
// vuln with no AI identifier never spuriously correlates to an asset.
func equalFoldIdentifier(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(a, b)
}
