package store

import (
	"context"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// DefaultCategory is the category assigned to any kind not present in
// CategoryForKind. It mirrors the frontend DEFAULT_CATEGORY constant.
const DefaultCategory = "Other"

// CategoryForKind maps a canonical entity kind to its presentation category.
// It is the server-authoritative copy of the frontend CATEGORY_FOR_KIND map
// used to fold kind facet counts into category facet counts. Kinds absent from
// this map fold into DefaultCategory.
var CategoryForKind = map[string]string{
	string(model.KindCompute):         "Compute",
	string(model.KindCluster):         "Compute",
	string(model.KindWorkload):        "Compute",
	string(model.KindContainerImage):  "Compute",
	string(model.KindDataStore):       "Storage",
	string(model.KindIdentity):        "IAM",
	string(model.KindCloudAccount):    "IAM",
	string(model.KindRepository):      "Source Control",
	string(model.KindPackage):         "Supply Chain",
	string(model.KindVulnerability):   "Vulnerabilities",
	string(model.KindAIAgent):         "AI",
	string(model.KindAIModel):         "AI",
	string(model.KindPrompt):          "AI",
	string(model.KindMCPServer):       "AI",
	string(model.KindAIService):       "AI",
	string(model.KindNetworkEndpoint): "Networking",
	string(model.KindExternalService): "Networking",
}

// categoryFor returns the presentation category for a kind, falling back to
// DefaultCategory for kinds not present in CategoryForKind.
func categoryFor(kind string) string {
	if c, ok := CategoryForKind[kind]; ok {
		return c
	}
	return DefaultCategory
}

// ExplorerFilter describes the server-side filter for a Graph Explorer query.
// Empty Kinds/Providers/Categories slices impose no constraint on that facet.
// Limit bounds the number of returned nodes (the working-set cap); it must be
// greater than zero (the handler supplies a default).
type ExplorerFilter struct {
	Kinds      []string
	Providers  []string
	Categories []string
	ProjectID  *int64
	Limit      int
}

// ExplorerNode is a canonical entity projected for the explorer working set.
type ExplorerNode struct {
	CanonicalID string `json:"id"`
	Kind        string `json:"kind"`
	Provider    string `json:"provider"`
	Name        string `json:"name"`
	Stale       bool   `json:"stale"`
}

// ExplorerEdge is a normalized edge among the returned nodes.
type ExplorerEdge struct {
	ID         int64   `json:"id"`
	SrcID      string  `json:"src_id"`
	Relation   string  `json:"relation"`
	DstID      string  `json:"dst_id"`
	Confidence float64 `json:"confidence"`
}

// ExplorerData is the result of Store.GraphExplorer: the bounded working set of
// nodes and edges plus authoritative, cap-independent counts and facet maps.
//
// CategoryFacet/ProviderFacet/KindFacet map a facet key to the count of nodes
// with that value across the whole filtered graph (independent of the cap).
// NodeCount/EdgeCount are the authoritative count(*) totals over the whole
// filtered graph. Capped is true when NodeCount exceeds len(Nodes).
type ExplorerData struct {
	Nodes []ExplorerNode
	Edges []ExplorerEdge

	CategoryFacet map[string]int64
	ProviderFacet map[string]int64
	KindFacet     map[string]int64

	NodeCount int64
	EdgeCount int64
	Capped    bool
}

// GraphExplorer returns the Graph Explorer working set for a filter. It is
// strictly read-only: it issues only SELECT statements and performs no writes.
//
// It returns up to f.Limit entities as nodes (filtered by the kind/provider
// facets), the edges strictly among those returned nodes (edge closure), and
// authoritative count(*) node/edge totals plus GROUP BY facet maps computed
// over the whole filtered graph independent of the cap. Category facets are
// folded from the kind facets via CategoryForKind. Capped is set true when the
// authoritative NodeCount exceeds the number of returned nodes.
func (s *Store) GraphExplorer(ctx context.Context, f ExplorerFilter) (*ExplorerData, error) {
	if f.Limit <= 0 {
		return nil, fmt.Errorf("store: graph explorer: limit must be > 0, got %d", f.Limit)
	}

	// Shared predicate: empty facet slice imposes no constraint on that facet.
	// $1 = kinds, $2 = providers. cardinality(...) = 0 means "no filter".
	const predicate = `(cardinality($1::text[]) = 0 OR kind = ANY($1))
		  AND (cardinality($2::text[]) = 0 OR provider = ANY($2))`

	// normalize nil slices to empty so ANY/cardinality behave consistently.
	kinds := f.Kinds
	if kinds == nil {
		kinds = []string{}
	}
	providers := f.Providers
	if providers == nil {
		providers = []string{}
	}

	data := &ExplorerData{
		CategoryFacet: map[string]int64{},
		ProviderFacet: map[string]int64{},
		KindFacet:     map[string]int64{},
	}

	// 1. Bounded node working set. Parameterized; filter values never
	//    concatenated into SQL. $3 = limit.
	nodeRows, err := s.pool.Query(ctx,
		`SELECT canonical_id, kind, provider, name, stale
		 FROM normalized_entities
		 WHERE `+predicate+`
		 ORDER BY kind, canonical_id
		 LIMIT $3`,
		kinds, providers, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: graph explorer nodes: %w", err)
	}
	ids := make([]string, 0)
	for nodeRows.Next() {
		var n ExplorerNode
		if err := nodeRows.Scan(&n.CanonicalID, &n.Kind, &n.Provider, &n.Name, &n.Stale); err != nil {
			nodeRows.Close()
			return nil, err
		}
		data.Nodes = append(data.Nodes, n)
		ids = append(ids, n.CanonicalID)
	}
	nodeRows.Close()
	if err := nodeRows.Err(); err != nil {
		return nil, err
	}

	// 2. Edges strictly among the returned node set (edge closure). Skip the
	//    query entirely when there are no nodes.
	if len(ids) > 0 {
		edgeRows, err := s.pool.Query(ctx,
			`SELECT id, src_id, relation, dst_id, confidence
			 FROM normalized_edges
			 WHERE src_id = ANY($1) AND dst_id = ANY($1)
			 ORDER BY id`,
			ids)
		if err != nil {
			return nil, fmt.Errorf("store: graph explorer edges: %w", err)
		}
		for edgeRows.Next() {
			var e ExplorerEdge
			if err := edgeRows.Scan(&e.ID, &e.SrcID, &e.Relation, &e.DstID, &e.Confidence); err != nil {
				edgeRows.Close()
				return nil, err
			}
			data.Edges = append(data.Edges, e)
		}
		edgeRows.Close()
		if err := edgeRows.Err(); err != nil {
			return nil, err
		}
	}

	// 3. Authoritative node count over the whole filtered graph (cap-independent).
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM normalized_entities WHERE `+predicate,
		kinds, providers).Scan(&data.NodeCount); err != nil {
		return nil, fmt.Errorf("store: graph explorer node count: %w", err)
	}

	// Authoritative edge count over the whole filtered graph: edges whose both
	// endpoints match the node predicate.
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM normalized_edges e
		 WHERE EXISTS (SELECT 1 FROM normalized_entities n WHERE n.canonical_id = e.src_id AND (`+predicate+`))
		   AND EXISTS (SELECT 1 FROM normalized_entities n WHERE n.canonical_id = e.dst_id AND (`+predicate+`))`,
		kinds, providers).Scan(&data.EdgeCount); err != nil {
		return nil, fmt.Errorf("store: graph explorer edge count: %w", err)
	}

	// 4. Kind facet map over the whole filtered graph (GROUP BY).
	kindRows, err := s.pool.Query(ctx,
		`SELECT kind, count(*) FROM normalized_entities WHERE `+predicate+` GROUP BY kind`,
		kinds, providers)
	if err != nil {
		return nil, fmt.Errorf("store: graph explorer kind facet: %w", err)
	}
	for kindRows.Next() {
		var k string
		var n int64
		if err := kindRows.Scan(&k, &n); err != nil {
			kindRows.Close()
			return nil, err
		}
		data.KindFacet[k] = n
		// Fold the kind count into its category, summing across kinds.
		data.CategoryFacet[categoryFor(k)] += n
	}
	kindRows.Close()
	if err := kindRows.Err(); err != nil {
		return nil, err
	}

	// 5. Provider facet map over the whole filtered graph (GROUP BY).
	provRows, err := s.pool.Query(ctx,
		`SELECT provider, count(*) FROM normalized_entities WHERE `+predicate+` GROUP BY provider`,
		kinds, providers)
	if err != nil {
		return nil, fmt.Errorf("store: graph explorer provider facet: %w", err)
	}
	for provRows.Next() {
		var p string
		var n int64
		if err := provRows.Scan(&p, &n); err != nil {
			provRows.Close()
			return nil, err
		}
		data.ProviderFacet[p] = n
	}
	provRows.Close()
	if err := provRows.Err(); err != nil {
		return nil, err
	}

	// 6. Capped is true when the authoritative total exceeds the returned slice.
	data.Capped = data.NodeCount > int64(len(data.Nodes))

	return data, nil
}
