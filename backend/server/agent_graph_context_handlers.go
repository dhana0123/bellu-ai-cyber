package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
)

// This file implements seam 1 of the agents plane: GET /api/agent/graph-context
// (task 2.1). It builds a BOUNDED scoped subgraph payload for a scan target and
// returns it to the Python scan runner, so the runner can inject initial graph
// context into the root agent without the payload growing with graph size.
//
// It follows the existing backend/server conventions: the tenant org is
// resolved only from the authenticated principal (the mcpOrg pattern), the read
// runs under a bounded 15s context, and the handler degrades gracefully when
// the Memgraph projection is unavailable (s.graph == nil), mirroring the MCP
// read tools and the graph:false health behavior.
//
// The agent-plane wire types (agentGraphContext / agentTargetRef /
// agentGraphNode / agentGraphEdge) and parseAgentTargetRef are defined here,
// prefixed to avoid colliding with sibling agent handlers added in parallel.

const (
	// maxContextNodes is the hard cap on neighbor nodes in a graph-context
	// payload. It bounds the response so its size never scales with total graph
	// size (design Property 6, Req 4.3).
	maxContextNodes = 200
	// maxContextDepth is the hard cap on traversal depth from the seed entity
	// (design Property 6, Req 4.3).
	maxContextDepth = 2
)

// agentGraphContext is the initial scoped subgraph handed to a run. It is
// bounded by construction: MaxNodes/MaxDepth are enforced server-side so the
// payload size cannot grow with graph size (Req 4.3, 4.5). The field tags match
// the design's GraphContext data model so the Python graph_context provider can
// decode it directly.
type agentGraphContext struct {
	Target      agentTargetRef   `json:"target"`             // entity or raw target
	Seed        *agentGraphNode  `json:"seed,omitempty"`     // nil for raw targets / missing seed
	Neighbors   []agentGraphNode `json:"neighbors"`          // capped at maxContextNodes
	Edges       []agentGraphEdge `json:"edges"`
	Bounded     bool             `json:"bounded"`            // always true (Req 4.5)
	GraphOnline bool             `json:"graph"`              // false when Memgraph is nil (Req 4.7)
	Truncated   bool             `json:"truncated"`          // true if caps clipped the result (Req 4.4)
}

// agentTargetRef names the scan target. Kind is "entity" (carrying a graph
// canonical id kind:provider:native-id) or "raw" (carrying an operator-supplied
// URL/IP with no graph entity). It carries NO org field: the tenant is always
// resolved server-side from the principal (Req 3.1-3.3, 9.6).
type agentTargetRef struct {
	Kind        string `json:"kind"`                   // "entity" | "raw"
	CanonicalID string `json:"canonical_id,omitempty"` // kind:provider:native-id
	Raw         string `json:"raw,omitempty"`          // URL/IP for raw targets
}

// agentGraphNode is one entity in the bounded subgraph.
type agentGraphNode struct {
	CanonicalID string         `json:"canonical_id"`
	Kind        string         `json:"kind"` // a model.Kind value
	Name        string         `json:"name"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// agentGraphEdge is one relationship in the bounded subgraph.
type agentGraphEdge struct {
	Relation string `json:"relation"` // a model.Relation value
	Src      string `json:"src"`
	Dst      string `json:"dst"`
}

// parseAgentTargetRef parses the ?target= query value into an agentTargetRef.
// The encoding is a scheme-prefixed string:
//
//	entity:<canonical_id>   -> an entity target (kind:provider:native-id)
//	raw:<url-or-ip>         -> an operator-supplied raw target
//
// An empty value, an unknown scheme, or an empty payload after the scheme is
// unparseable and yields ok=false so the handler responds 400 (Req 3.4). The
// canonical id itself contains colons (kind:provider:native-id), so only the
// FIRST colon is treated as the scheme separator.
func parseAgentTargetRef(raw string) (agentTargetRef, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return agentTargetRef{}, false
	}
	scheme, rest, found := strings.Cut(raw, ":")
	if !found {
		return agentTargetRef{}, false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return agentTargetRef{}, false
	}
	switch scheme {
	case "entity":
		return agentTargetRef{Kind: "entity", CanonicalID: rest}, true
	case "raw":
		return agentTargetRef{Kind: "raw", Raw: rest}, true
	default:
		return agentTargetRef{}, false
	}
}

// handleAgentGraphContext serves GET /api/agent/graph-context. It resolves the
// org from the principal (401 if none), parses ?target= (400 if unparseable),
// and returns a bounded scoped subgraph:
//   - a raw target has no graph entity -> an empty-but-valid bounded context
//     (Req 4.6);
//   - s.graph == nil -> 200 with graph:false and empty neighbors (Req 4.7);
//   - a graph read failure -> 503 with a generic temporarily-unavailable error,
//     the internal detail logged but never returned (Req 4.8);
//   - success -> 200 with the bounded subgraph.
//
// Route wiring lives in task 5 (registerAgentRoutes); this handler is defined
// here so that task can mount it.
func (s *Server) handleAgentGraphContext(c *gin.Context) {
	org, ok := mcpOrg(c) // org from the principal, NEVER from client input (Req 4.1)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	target, ok := parseAgentTargetRef(c.Query("target"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target"})
		return
	}

	// Raw targets have no graph entity: return an empty-but-valid bounded
	// context. GraphOnline still reflects whether the projection is wired.
	if target.Kind == "raw" {
		c.JSON(http.StatusOK, agentGraphContext{
			Target:      target,
			Neighbors:   []agentGraphNode{},
			Edges:       []agentGraphEdge{},
			Bounded:     true,
			GraphOnline: s.graph != nil,
		})
		return
	}

	// Graceful degradation: mirror the graph:false behavior when Memgraph is
	// down, returning a valid empty context rather than erroring (Req 4.7).
	if s.graph == nil {
		c.JSON(http.StatusOK, agentGraphContext{
			Target:      target,
			Neighbors:   []agentGraphNode{},
			Edges:       []agentGraphEdge{},
			Bounded:     true,
			GraphOnline: false,
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	gc, err := buildScopedSubgraph(ctx, s.graph, org, target, maxContextDepth, maxContextNodes)
	if err != nil {
		// Collapse any read failure to the generic message; log the detail so a
		// client cannot learn backend topology (Req 4.8).
		log.Printf("agent: graph-context: subgraph read failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}
	c.JSON(http.StatusOK, gc)
}

// buildScopedSubgraph reads a bounded subgraph around an entity target from the
// Memgraph projection via gw.Driver(), using a short-lived read session like
// the MCP read tools. The traversal is bounded to depth and the neighbor set is
// clipped to cap; Truncated is set when the caps actually clip the result so
// the payload size never scales with total graph size (Req 4.3, 4.4).
//
// The depth bound is interpolated into the Cypher only after being clamped to a
// small positive integer (never from raw input), matching the get_neighbors
// pattern. The org argument is accepted for a uniform seam signature and future
// per-tenant projection scoping; V1 scopes the read structurally by the seed
// canonical id, as the MCP read tools do.
func buildScopedSubgraph(ctx context.Context, gw *graph.Writer, org int64, target agentTargetRef, depth, nodeCap int) (agentGraphContext, error) {
	_ = org

	gc := agentGraphContext{
		Target:      target,
		Neighbors:   []agentGraphNode{},
		Edges:       []agentGraphEdge{},
		Bounded:     true,
		GraphOnline: true,
	}

	depth = clampContextDepth(depth)

	sess := gw.Driver().NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer sess.Close(ctx)

	// Seed lookup: the center entity's identity + scalar properties. A missing
	// seed is not an error — the target may simply not be in the projection —
	// it yields a context with a nil seed and no neighbors.
	seedRows, err := collectRows(ctx, sess,
		`MATCH (n:Entity {canonical_id: $id})
		 RETURN n.canonical_id AS canonical_id, n.name AS name, n.kind AS kind,
		        properties(n) AS props
		 LIMIT 1`,
		map[string]any{"id": target.CanonicalID})
	if err != nil {
		return agentGraphContext{}, err
	}
	if len(seedRows) == 0 {
		return gc, nil
	}
	sr := seedRows[0]
	seedProps, _ := sr["props"].(map[string]any)
	gc.Seed = &agentGraphNode{
		CanonicalID: asAgentString(sr["canonical_id"]),
		Kind:        asAgentString(sr["kind"]),
		Name:        asAgentString(sr["name"]),
		Attributes:  seedProps,
	}

	// Bounded traversal from the seed. The depth bound is a clamped small
	// integer; everything else travels as a bound parameter. LIMIT caps the
	// rows so a dense neighborhood cannot blow the payload past the node cap.
	// One extra row is requested so clipping to cap can be detected and
	// Truncated set (Req 4.4).
	cypher := `
		MATCH (c:Entity {canonical_id: $id})
		MATCH path = (c)-[*1..` + depthLiteral(depth) + `]-(m:Entity)
		WITH relationships(path) AS rels
		UNWIND rels AS r
		WITH startNode(r) AS a, endNode(r) AS b, r
		RETURN DISTINCT
		     a.canonical_id AS src_id, b.canonical_id AS dst_id, type(r) AS relation,
		     b.canonical_id AS n_id, b.name AS n_name, b.kind AS n_kind,
		     properties(b) AS n_props
		LIMIT $limit`

	// Over-fetch by one so a result that exactly fills the cap can be
	// distinguished from one that would overflow it.
	rows, err := collectRows(ctx, sess, cypher, map[string]any{
		"id":    target.CanonicalID,
		"limit": nodeCap + 1,
	})
	if err != nil {
		return agentGraphContext{}, err
	}

	seen := map[string]bool{target.CanonicalID: true}
	for _, r := range rows {
		// Stop adding neighbors once the cap is reached; mark Truncated so the
		// caller knows the subgraph was clipped.
		nid := asAgentString(r["n_id"])
		if nid != "" && nid != target.CanonicalID && !seen[nid] {
			if len(gc.Neighbors) >= nodeCap {
				gc.Truncated = true
				break
			}
			seen[nid] = true
			nprops, _ := r["n_props"].(map[string]any)
			gc.Neighbors = append(gc.Neighbors, agentGraphNode{
				CanonicalID: nid,
				Kind:        asAgentString(r["n_kind"]),
				Name:        asAgentString(r["n_name"]),
				Attributes:  nprops,
			})
		}
		gc.Edges = append(gc.Edges, agentGraphEdge{
			Relation: asAgentString(r["relation"]),
			Src:      asAgentString(r["src_id"]),
			Dst:      asAgentString(r["dst_id"]),
		})
	}

	return gc, nil
}

// collectRows runs a read-only Cypher query and returns the records as maps,
// mirroring the MCP read layer's readRows helper. Callers pass vetted,
// parameterized Cypher.
func collectRows(ctx context.Context, sess neo4j.SessionWithContext, cypher string, params map[string]any) ([]map[string]any, error) {
	res, err := sess.Run(ctx, cypher, params)
	if err != nil {
		return nil, err
	}
	recs, err := res.Collect(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		out = append(out, rec.AsMap())
	}
	return out, nil
}

// clampContextDepth bounds a requested traversal depth to [1, maxContextDepth]:
// a non-positive depth becomes 1, and anything above the cap is clipped to the
// cap, so the interpolated depth literal is always a small safe integer.
func clampContextDepth(d int) int {
	if d < 1 {
		return 1
	}
	if d > maxContextDepth {
		return maxContextDepth
	}
	return d
}

// depthLiteral renders a clamped depth as its decimal literal for interpolation
// into the variable-length traversal bound. It only ever handles the small
// clamped range [1, maxContextDepth], so a tiny switch is both sufficient and
// avoids pulling in strconv for a single-digit value.
func depthLiteral(d int) string {
	switch d {
	case 1:
		return "1"
	default:
		return "2"
	}
}

// asAgentString coerces a Cypher scalar to a string, tolerating nil. It mirrors
// the MCP read layer's asString helper so the seam renders ids/names the same
// way the existing tools do.
func asAgentString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
