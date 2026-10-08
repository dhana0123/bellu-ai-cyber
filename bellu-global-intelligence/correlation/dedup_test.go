package correlation

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
)

// aliasPool is a small, fixed set of normalized aliases drawn from the CVE/GHSA/
// PYSEC schemes. Keeping the pool small forces generated records to actually
// share aliases, so the alias graph has non-trivial connected components (the
// interesting case for dedup) rather than every record being its own singleton.
var aliasPool = []string{
	"CVE-2024-0001", "CVE-2024-0002", "CVE-2024-0003",
	"GHSA-AAAA-BBBB-CCCC", "GHSA-DDDD-EEEE-FFFF", "GHSA-GGGG-HHHH-IIII",
	"PYSEC-2024-1", "PYSEC-2024-2", "PYSEC-2024-3",
}

func init() {
	// aliasPool must already be normalized so constructed records validate.
	for _, a := range aliasPool {
		if canonical.NormalizeAlias(a) != a {
			panic("aliasPool entry is not normalized: " + a)
		}
	}
}

// genRecord builds one valid CanonicalVuln from a random, non-empty subset of
// aliasPool. The ID is chosen from the record's own aliases so canonical.Validate
// passes (ID present, aliases normalized, non-empty, de-duplicated).
func genRecord(rnd *rand.Rand) canonical.CanonicalVuln {
	// Pick a non-empty subset of the pool.
	perm := rnd.Perm(len(aliasPool))
	n := 1 + rnd.Intn(3) // 1..3 aliases per record keeps components lively but bounded
	if n > len(perm) {
		n = len(perm)
	}
	aliases := make([]string, 0, n)
	for _, idx := range perm[:n] {
		aliases = append(aliases, aliasPool[idx])
	}
	aliases = canonical.DedupeAliases(aliases)
	// ID must be one of the aliases.
	id := aliases[rnd.Intn(len(aliases))]
	return canonical.CanonicalVuln{ID: id, Aliases: aliases}
}

// recordSet is a generatable slice of valid CanonicalVuln records. It implements
// quick.Generator so testing/quick can drive the properties with bounded, valid
// alias graphs.
type recordSet []canonical.CanonicalVuln

// Generate implements quick.Generator, producing a bounded set (0..6) of valid
// records whose aliases are drawn from the shared pool.
func (recordSet) Generate(rnd *rand.Rand, _ int) reflect.Value {
	count := rnd.Intn(7) // 0..6 records; small so the property test stays fast
	recs := make(recordSet, 0, count)
	for i := 0; i < count; i++ {
		recs = append(recs, genRecord(rnd))
	}
	return reflect.ValueOf(recs)
}

// assertAllValid guards the test's own generators: every generated/merged record
// must satisfy canonical.Validate, otherwise the property under test is moot.
func assertAllValid(t *testing.T, recs []canonical.CanonicalVuln, context string) {
	t.Helper()
	for i, r := range recs {
		if err := r.Validate(); err != nil {
			t.Fatalf("%s: record %d (%+v) failed Validate: %v", context, i, r, err)
		}
	}
}

// TestDedupOrderIndependence asserts Property 1 (order-independence): for any
// permutation of the input, Dedup returns the identical slice. The output is
// sorted by canonical id, so reflect.DeepEqual is the right equality.
//
// **Validates: Requirements 5.2**
func TestDedupOrderIndependence(t *testing.T) {
	d := NewDeduper()
	prop := func(recs recordSet) bool {
		in := []canonical.CanonicalVuln(recs)

		got, err := d.Dedup(in)
		if err != nil {
			t.Fatalf("Dedup error: %v", err)
		}

		// Random permutation of the input.
		perm := append([]canonical.CanonicalVuln(nil), in...)
		rand.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

		gotPerm, err := d.Dedup(perm)
		if err != nil {
			t.Fatalf("Dedup(perm) error: %v", err)
		}

		return reflect.DeepEqual(got, gotPerm)
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatalf("order-independence property failed: %v", err)
	}
}

// TestDedupIdempotence asserts Property 1 (idempotence): Dedup(Dedup(x)) deep-equals
// Dedup(x). The merged records are themselves valid canonical records, so feeding
// them back through Dedup must be a fixed point.
//
// **Validates: Requirements 5.3**
func TestDedupIdempotence(t *testing.T) {
	d := NewDeduper()
	prop := func(recs recordSet) bool {
		in := []canonical.CanonicalVuln(recs)

		once, err := d.Dedup(in)
		if err != nil {
			t.Fatalf("Dedup error: %v", err)
		}
		assertAllValid(t, once, "Dedup output")

		twice, err := d.Dedup(once)
		if err != nil {
			t.Fatalf("Dedup(Dedup) error: %v", err)
		}

		return reflect.DeepEqual(once, twice)
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatalf("idempotence property failed: %v", err)
	}
}

// TestDedupConnectedComponents asserts Property 1 (connected components, Req 5.1):
//   - the number of merged records equals the number of connected components of
//     the alias graph (two records linked when they share any normalized alias),
//   - every input alias appears in exactly one output record, and
//   - any two input records that share an alias end up in the same merged record.
//
// The expected component count is computed independently from Dedup, with a plain
// union-find over normalized aliases, so this cross-checks Dedup's own union-find.
//
// **Validates: Requirements 5.1**
func TestDedupConnectedComponents(t *testing.T) {
	d := NewDeduper()
	prop := func(recs recordSet) bool {
		in := []canonical.CanonicalVuln(recs)
		assertAllValid(t, in, "generated input")

		out, err := d.Dedup(in)
		if err != nil {
			t.Fatalf("Dedup error: %v", err)
		}

		// 1) Component count: expected number of components equals len(out).
		wantComponents := countComponents(in)
		if wantComponents != len(out) {
			t.Fatalf("component count mismatch: want %d, got %d output records (in=%+v out=%+v)",
				wantComponents, len(out), in, out)
		}

		// 2) Every input alias appears in exactly one output record.
		aliasToOut := make(map[string]int) // normalized alias -> output index
		for oi, o := range out {
			for _, a := range o.Aliases {
				if prev, ok := aliasToOut[a]; ok {
					t.Fatalf("alias %q appears in output records %d and %d", a, prev, oi)
				}
				aliasToOut[a] = oi
			}
		}
		for _, r := range in {
			for _, a := range canonical.DedupeAliases(r.Aliases) {
				if _, ok := aliasToOut[a]; !ok {
					t.Fatalf("input alias %q is missing from every output record (out=%+v)", a, out)
				}
			}
		}

		// 3) Records sharing an alias were merged: all aliases of any single input
		// record must land in the same output record.
		for _, r := range in {
			na := canonical.DedupeAliases(r.Aliases)
			if len(na) == 0 {
				continue
			}
			target := aliasToOut[na[0]]
			for _, a := range na[1:] {
				if aliasToOut[a] != target {
					t.Fatalf("aliases of one input record landed in different output records: %q->%d vs %q->%d",
						na[0], target, a, aliasToOut[a])
				}
			}
		}

		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatalf("connected-components property failed: %v", err)
	}
}

// countComponents computes the number of connected components of the alias graph
// independently of the Deduper: records are nodes, two records are connected when
// they share any normalized alias. Implemented with a simple union-find over
// records (distinct from UnionFindDeduper's internal one) so it is a genuine
// cross-check rather than a tautology.
func countComponents(in []canonical.CanonicalVuln) int {
	if len(in) == 0 {
		return 0
	}
	parent := make([]int, len(in))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}

	// Link records that share any normalized alias.
	owner := make(map[string]int)
	for i := range in {
		for _, a := range canonical.DedupeAliases(in[i].Aliases) {
			if o, ok := owner[a]; ok {
				union(o, i)
			} else {
				owner[a] = i
			}
		}
	}

	roots := make(map[int]struct{})
	for i := range in {
		roots[find(i)] = struct{}{}
	}
	return len(roots)
}
