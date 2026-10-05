// AI repositories (or images) that depend on a vulnerable package.
// Demonstrates the SBOM + AI-BOM graph from Task 5.
MATCH (subj:Entity)-[:DEPENDS_ON|USES_MODEL*1..4]->(p:Package)-[:HAS_VULN]->(v:Vulnerability)
WHERE subj:Repository OR subj:ContainerImage
RETURN subj.canonical_id AS subject,
       labels(subj)       AS subject_labels,
       p.name             AS package,
       v.name             AS cve,
       v.attr_description  AS description
ORDER BY cve;
