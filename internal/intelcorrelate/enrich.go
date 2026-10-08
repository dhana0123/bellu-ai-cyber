// This file implements task 14.3: enriching the customer graph with the
// intelligence a Candidate carries, through the allow-listed write path only.
//
// The matcher (matcher.go) emits Candidates; Enrich turns each Candidate plus
// its canonical vuln data into a Vulnerability node and a HAS_VULN edge, written
// EXCLUSIVELY through graph.Writer.SyncEntities / SyncEdges (both of which
// validate kind/relation against the model allow-list). Enrichment is a *source
// of attributes*, never a source of graph existence:
//
//   - It never writes a row to the Postgres evidence table (which references
//     raw_events). EvidenceIDs on the entity and edge are left EMPTY — the
//     enrichment invents no evidence ids and no raw_event ids.
//   - It cites the canonical intel record id (the vuln id) in the node/edge
//     `intel_ref` attribute, NEVER a raw_event id.
//   - It annotates only assets a prior customer raw_event already justified: the
//     HAS_VULN edge's MATCH on the source asset in SyncEdges silently no-ops when
//     the asset node does not already exist, so no edge is fabricated for an
//     unknown asset.
//
// Enrich is deterministic and idempotent: it dedupes nodes/edges by canonical
// id and MERGEs through the writer, so re-running over the same Candidates +
// intel yields the same graph state.
package intelcorrelate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// GraphWriter is the write slice of graph.Writer the enrichment needs. The
// concrete *graph.Writer satisfies it via SyncEntities / SyncEdges — the only
// allow-listed graph write paths — and tests can supply a fake without a live
// Memgraph. It is intentionally separate from the read-only GraphReader the
// matcher uses.
type GraphWriter interface {
	SyncEntities(ctx context.Context, entities []store.GraphEntity) (int, error)
	SyncEdges(ctx context.Context, edges []store.GraphEdge) (int, error)
}

// EnrichResult reports how many Vulnerability nodes and HAS_VULN edges were
// synced.
type EnrichResult struct {
	Nodes int
	Edges int
}

// Enrich upserts, for each Candidate, a Vulnerability node and a HAS_VULN edge
// carrying the candidate's intel attributes, through the allow-listed writer.
//
// vulns maps a candidate's VulnID to its canonical record so node/edge
// attributes (severity, cvss, epss, known_exploited) can be filled. A candidate
// whose VulnID is absent from vulns is enriched with only the citation
// (intel_ref) and whatever attributes default to — it is never skipped, since
// the Candidate itself already cites a real intel record.
//
// The vuln node is keyed by the same canonical convention the resolver uses for
// Vulnerability entities (`vuln:<ID>`), so enrichment MERGEs onto the exact node
// an SBOM-derived HAS_VULN already points at rather than creating a parallel
// node. EvidenceIDs are left empty on both the node and the edge: enrichment
// writes no evidence and cites the intel record id, never a raw_event id.
func Enrich(ctx context.Context, w GraphWriter, cands []Candidate, vulns map[string]canonical.CanonicalVuln) (EnrichResult, error) {
	// Dedupe nodes by canonical id and edges by (src, dst) so repeated
	// candidates for the same (asset, vuln) collapse to one deterministic write.
	nodeByID := map[string]store.GraphEntity{}
	edgeByKey := map[string]store.GraphEdge{}
	var nodeOrder, edgeOrder []string

	for _, c := range cands {
		if c.VulnID == "" || c.AssetID == "" {
			continue
		}
		vulnCID := vulnCanonicalID(c.VulnID)
		v, hasVuln := vulns[c.VulnID]

		attrs := vulnAttributes(c, v, hasVuln)

		if _, ok := nodeByID[vulnCID]; !ok {
			nodeByID[vulnCID] = store.GraphEntity{
				CanonicalID: vulnCID,
				Kind:        model.KindVulnerability,
				Provider:    "intel",
				Name:        c.VulnID,
				Attributes:  attrs,
				// No evidence: enrichment never writes to the evidence table.
				EvidenceIDs: nil,
				Sources:     nil,
			}
			nodeOrder = append(nodeOrder, vulnCID)
		}

		edgeKey := c.AssetID + "\x00" + vulnCID
		if _, ok := edgeByKey[edgeKey]; !ok {
			edgeByKey[edgeKey] = store.GraphEdge{
				SrcID:      c.AssetID,
				Relation:   model.RelHasVuln,
				DstID:      vulnCID,
				Attributes: attrs,
				// Confidence left at zero so SyncEdges keeps any stronger value
				// a prior evidence-backed HAS_VULN already set.
				Confidence: 0,
				// No evidence: enrichment annotates an existing asset, it does
				// not justify the edge's existence with a raw_event.
				EvidenceIDs: nil,
				Sources:     nil,
			}
			edgeOrder = append(edgeOrder, edgeKey)
		}
	}

	// Build deterministic slices (sorted keys) so re-runs issue identical writes.
	sort.Strings(nodeOrder)
	sort.Strings(edgeOrder)

	entities := make([]store.GraphEntity, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		entities = append(entities, nodeByID[id])
	}
	edges := make([]store.GraphEdge, 0, len(edgeOrder))
	for _, k := range edgeOrder {
		edges = append(edges, edgeByKey[k])
	}

	var res EnrichResult
	if len(entities) > 0 {
		n, err := w.SyncEntities(ctx, entities)
		if err != nil {
			return res, fmt.Errorf("intelcorrelate: sync vulnerability nodes: %w", err)
		}
		res.Nodes = n
	}
	if len(edges) > 0 {
		n, err := w.SyncEdges(ctx, edges)
		if err != nil {
			return res, fmt.Errorf("intelcorrelate: sync HAS_VULN edges: %w", err)
		}
		res.Edges = n
	}
	return res, nil
}

// vulnAttributes builds the intel attribute map carried on both the
// Vulnerability node and the HAS_VULN edge. intel_ref cites the candidate's
// IntelRef (the canonical intel record id) — never a raw_event id. When the
// candidate's canonical vuln is known, severity/cvss/epss/known_exploited are
// filled from it; otherwise only the citation is carried.
func vulnAttributes(c Candidate, v canonical.CanonicalVuln, hasVuln bool) map[string]any {
	// intel_ref cites the intel record id. Prefer the candidate's explicit
	// IntelRef; fall back to the vuln id it points at. Both are intel ids.
	intelRef := c.IntelRef
	if intelRef == "" {
		intelRef = c.VulnID
	}
	attrs := map[string]any{
		"intel_ref": intelRef,
	}
	if hasVuln {
		attrs["severity"] = string(v.Severity)
		attrs["cvss"] = v.CVSS.Score
		attrs["epss"] = v.EPSS
		attrs["known_exploited"] = v.KnownExploited
	}
	return attrs
}

// vulnCanonicalID maps a canonical intel vuln id to the graph canonical id used
// for Vulnerability nodes. The resolver keys Vulnerability entities as
// `vuln:<ID>` (stripping the normalizer's `cve:` native-key prefix); enrichment
// matches that exactly so it MERGEs onto the same node an SBOM HAS_VULN already
// references rather than creating a parallel one. An id already in `vuln:` form
// is returned unchanged.
func vulnCanonicalID(vulnID string) string {
	id := strings.TrimSpace(vulnID)
	if strings.HasPrefix(id, "vuln:") {
		return id
	}
	id = strings.TrimPrefix(id, "cve:")
	return "vuln:" + id
}
