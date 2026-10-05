// AI agent blast radius: an agent's full reach to sensitive data stores and
// external AI APIs, through the images it runs in and the identities those
// workloads assume.

// 1) Agent -> sensitive data store (via workload identity chain).
MATCH (a:AIAgent)-[:RUNS_IN]->(w:Workload)-[:USES]->(sa:Identity)
      -[:AUTHENTICATES_AS]->(cloud:Identity)-[acc:CAN_READ|CAN_WRITE|CAN_ADMIN]->(d:DataStore)
RETURN a.name AS agent, w.name AS workload, type(acc) AS access, d.name AS datastore;

// 2) Agent -> external AI API observed on the network.
// MATCH (a:AIAgent)-[:RUNS_IN]->(w:Workload)-[:CONNECTS_TO]->(s:ExternalService)
// WHERE s.attr_is_ai_api = true
// RETURN a.name AS agent, w.name AS workload, s.name AS ai_endpoint;
