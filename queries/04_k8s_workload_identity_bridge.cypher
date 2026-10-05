// Kubernetes workloads reaching cloud data through the workload-identity
// bridge: Pod -> USES -> K8s ServiceAccount -> AUTHENTICATES_AS -> cloud
// Identity -> CAN_* -> DataStore. Covers IRSA, GKE WI and Azure WI.
MATCH (p:Workload)-[:USES]->(sa:Identity)-[a:AUTHENTICATES_AS]->(cloud:Identity)-[acc:CAN_READ|CAN_WRITE|CAN_ADMIN]->(d:DataStore)
RETURN p.name        AS pod,
       sa.name       AS service_account,
       a.attr_provider AS wi_provider,
       cloud.name    AS cloud_identity,
       type(acc)     AS access,
       d.name        AS datastore;
