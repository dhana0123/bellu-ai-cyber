// Exposed workloads running images with vulnerable AI packages.
// Combines network exposure (EXPOSED_TO internet) with SBOM vulnerabilities on
// AI-framework packages.
MATCH (img:ContainerImage)-[:DEPENDS_ON]->(p:Package)-[:HAS_VULN]->(v:Vulnerability)
MATCH (w:Workload)-[:RUNS_IMAGE]->(img)
OPTIONAL MATCH (w)-[:EXPOSED_TO]->(net:NetworkEndpoint)
RETURN w.name AS workload, p.name AS package, v.name AS cve,
       net.name AS exposed_to
ORDER BY cve;
