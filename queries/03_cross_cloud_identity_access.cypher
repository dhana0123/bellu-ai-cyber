// Cross-cloud data access: identities that can read a data store, grouped by
// cloud. Returns rows for aws, azure and gcp from the same query, proving the
// model is provider-independent (no provider-specific node labels).
MATCH (i:Identity)-[:CAN_READ]->(d:DataStore)
RETURN i.provider AS provider, count(*) AS readable_datastores
ORDER BY provider;
