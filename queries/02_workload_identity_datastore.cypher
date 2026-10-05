// Workloads that reach a data store through an identity's permissions.
// Each CAN_* edge is approximate (effective:"approx", confidence 0.7) and
// links back to the IAM policy evidence that justified it.
MATCH (w:Workload)-[:USES]->(i:Identity)-[acc:CAN_READ|CAN_WRITE|CAN_ADMIN]->(d:DataStore)
RETURN w.name       AS workload,
       i.name       AS identity,
       type(acc)    AS access,
       acc.attr_actions AS actions,
       d.name       AS datastore,
       acc.confidence AS confidence,
       acc.evidence_ids AS evidence
ORDER BY workload;
