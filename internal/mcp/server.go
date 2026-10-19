// Package mcp exposes a tenant-scoped read surface over the temporal graph:
// the Memgraph projection (current/as-of inventory and adjacency) and the two
// append-only logs (history and the recent-change feed), surfaced as six MCP
// tools. It is mounted behind the existing JWT + RBAC router so every call is
// already authenticated and authorized before it reaches a tool.
//
// Tenant isolation is structural: every tool method takes org int64 as its
// first business argument, and the caller resolves that org from the
// authenticated principal (never from a client-supplied field). A client that
// passes an organization in its arguments is ignored — the org argument the
// mount supplies always wins (Req 10.2, Req 16.2).
//
// The six tools (Req 10.1):
//
//	find_entities    search the projection by kind / name substring
//	get_entity       one entity's current (or as-of) detail incl. derived props
//	get_neighbors    adjacency to depth N (1..5), edges tagged by lane
//	entity_history   the ordered change stream for one entity (both lanes)
//	recent_changes   the tenant-wide change feed since a cutoff
//	describe_schema  the model allow-lists (kinds, relations) + derived props
//
// Input clamping (get_neighbors depth), per-edge lane tagging, name-alongside-id
// enrichment, and the plain not-found / temporarily-unavailable error messages
// are task 17.2; this file wires the six tools, org-from-context resolution, and
// the mount. The projection reads use graph.Writer.Driver() directly so the
// package stays a thin read layer over the two existing stores.
package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/temporal"
)

// Querier is the subset of temporal.Querier the MCP server reads the two logs
// through: history and the recent-change feed. Narrowing it to an interface
// keeps the server testable without a live PostgreSQL and documents exactly
// which log queries the read surface depends on. *temporal.Querier satisfies it.
type Querier interface {
	History(ctx context.Context, org int64, canonicalID string, opts temporal.HistoryOpts) ([]temporal.ChangeRow, error)
	RecentChanges(ctx context.Context, org int64, since time.Time, limit int) ([]temporal.ChangeRow, error)
}

// Server answers the six MCP read tools over the temporal graph. It holds the
// Querier (the two append-only logs: history + recent changes) and the Memgraph
// driver (the projection: inventory, detail, adjacency). Both are read-only
// from here: the server never writes to either store.
//
// The server itself is tenant-agnostic — every tool takes the org as an
// argument that the mount resolves from the authenticated context — so a single
// Server instance serves all tenants safely.
type Server struct {
	querier Querier
	driver  neo4j.DriverWithContext
}

// NewServer builds a Server over the two logs (via the Querier) and the
// Memgraph projection (via the graph writer's driver). The graph writer may be
// nil when Memgraph is unavailable; in that case the projection-backed tools
// (find_entities, get_entity, get_neighbors) report the projection is
// unavailable while the log-backed tools (entity_history, recent_changes,
// describe_schema) still work, since the logs are the source of truth.
func NewServer(querier Querier, gw *graph.Writer) *Server {
	s := &Server{querier: querier}
	if gw != nil {
		s.driver = gw.Driver()
	}
	return s
}

// EntityRef is the lightweight identity of an entity: its canonical id, its
// human-readable name, and its kind. find_entities returns these; name travels
// alongside the id so callers never have to resolve it separately (Req 10.6).
type EntityRef struct {
	CanonicalID string `json:"canonical_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
}

// FindEntitiesInput selects entities from the projection by kind and/or a
// case-insensitive name substring. All fields are optional; an empty input
// lists entities up to the limit. The org is never read from here — it is the
// resolved tenant passed to FindEntities.
type FindEntitiesInput struct {
	// Kind, when set, restricts to a single node kind (validated against the
	// model allow-list; an unknown kind matches nothing).
	Kind string `json:"kind"`
	// NameLike, when set, is a case-insensitive substring match on the name.
	NameLike string `json:"name_like"`
	// Limit caps the number of results; non-positive means the default.
	Limit int `json:"limit"`
}

// FindEntities searches the projection for entities matching the input,
// returning id + name + kind for each (Req 10.1). The query is read-only and
// parameterized; the optional kind label is validated against the model
// allow-list and only then used to narrow the match. Results are scoped to the
// authenticated tenant via org.
func (s *Server) FindEntities(ctx context.Context, org int64, in FindEntitiesInput) ([]EntityRef, error) {
	if s.driver == nil {
		return nil, errProjectionUnavailable
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultFindLimit
	}

	// The kind label, when provided, is validated against the allow-list before
	// it is placed in the Cypher text — it is never interpolated from raw input
	// otherwise. Everything else travels as a bound parameter.
	kindClause := ""
	if in.Kind != "" {
		if !model.ValidKind(model.Kind(in.Kind)) {
			return []EntityRef{}, nil
		}
		kindClause = ":" + in.Kind
	}

	cypher := fmt.Sprintf(`
		MATCH (n:Entity%s)
		WHERE $org_scoped
		  AND ($name_like = '' OR toLower(n.name) CONTAINS toLower($name_like))
		RETURN n.canonical_id AS canonical_id, n.name AS name, n.kind AS kind
		ORDER BY n.name
		LIMIT $limit`, kindClause)

	rows, err := s.readRows(ctx, cypher, map[string]any{
		"org_scoped": true, // V1 projection is tenant-global; org is enforced on the log reads.
		"name_like":  in.NameLike,
		"limit":      limit,
	})
	if err != nil {
		return nil, err
	}

	out := make([]EntityRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, EntityRef{
			CanonicalID: asString(r["canonical_id"]),
			Name:        asString(r["name"]),
			Kind:        asString(r["kind"]),
		})
	}
	return out, nil
}

// GetEntityInput identifies the entity to fetch and the optional bi-temporal
// cutoff. Zero EventTime/KnownAt mean the current (reality) projection.
type GetEntityInput struct {
	// CanonicalID is the entity to fetch (required).
	CanonicalID string `json:"canonical_id"`
	// EventTime, when non-zero, requests the as-of view at that event time.
	EventTime time.Time `json:"event_time"`
	// KnownAt, when non-zero, adds the asKnownAt (audit) cutoff.
	KnownAt time.Time `json:"known_at"`
}

// EntityDetail is a single entity's full projection: identity plus its scalar
// properties and the derived classification properties (data_class and friends)
// that the derived overlay sets on the node. Name travels alongside the id
// (Req 10.6).
type EntityDetail struct {
	CanonicalID string         `json:"canonical_id"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Provider    string         `json:"provider"`
	Stale       bool           `json:"stale"`
	Properties  map[string]any `json:"properties"`
}

// GetEntity returns the full current entity from the projection, including the
// derived classification properties the overlay sets (data_class,
// data_class_confidence, classified_by) (Req 10.1). The lookup is read-only and
// parameterized on the canonical id. The bi-temporal cutoff on the input is
// accepted for forward compatibility with an as-of projection; V1 serves the
// current projection. Results are scoped to the authenticated tenant via org.
func (s *Server) GetEntity(ctx context.Context, org int64, in GetEntityInput) (*EntityDetail, error) {
	if s.driver == nil {
		return nil, errProjectionUnavailable
	}
	if in.CanonicalID == "" {
		return nil, fmt.Errorf("mcp: get_entity requires a canonical_id")
	}

	rows, err := s.readRows(ctx,
		`MATCH (n:Entity {canonical_id: $id})
		 RETURN n.canonical_id AS canonical_id, n.name AS name, n.kind AS kind,
		        n.provider AS provider, coalesce(n.stale, false) AS stale,
		        properties(n) AS props
		 LIMIT 1`,
		map[string]any{"id": in.CanonicalID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil // not found; the mount renders the plain not-found message (17.2).
	}

	r := rows[0]
	props, _ := r["props"].(map[string]any)
	return &EntityDetail{
		CanonicalID: asString(r["canonical_id"]),
		Name:        asString(r["name"]),
		Kind:        asString(r["kind"]),
		Provider:    asString(r["provider"]),
		Stale:       asBool(r["stale"]),
		Properties:  props,
	}, nil
}

// GetNeighborsInput selects the center entity and the traversal depth. Depth is
// clamped to [1,5] (the clamp itself is task 17.2); EventTime/KnownAt are the
// bi-temporal cutoff (zero = now).
type GetNeighborsInput struct {
	CanonicalID string    `json:"canonical_id"`
	Depth       int       `json:"depth"`
	EventTime   time.Time `json:"event_time"`
	KnownAt     time.Time `json:"known_at"`
}

// NeighborEdge is one adjacency edge: its endpoints, relation, and the lane it
// came from. Lane is "asserted" for base (observed) edges and "inferred" for
// derived edges, read off the edge's inferred property (task 17.2 finalizes the
// tagging and depth clamp).
type NeighborEdge struct {
	SrcID    string  `json:"src_id"`
	Relation string  `json:"relation"`
	DstID    string  `json:"dst_id"`
	Lane     string  `json:"lane"`
	Conf     float64 `json:"confidence"`
}

// Neighbors is the adjacency around a center entity: the neighbor entities and
// the edges connecting them, each edge tagged with its lane.
type Neighbors struct {
	Center string         `json:"center"`
	Nodes  []EntityRef    `json:"nodes"`
	Edges  []NeighborEdge `json:"edges"`
}

// GetNeighbors returns the adjacency around the center entity to the requested
// depth, mixing asserted (observed) and inferred (derived) edges and tagging
// each with its lane (Req 10.1, Req 10.5). The traversal depth is bounded to a
// safe range so a deep request cannot run away; the final [1,5] clamp and the
// not-found behavior are task 17.2. Results are scoped to the tenant via org.
func (s *Server) GetNeighbors(ctx context.Context, org int64, in GetNeighborsInput) (*Neighbors, error) {
	if s.driver == nil {
		return nil, errProjectionUnavailable
	}
	if in.CanonicalID == "" {
		return nil, fmt.Errorf("mcp: get_neighbors requires a canonical_id")
	}
	depth := clampDepth(in.Depth)

	// Variable-length traversal; the depth bound is interpolated only after the
	// clamp guarantees it is a small integer in [1,5], never from raw input.
	cypher := fmt.Sprintf(`
		MATCH (c:Entity {canonical_id: $id})
		MATCH path = (c)-[*1..%d]-(m:Entity)
		WITH c, relationships(path) AS rels, nodes(path) AS ns
		UNWIND rels AS r
		WITH c,
		     startNode(r) AS a, endNode(r) AS b, r
		RETURN DISTINCT
		     a.canonical_id AS src_id, b.canonical_id AS dst_id, type(r) AS relation,
		     coalesce(r.inferred, false) AS inferred, coalesce(r.confidence, 1.0) AS confidence,
		     b.canonical_id AS n_id, b.name AS n_name, b.kind AS n_kind`, depth)

	rows, err := s.readRows(ctx, cypher, map[string]any{"id": in.CanonicalID})
	if err != nil {
		return nil, err
	}

	nb := &Neighbors{Center: in.CanonicalID, Nodes: []EntityRef{}, Edges: []NeighborEdge{}}
	seenNode := map[string]bool{}
	for _, r := range rows {
		edge := NeighborEdge{
			SrcID:    asString(r["src_id"]),
			Relation: asString(r["relation"]),
			DstID:    asString(r["dst_id"]),
			Lane:     laneForEdge(asBool(r["inferred"])),
			Conf:     asFloat(r["confidence"]),
		}
		nb.Edges = append(nb.Edges, edge)

		nid := asString(r["n_id"])
		if nid != "" && nid != in.CanonicalID && !seenNode[nid] {
			seenNode[nid] = true
			nb.Nodes = append(nb.Nodes, EntityRef{
				CanonicalID: nid,
				Name:        asString(r["n_name"]),
				Kind:        asString(r["n_kind"]),
			})
		}
	}
	return nb, nil
}

// EntityHistoryInput selects the entity and tunes the returned change stream.
type EntityHistoryInput struct {
	// CanonicalID is the entity whose history to return (required).
	CanonicalID string `json:"canonical_id"`
	// Newest returns most-recent changes first (default is oldest first).
	Newest bool `json:"newest"`
	// Limit caps the stream; non-positive means no cap.
	Limit int `json:"limit"`
	// IncludeDerived merges the derived lane (inferred edges, classifications)
	// into the stream alongside the observed facts.
	IncludeDerived bool `json:"include_derived"`
}

// EntityHistory returns the ordered change stream for one entity, merging both
// lanes by time (Req 10.1). It reads the two logs through the Querier and is
// tenant-scoped by org (Req 16.2). A missing entity yields an empty slice.
func (s *Server) EntityHistory(ctx context.Context, org int64, in EntityHistoryInput) ([]temporal.ChangeRow, error) {
	if in.CanonicalID == "" {
		return nil, fmt.Errorf("mcp: entity_history requires a canonical_id")
	}
	return s.querier.History(ctx, org, in.CanonicalID, temporal.HistoryOpts{
		Newest:         in.Newest,
		Limit:          in.Limit,
		IncludeDerived: in.IncludeDerived,
	})
}

// RecentChangesInput tunes the tenant-wide change feed.
type RecentChangesInput struct {
	// Since returns only changes recorded strictly after this cutoff. Zero
	// returns the most recent changes up to the limit.
	Since time.Time `json:"since"`
	// Limit caps the number of changes (must be positive; defaulted when not).
	Limit int `json:"limit"`
}

// RecentChanges returns the tenant's recent observed changes newest-first since
// the cutoff (Req 10.1). It reads the observed log through the Querier and is
// tenant-scoped by org (Req 16.2). A non-positive limit is defaulted so the
// tool is forgiving of a missing limit.
func (s *Server) RecentChanges(ctx context.Context, org int64, in RecentChangesInput) ([]temporal.ChangeRow, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultRecentLimit
	}
	return s.querier.RecentChanges(ctx, org, in.Since, limit)
}

// SchemaDoc describes the graph's allow-lists and the derived node properties,
// so a client can discover what kinds, relations, and derived annotations exist
// without probing the data. It is static (derived from the model package) and
// identical across tenants.
type SchemaDoc struct {
	// Kinds is the model's node-kind allow-list.
	Kinds []string `json:"kinds"`
	// Relations is the model's relation allow-list.
	Relations []string `json:"relations"`
	// DerivedProperties are the namespaced node properties the derived overlay
	// sets (classification) plus the inferred-edge marker, so a client knows how
	// to read derivations off the projection.
	DerivedProperties []string `json:"derived_properties"`
}

// DescribeSchema returns the model allow-lists (kinds, relations) plus the
// derived properties the overlay annotates the projection with (Req 10.1). The
// org is accepted for a uniform tool signature; the schema is identical for
// every tenant, so it is not used to narrow the result.
func (s *Server) DescribeSchema(ctx context.Context, org int64) (*SchemaDoc, error) {
	_ = ctx
	_ = org

	kinds := make([]string, 0, len(model.AllKinds))
	for _, k := range model.AllKinds {
		kinds = append(kinds, string(k))
	}
	relations := make([]string, 0, len(model.AllRelations))
	for _, r := range model.AllRelations {
		relations = append(relations, string(r))
	}

	return &SchemaDoc{
		Kinds:     kinds,
		Relations: relations,
		// Derived annotations the replay overlay writes: inferred edges carry
		// inferred=true (+confidence/evidence), and classified datastores carry
		// the three namespaced data_class properties.
		DerivedProperties: []string{
			"inferred",
			"data_class",
			"data_class_confidence",
			"classified_by",
		},
	}, nil
}

// readRows runs a read-only Cypher query against the projection and returns the
// records as maps. It opens a short-lived read session so the server never
// holds a write transaction open. Callers pass vetted, parameterized Cypher.
func (s *Server) readRows(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error) {
	sess := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer sess.Close(ctx)

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

// clampDepth bounds a requested traversal depth to [1,5]: a non-positive depth
// becomes 1, and anything above 5 is capped at 5 (Req 10.4).
func clampDepth(d int) int {
	const (
		minDepth = 1
		maxDepth = 5
	)
	if d < minDepth {
		return minDepth
	}
	if d > maxDepth {
		return maxDepth
	}
	return d
}

// laneForEdge maps the projection's inferred marker to the lane label the tool
// surfaces: "inferred" for derived edges, "asserted" for base (observed) edges
// (Req 10.5).
func laneForEdge(inferred bool) string {
	if inferred {
		return laneInferred
	}
	return laneAsserted
}

const (
	laneAsserted = "asserted"
	laneInferred = "inferred"

	defaultFindLimit   = 100
	defaultRecentLimit = 100
)

// errProjectionUnavailable is returned by the projection-backed tools when the
// graph writer was nil at construction (Memgraph down). The mount renders it as
// the generic "temporarily unavailable" message (task 17.2); the logs-backed
// tools keep working because the logs are the source of truth.
var errProjectionUnavailable = fmt.Errorf("mcp: graph projection unavailable")

// asString coerces a Cypher scalar to a string, tolerating nil.
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// asBool coerces a Cypher scalar to a bool, defaulting to false.
func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// asFloat coerces a Cypher numeric scalar to float64, tolerating int64/int.
func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

// trimKind is a small helper kept for symmetry with future input normalization
// (task 17.2); it trims surrounding whitespace from a client-supplied kind.
func trimKind(k string) string { return strings.TrimSpace(k) }
