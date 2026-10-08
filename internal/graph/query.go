package graph

import (
	"context"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// QueryRows runs a read-only Cypher query and returns records as maps. Intended
// for the control-panel API; callers pass vetted queries, not raw user input.
func (w *Writer) QueryRows(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error) {
	res, err := neo4j.ExecuteQuery(ctx, w.driver, cypher, params,
		neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(""))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res.Records))
	for _, rec := range res.Records {
		out = append(out, rec.AsMap())
	}
	return out, nil
}

// AttackPathNode is a node on a returned path.
type AttackPathNode struct {
	CanonicalID string `json:"canonical_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
}

// AttackPaths returns AI-agent attack paths to data stores, as node lists.
func (w *Writer) AttackPaths(ctx context.Context) ([]map[string]any, error) {
	cypher := `
		MATCH (a:AIAgent)-[:RUNS_IN]->(w:Workload)-[:USES]->(sa:Identity)
		      -[:AUTHENTICATES_AS]->(cl:Identity)-[acc:CAN_READ|CAN_WRITE|CAN_ADMIN]->(d:DataStore)
		RETURN a.name AS agent, w.name AS workload, sa.name AS service_account,
		       cl.name AS cloud_identity, type(acc) AS access, d.name AS datastore`
	return w.QueryRows(ctx, cypher, nil)
}

// ReachesAsset reports whether an attack path reaches the asset identified by
// canonicalID. It runs a single read-only, parameterized Cypher query and
// performs no graph writes (Requirement 10.2). The id is always passed as the
// $canonicalID parameter — it is never concatenated into the query text.
//
// An asset is considered reachable if any of the following holds:
//   - it (or the workload/compute hosting it, or an identity that can access
//     it) lies on a path originating from an externally-exposed entry point
//     (an EXPOSED_TO relation, or a NetworkEndpoint/ExternalService that
//     CONNECTS_TO it, treated as an internet-facing entry); or
//   - it lies on the existing AI-agent-to-datastore attack path reused from
//     Writer.AttackPaths.
//
// The Cypher is intentionally conservative and only references node kinds and
// relations already present in the vetted model allow-list (AIAgent, Workload,
// Identity, DataStore, NetworkEndpoint, ExternalService; RUNS_IN, USES,
// AUTHENTICATES_AS, CAN_READ/CAN_WRITE/CAN_ADMIN, CONNECTS_TO, EXPOSED_TO).
func (w *Writer) ReachesAsset(ctx context.Context, canonicalID string) (bool, error) {
	// Each OPTIONAL MATCH is bounded to the target asset via {canonical_id:
	// $canonicalID} so the query stays anchored on the asset and never scans
	// unrelated subgraphs. The final RETURN collapses the matches to a single
	// boolean. There are no MERGE/SET/CREATE/DELETE clauses: this is read-only.
	const cypher = `
		// Target asset by canonical id.
		MATCH (target:Entity {canonical_id: $canonicalID})

		// (1) An externally-exposed entry point reaches the asset directly.
		OPTIONAL MATCH exposedPath =
			(entry)-[:EXPOSED_TO|CONNECTS_TO*1..6]->(target)
		WHERE entry:NetworkEndpoint OR entry:ExternalService

		// (2) An entry point reaches an identity that can access the asset,
		//     or the workload/compute hosting it.
		OPTIONAL MATCH accessPath =
			(entry2)-[:EXPOSED_TO|CONNECTS_TO|RUNS_IN|USES|AUTHENTICATES_AS*1..8]->(reach)
			-[:CAN_READ|CAN_WRITE|CAN_ADMIN]->(target)
		WHERE entry2:NetworkEndpoint OR entry2:ExternalService

		// (3) The existing AI-agent-to-datastore attack path lands on the asset
		//     (only meaningful when the asset is a DataStore).
		RETURN (exposedPath IS NOT NULL
		        OR accessPath IS NOT NULL
		        OR (target:DataStore AND EXISTS {
		            MATCH (:AIAgent)-[:RUNS_IN]->(:Workload)-[:USES]->(:Identity)
		                  -[:AUTHENTICATES_AS]->(:Identity)
		                  -[:CAN_READ|CAN_WRITE|CAN_ADMIN]->(target)
		        })) AS reachable
		LIMIT 1`

	rows, err := w.QueryRows(ctx, cypher, map[string]any{"canonicalID": canonicalID})
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	reachable, _ := rows[0]["reachable"].(bool)
	return reachable, nil
}

// ExternalAICalls returns agent/workload connections to external AI APIs.
func (w *Writer) ExternalAICalls(ctx context.Context) ([]map[string]any, error) {
	cypher := `
		MATCH (w:Workload)-[:CONNECTS_TO]->(s:ExternalService)
		WHERE s.attr_is_ai_api = true
		OPTIONAL MATCH (a:AIAgent)-[:RUNS_IN]->(w)
		RETURN a.name AS agent, w.name AS workload, s.name AS endpoint,
		       s.attr_ai_service AS ai_service`
	return w.QueryRows(ctx, cypher, nil)
}
