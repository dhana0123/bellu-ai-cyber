package runner

import (
	"fmt"
	"strings"

	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/contract"
	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/graphquery"
	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/report"
)

// compare checks the queried graph (nodes, edges) against the scenario
// contract. It returns the lists of missing nodes/edges/paths; PASS is implied
// when all three are empty. Nodes match by Kind, and additionally by Name when
// the contract declares a name. Edges match by relation + endpoint Kinds, with
// the queried confidence required to meet the edge's min_confidence (Decision
// D3 tolerance).
func compare(sc contract.Scenario, nodes []graphquery.Node, edges []graphquery.Edge) (
	missNodes []contract.NodeExpect,
	missEdges []contract.EdgeExpect,
	missPaths []report.AttackPathDiag,
) {
	// Index nodes by canonical id so edge endpoints can be resolved to Kinds,
	// and by kind for membership checks.
	kindByID := make(map[string]string, len(nodes))
	for _, n := range nodes {
		kindByID[n.CanonicalID] = n.Kind
	}

	// Missing nodes: a contract node is present if some queried node shares its
	// Kind and (when declared) its Name (case-insensitive substring, since
	// canonical names carry prefixes/suffixes).
	for _, want := range sc.ExpectedNodes {
		if !nodePresent(want, nodes) {
			missNodes = append(missNodes, want)
		}
	}

	// Missing edges: a contract edge is present if some queried edge has the
	// same relation, endpoints whose Kinds match From/To, and confidence >=
	// min_confidence.
	for _, want := range sc.ExpectedEdges {
		if !edgePresent(want, edges, kindByID) {
			missEdges = append(missEdges, want)
		}
	}

	// Missing attack paths: every edge in the chain must be present; report the
	// first missing edge of each failing path.
	for i, p := range sc.ExpectedAttackPaths {
		for _, we := range p.Edges {
			if !edgePresent(we, edges, kindByID) {
				missPaths = append(missPaths, report.AttackPathDiag{
					Index:       i,
					MissingEdge: we,
					Detail: fmt.Sprintf("attack path %d: missing edge %s %s->%s",
						i, we.Relation, we.From, we.To),
				})
				break
			}
		}
	}
	return missNodes, missEdges, missPaths
}

// nodePresent reports whether any queried node satisfies the expectation.
func nodePresent(want contract.NodeExpect, nodes []graphquery.Node) bool {
	for _, n := range nodes {
		if n.Kind != want.Kind {
			continue
		}
		if want.Name == "" {
			return true
		}
		if nameMatches(n, want.Name) {
			return true
		}
	}
	return false
}

// nameMatches tolerates canonical-id decoration: the declared name matches if it
// is a case-insensitive substring of either the node's Name or its CanonicalID.
func nameMatches(n graphquery.Node, want string) bool {
	w := strings.ToLower(want)
	return strings.Contains(strings.ToLower(n.Name), w) ||
		strings.Contains(strings.ToLower(n.CanonicalID), w)
}

// edgePresent reports whether any queried edge matches the expectation on
// relation, endpoint Kinds (resolved via kindByID), and confidence tolerance.
// From/To are matched as Kinds when they name a model Kind; otherwise they are
// matched as a substring of the endpoint canonical id (e.g. "0.0.0.0/0").
func edgePresent(want contract.EdgeExpect, edges []graphquery.Edge, kindByID map[string]string) bool {
	for _, e := range edges {
		if e.Relation != want.Relation {
			continue
		}
		if e.Confidence < want.MinConfidence {
			continue
		}
		if endpointMatches(want.From, e.SrcID, kindByID) &&
			endpointMatches(want.To, e.DstID, kindByID) {
			return true
		}
	}
	return false
}

// endpointMatches resolves an endpoint reference. If ref equals the endpoint's
// resolved Kind, it matches; otherwise ref is treated as a canonical-id
// substring (so literal references like "0.0.0.0/0" work).
func endpointMatches(ref, canonicalID string, kindByID map[string]string) bool {
	if k, ok := kindByID[canonicalID]; ok && k == ref {
		return true
	}
	return strings.Contains(strings.ToLower(canonicalID), strings.ToLower(ref))
}
