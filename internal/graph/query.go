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
