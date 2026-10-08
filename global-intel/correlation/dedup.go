// Package correlation performs intel-plane identifier linking and
// deduplication. Its first job is dedup: merging canonical vulnerabilities that
// share any alias into one canonical record using an identifier graph
// (union-find over the alias sets), mirroring the deterministic alias merging
// the customer-side resolver already does for graph entities.
package correlation

import (
	"sort"
	"strings"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
)

// Deduper merges canonical vulnerabilities that share any alias into one
// record. The merge is a deterministic, order-independent function of the
// input alias sets (connected components of the identifier graph).
type Deduper interface {
	Dedup(in []canonical.CanonicalVuln) ([]canonical.CanonicalVuln, error)
}

// UnionFindDeduper is the default Deduper. It groups input records into the
// connected components of the alias identifier graph (two records are linked
// when they share any normalized alias) using union-find, then merges each
// component into a single CanonicalVuln with a deterministically selected
// canonical id and deterministically merged fields.
//
// The result is order-independent (Property 1): for any permutation of the
// input, Dedup returns the same set of merged vulnerabilities (identical slice,
// since the output is sorted by canonical id). It is also idempotent:
// Dedup(Dedup(x)) == Dedup(x).
type UnionFindDeduper struct{}

// NewDeduper returns the default union-find Deduper.
func NewDeduper() UnionFindDeduper { return UnionFindDeduper{} }

// Dedup merges records sharing any alias into one CanonicalVuln per connected
// component. See UnionFindDeduper for the determinism guarantees.
func (UnionFindDeduper) Dedup(in []canonical.CanonicalVuln) ([]canonical.CanonicalVuln, error) {
	if len(in) == 0 {
		return nil, nil
	}

	uf := newUnionFind(len(in))

	// Map each normalized alias to the index of the first record that carried
	// it; union subsequent records that share the alias. Iterating in input
	// order keeps the union structure deterministic, and union-by-root makes
	// the resulting components independent of input order.
	aliasOwner := make(map[string]int)
	for i := range in {
		for _, a := range canonical.DedupeAliases(in[i].Aliases) {
			if owner, ok := aliasOwner[a]; ok {
				uf.union(owner, i)
			} else {
				aliasOwner[a] = i
			}
		}
	}

	// Collect component members keyed by canonical component root.
	components := make(map[int][]int)
	for i := range in {
		root := uf.find(i)
		components[root] = append(components[root], i)
	}

	// Produce a merged record per component. Keys are iterated via a sorted
	// slice so the construction order is deterministic (output is re-sorted by
	// id below regardless, but we avoid any map-iteration-into-slice ordering).
	roots := make([]int, 0, len(components))
	for root := range components {
		roots = append(roots, root)
	}
	sort.Ints(roots)

	out := make([]canonical.CanonicalVuln, 0, len(roots))
	for _, root := range roots {
		members := components[root]
		sort.Ints(members)
		recs := make([]canonical.CanonicalVuln, 0, len(members))
		for _, idx := range members {
			recs = append(recs, in[idx])
		}
		out = append(out, mergeComponent(recs))
	}

	// Final deterministic ordering by canonical id.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// mergeComponent folds a set of records that belong to the same connected
// component into a single CanonicalVuln. Every field is combined
// deterministically so the result depends only on the set of records, not
// their order.
func mergeComponent(recs []canonical.CanonicalVuln) canonical.CanonicalVuln {
	// Union + normalize + sort all aliases.
	var allAliases []string
	for _, r := range recs {
		allAliases = append(allAliases, r.Aliases...)
	}
	aliases := canonical.DedupeAliases(allAliases)
	sort.Strings(aliases)

	merged := canonical.CanonicalVuln{
		ID:      selectCanonicalID(aliases),
		Aliases: aliases,
	}

	// Summary: prefer the non-empty summary from the record whose id sorts
	// first, for a deterministic tie-break. Fall back to any non-empty summary
	// in id order so a merge never loses an available summary.
	byID := append([]canonical.CanonicalVuln(nil), recs...)
	sort.Slice(byID, func(i, j int) bool { return byID[i].ID < byID[j].ID })
	for _, r := range byID {
		if strings.TrimSpace(r.Summary) != "" {
			merged.Summary = r.Summary
			break
		}
	}

	// KnownExploited: OR across the component (if any source flags KEV, it's
	// exploited).
	// EPSS: take the max probability.
	// CVSS: take the entry with the highest score (deterministic tie-break by
	// vector then version).
	// Severity: take the highest severity.
	// Published: earliest non-zero. Modified: latest.
	var bestCVSS canonical.CVSS
	haveCVSS := false
	for _, r := range byID {
		if r.KnownExploited {
			merged.KnownExploited = true
		}
		if r.EPSS > merged.EPSS {
			merged.EPSS = r.EPSS
		}
		if !haveCVSS || cvssLess(bestCVSS, r.CVSS) {
			bestCVSS = r.CVSS
			haveCVSS = true
		}
		if severityRank(r.Severity) > severityRank(merged.Severity) {
			merged.Severity = r.Severity
		}
		if !r.Published.IsZero() {
			if merged.Published.IsZero() || r.Published.Before(merged.Published) {
				merged.Published = r.Published
			}
		}
		if r.Modified.After(merged.Modified) {
			merged.Modified = r.Modified
		}
	}
	merged.CVSS = bestCVSS

	// Sources: union + sorted.
	var allSources []string
	for _, r := range recs {
		allSources = append(allSources, r.Sources...)
	}
	merged.Sources = unionSortedStrings(allSources)

	// Affected: union, deduped, and sorted deterministically.
	merged.Affected = mergeAffected(recs)

	return merged
}

// selectCanonicalID chooses the canonical id from a (sorted, normalized) alias
// set: prefer a CVE (lowest-sorting), else a GHSA (lowest-sorting), else the
// lowest-sorting remaining alias. The chosen id is guaranteed to be present in
// aliases so canonical.Validate passes. aliases must be non-empty.
func selectCanonicalID(aliases []string) string {
	if cve := firstWithPrefix(aliases, "CVE-"); cve != "" {
		return cve
	}
	if ghsa := firstWithPrefix(aliases, "GHSA-"); ghsa != "" {
		return ghsa
	}
	// aliases is sorted; the lowest-sorting entry is the first.
	return aliases[0]
}

// firstWithPrefix returns the lowest-sorting alias with the given prefix, or ""
// if none. aliases is assumed already sorted ascending.
func firstWithPrefix(aliases []string, prefix string) string {
	for _, a := range aliases {
		if strings.HasPrefix(a, prefix) {
			return a
		}
	}
	return ""
}

// cvssLess reports whether CVSS a ranks below b: primarily by score, then by
// vector string, then by version, so the "best" CVSS is deterministic even
// when scores tie.
func cvssLess(a, b canonical.CVSS) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	if a.Vector != b.Vector {
		return a.Vector < b.Vector
	}
	return a.Version < b.Version
}

// severityRank orders severities so the highest can be selected. Unknown/empty
// values rank below Info.
func severityRank(s canonical.Severity) int {
	switch s {
	case canonical.SevCritical:
		return 5
	case canonical.SevHigh:
		return 4
	case canonical.SevMedium:
		return 3
	case canonical.SevLow:
		return 2
	case canonical.SevInfo:
		return 1
	default:
		return 0
	}
}

// unionSortedStrings drops empty/whitespace entries, de-duplicates the input,
// and returns the result sorted ascending.
func unionSortedStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		seen[s] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// mergeAffected unions the affected-package entries across records, de-duplicating
// identical (purl, ecosystem, range) tuples and fixed-in versions, and returns
// the result sorted deterministically.
func mergeAffected(recs []canonical.CanonicalVuln) []canonical.AffectedPackage {
	// Group ranges and fixed-in versions by (purl, ecosystem).
	type key struct{ purl, eco string }
	rangeSet := make(map[key]map[canonical.SemverRange]struct{})
	fixedSet := make(map[key]map[string]struct{})
	order := make(map[key]struct{})

	for _, r := range recs {
		for _, ap := range r.Affected {
			k := key{purl: ap.Purl, eco: ap.Ecosystem}
			order[k] = struct{}{}
			if rangeSet[k] == nil {
				rangeSet[k] = make(map[canonical.SemverRange]struct{})
			}
			for _, rng := range ap.Ranges {
				rangeSet[k][rng] = struct{}{}
			}
			if fixedSet[k] == nil {
				fixedSet[k] = make(map[string]struct{})
			}
			for _, f := range ap.FixedIn {
				if strings.TrimSpace(f) != "" {
					fixedSet[k][f] = struct{}{}
				}
			}
		}
	}

	if len(order) == 0 {
		return nil
	}

	keys := make([]key, 0, len(order))
	for k := range order {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].purl != keys[j].purl {
			return keys[i].purl < keys[j].purl
		}
		return keys[i].eco < keys[j].eco
	})

	out := make([]canonical.AffectedPackage, 0, len(keys))
	for _, k := range keys {
		ranges := make([]canonical.SemverRange, 0, len(rangeSet[k]))
		for rng := range rangeSet[k] {
			ranges = append(ranges, rng)
		}
		sort.Slice(ranges, func(i, j int) bool {
			if ranges[i].Introduced != ranges[j].Introduced {
				return ranges[i].Introduced < ranges[j].Introduced
			}
			return ranges[i].Fixed < ranges[j].Fixed
		})

		fixed := make([]string, 0, len(fixedSet[k]))
		for f := range fixedSet[k] {
			fixed = append(fixed, f)
		}
		sort.Strings(fixed)

		ap := canonical.AffectedPackage{
			Purl:      k.purl,
			Ecosystem: k.eco,
		}
		if len(ranges) > 0 {
			ap.Ranges = ranges
		}
		if len(fixed) > 0 {
			ap.FixedIn = fixed
		}
		out = append(out, ap)
	}
	return out
}

// unionFind is a classic disjoint-set structure with path compression and
// union-by-size, used to compute connected components of the alias graph.
type unionFind struct {
	parent []int
	size   []int
}

func newUnionFind(n int) *unionFind {
	uf := &unionFind{parent: make([]int, n), size: make([]int, n)}
	for i := range uf.parent {
		uf.parent[i] = i
		uf.size[i] = 1
	}
	return uf
}

func (uf *unionFind) find(x int) int {
	for uf.parent[x] != x {
		uf.parent[x] = uf.parent[uf.parent[x]] // path compression
		x = uf.parent[x]
	}
	return x
}

// union merges the sets containing a and b. The smaller set is attached under
// the larger one, with the smaller index winning ties, so the component
// structure is independent of the order in which unions are applied.
func (uf *unionFind) union(a, b int) {
	ra, rb := uf.find(a), uf.find(b)
	if ra == rb {
		return
	}
	if uf.size[ra] < uf.size[rb] {
		ra, rb = rb, ra
	} else if uf.size[ra] == uf.size[rb] && ra > rb {
		ra, rb = rb, ra
	}
	uf.parent[rb] = ra
	uf.size[ra] += uf.size[rb]
}
