package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file defines the Graph Explorer response contract (task 2.1): the JSON
// shapes returned by GET /api/graph/explorer and a helper that maps the
// store-layer *store.ExplorerData working set into that contract. The handler
// (task 2.2) and route wiring (task 2.3) live elsewhere.
//
// The structs mirror src/types/graphExplorer.ts so the frontend client can
// decode the response directly. Facet maps become sorted-by-nothing slices of
// {key,label,count}; here label equals key (the raw kind/provider/category
// value), matching the design's facetCount shape.

// explorerNode mirrors the frontend GraphNode. The category is derived
// server-side by folding the entity kind through store.CategoryForKind so the
// client never has to recompute it.
type explorerNode struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Stale    bool   `json:"stale"`
}

// explorerEdge mirrors the frontend GraphEdge. The store edge id is a numeric
// primary key; it is rendered as a string here to match the frontend contract
// where every node/edge id is a string.
type explorerEdge struct {
	ID         string  `json:"id"`
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Relation   string  `json:"relation"`
	Confidence float64 `json:"confidence"`
}

// facetCount is one selectable facet key with its label and node count across
// the whole filtered graph (cap-independent).
type facetCount struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// facets groups the three facet dimensions the explorer exposes.
type facets struct {
	Categories []facetCount `json:"categories"`
	Providers  []facetCount `json:"providers"`
	Kinds      []facetCount `json:"kinds"`
}

// explorerResponse is the full GET /api/graph/explorer body. NodeCount and
// EdgeCount are the authoritative count(*) totals (independent of the cap);
// Capped reports whether the returned node slice was bounded; GraphAvailable
// reports whether the optional Memgraph service is reachable.
type explorerResponse struct {
	Nodes          []explorerNode `json:"nodes"`
	Edges          []explorerEdge `json:"edges"`
	Facets         facets         `json:"facets"`
	NodeCount      int64          `json:"nodeCount"`
	EdgeCount      int64          `json:"edgeCount"`
	Capped         bool           `json:"capped"`
	GraphAvailable bool           `json:"graphAvailable"`
}

// categoryForKind folds a canonical entity kind into its presentation category
// using the store-authoritative map, falling back to store.DefaultCategory for
// kinds absent from the map. It mirrors the unexported store.categoryFor so the
// server response can label categories without duplicating the mapping.
func categoryForKind(kind string) string {
	if c, ok := store.CategoryForKind[kind]; ok {
		return c
	}
	return store.DefaultCategory
}

// newExplorerResponse maps a store working set into the wire contract. The
// graphAvailable flag is supplied by the caller (handler) since it reflects the
// Server's Memgraph wiring rather than anything in the store data.
//
// Node/edge slices are always non-nil so the JSON encodes [] rather than null
// for an empty working set. Facet maps are projected into {key,label,count}
// slices with label == key (the raw facet value).
func newExplorerResponse(data *store.ExplorerData, graphAvailable bool) explorerResponse {
	resp := explorerResponse{
		Nodes:          make([]explorerNode, 0, len(data.Nodes)),
		Edges:          make([]explorerEdge, 0, len(data.Edges)),
		NodeCount:      data.NodeCount,
		EdgeCount:      data.EdgeCount,
		Capped:         data.Capped,
		GraphAvailable: graphAvailable,
	}

	for _, n := range data.Nodes {
		resp.Nodes = append(resp.Nodes, explorerNode{
			ID:       n.CanonicalID,
			Kind:     n.Kind,
			Provider: n.Provider,
			Category: categoryForKind(n.Kind),
			Name:     n.Name,
			Stale:    n.Stale,
		})
	}

	for _, e := range data.Edges {
		resp.Edges = append(resp.Edges, explorerEdge{
			ID:         strconv.FormatInt(e.ID, 10),
			Source:     e.SrcID,
			Target:     e.DstID,
			Relation:   e.Relation,
			Confidence: e.Confidence,
		})
	}

	resp.Facets = facets{
		Categories: facetSlice(data.CategoryFacet),
		Providers:  facetSlice(data.ProviderFacet),
		Kinds:      facetSlice(data.KindFacet),
	}

	return resp
}

// facetSlice projects a facet count map into the wire slice. The label equals
// the key (the raw facet value); the slice is always non-nil so the JSON
// encodes [] for an empty facet.
func facetSlice(m map[string]int64) []facetCount {
	out := make([]facetCount, 0, len(m))
	for k, n := range m {
		out = append(out, facetCount{Key: k, Label: k, Count: n})
	}
	return out
}

// defaultExplorerLimit is the default and maximum node cap for the explorer
// working set (Requirement 8.6 / 9.1 / 9.2). An omitted limit defaults to this
// value; a limit greater than this is clamped down to it to protect the DB and
// the browser renderer.
const defaultExplorerLimit = 5000

// knownProviders is the provider allow-list used to validate the provider
// facet before any data query is issued (Requirement 8.9). The repo has no
// single exported provider enum; this mirrors the canonical provider
// vocabulary documented on model.Entity.Provider ("aws | azure | gcp | k8s |
// image | repo | network"), which is the authoritative set of values the
// resolver assigns to normalized_entities.provider.
var knownProviders = map[string]struct{}{
	"aws":     {},
	"azure":   {},
	"gcp":     {},
	"k8s":     {},
	"image":   {},
	"repo":    {},
	"network": {},
}

// knownCategories is the category allow-list: the set of category labels in
// store.CategoryForKind plus store.DefaultCategory. It is built once at init so
// the handler can validate the category facet in O(1) without issuing a query
// (Requirement 8.9 / 8.10).
var knownCategories = buildKnownCategories()

func buildKnownCategories() map[string]struct{} {
	set := map[string]struct{}{store.DefaultCategory: {}}
	for _, cat := range store.CategoryForKind {
		set[cat] = struct{}{}
	}
	return set
}

// handleGraphExplorer serves GET /api/graph/explorer. It validates and clamps
// the query inputs, enforces optional project scoping, and returns the
// Postgres-sourced working set. It is mounted under the Authenticate +
// PermGraphRead group (route wiring is task 2.3).
//
// Validation precedes any data query: a non-member kind/provider/category, a
// malformed/out-of-range limit, or a malformed project_id is rejected with 400
// before s.store.GraphExplorer is called, leaving stored data unchanged
// (Requirement 8.10). GraphAvailable reflects s.graph != nil; the endpoint
// never returns 5xx merely because Memgraph is down, since all data comes from
// Postgres (Requirement 11.1-11.3).
func (s *Server) handleGraphExplorer(c *gin.Context) {
	// Parse + validate the CSV facet inputs against their allow-lists first, so
	// a bad input is a 400 before any query runs.
	kinds, ok := parseCSVFacet(c, "kinds", validKind)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid kinds facet"})
		return
	}
	providers, ok := parseCSVFacet(c, "providers", validProvider)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid providers facet"})
		return
	}
	categories, ok := parseCSVFacet(c, "categories", validCategory)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid categories facet"})
		return
	}

	// Parse + clamp the limit. Omitted -> default cap; > cap -> clamp to cap;
	// non-numeric or < 1 -> 400.
	limit, ok := parseExplorerLimit(c.Query("limit"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
		return
	}

	filter := store.ExplorerFilter{
		Kinds:      kinds,
		Providers:  providers,
		Categories: categories,
		Limit:      limit,
	}

	// Optional project scoping, mirroring handleEntities: malformed project_id
	// is a 400, the principal is resolved server-side, and access is enforced
	// with CanAccessProject (403 when not accessible).
	if pid := c.Query("project_id"); pid != "" {
		projectID, perr := strconv.ParseInt(pid, 10, 64)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project_id"})
			return
		}
		p, pok := auth.PrincipalFrom(c)
		if !pok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if !p.CanAccessProject(projectID) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		filter.ProjectID = &projectID
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	data, err := s.store.GraphExplorer(ctx, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// GraphAvailable reflects whether the optional Memgraph service is wired;
	// the data itself is Postgres-sourced, so s.graph == nil is not an error.
	c.JSON(http.StatusOK, newExplorerResponse(data, s.graph != nil))
}

// parseCSVFacet parses a comma-separated facet query param into a slice of
// trimmed, non-empty tokens, validating each against valid. It returns ok=false
// (so the caller responds 400) on the first non-member token, without ever
// touching the store. An absent or empty param yields a nil slice and ok=true
// ("no filter on this facet").
func parseCSVFacet(c *gin.Context, key string, valid func(string) bool) (vals []string, ok bool) {
	raw := c.Query(key)
	if raw == "" {
		return nil, true
	}
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if !valid(tok) {
			return nil, false
		}
		vals = append(vals, tok)
	}
	return vals, true
}

// parseExplorerLimit parses and clamps the limit query param. An empty value
// defaults to the cap; a value greater than the cap clamps to the cap; a
// non-numeric value or a value < 1 is rejected (ok=false -> 400).
func parseExplorerLimit(raw string) (limit int, ok bool) {
	if raw == "" {
		return defaultExplorerLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, false
	}
	if n > defaultExplorerLimit {
		return defaultExplorerLimit, true
	}
	return n, true
}

// validKind reports whether k is a member of the canonical kind allow-list.
func validKind(k string) bool { return model.ValidKind(model.Kind(k)) }

// validProvider reports whether p is a member of the provider allow-list.
func validProvider(p string) bool { _, ok := knownProviders[p]; return ok }

// validCategory reports whether cat is a member of the category allow-list
// (store.CategoryForKind values plus store.DefaultCategory).
func validCategory(cat string) bool { _, ok := knownCategories[cat]; return ok }
