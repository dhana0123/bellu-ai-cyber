// Cross-cloud identity sprawl: Kubernetes service accounts that authenticate as
// cloud identities across more than one provider, or data stores reachable by
// identities from multiple clouds.
MATCH (sa:Identity)-[:AUTHENTICATES_AS]->(cloud:Identity)
WITH sa, collect(DISTINCT cloud.provider) AS providers
WHERE size(providers) > 1
RETURN sa.name AS service_account, providers;
