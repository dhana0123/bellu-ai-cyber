// Workloads reaching an external AI API, observed from network traffic (Zeek).
// The source IP is resolved to the owning workload via time-bounded IP aliases;
// the destination is tagged as an AI service from a curated domain list.
MATCH (w:Workload)-[c:CONNECTS_TO]->(s:ExternalService)
WHERE s.attr_is_ai_api = true
RETURN w.name            AS workload,
       s.name            AS endpoint,
       s.attr_ai_service AS ai_service,
       c.evidence_ids    AS zeek_evidence
ORDER BY workload;
