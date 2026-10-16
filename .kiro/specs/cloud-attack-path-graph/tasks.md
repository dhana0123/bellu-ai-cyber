# Implementation Plan: Cloud Attack Path Graph

## Overview

This plan extends Bellu's existing Go discovery pipeline (Steampipe → RawEvent → Normalizer → EntityResolver → GraphWriter → Memgraph) to close silent edge loss and expand AWS relationship coverage. Work is ordered to de-risk the feature: the shared model/schema foundation comes first (everything depends on it), then visibility (which makes the rest observable), then key-normalization fidelity, then IAM/trust/network/data-store/entry-point coverage, and finally end-to-end integration.

Every new entity/edge is a generic `model.Entity`/`model.Edge`. Analyzers (`Trust_Policy_Analyzer`, `IAM_Access_Evaluator`, `Instance_Profile_Mapper`) are helper components invoked inside `AWS.Normalize`'s existing passes, not new pipeline interfaces. New standalone normalizers (`AWSNetwork`, `Entry_Point_Normalizer`) are registered in `internal/pipeline/process.go`.

Property-based tests use `pgregory.net/rapid` (min 100 iterations), one test per design property, tagged `// Feature: cloud-attack-path-graph, Property {n}: {text}`. Test sub-tasks are marked optional with `*`.

## Tasks

- [ ] 1. Model/schema foundation: Kinds, Relations, and visibility persistence
  - [ ] 1.1 Add new Kind and Relation values to the shared allow-lists
    - In `internal/model/model.go`, add `KindNetwork = "Network"` and `KindEntryPoint = "EntryPoint"` (DataStore already exists) and append both to `AllKinds`.
    - Add `RelCanAssume = "CAN_ASSUME"`, `RelRoutesTo = "ROUTES_TO"`, `RelPeersWith = "PEERS_WITH"`, `RelMemberOf = "MEMBER_OF"`, `RelAttachedTo = "ATTACHED_TO"` and append all to `AllRelations`.
    - Use provider-neutral constant names and string values (no `_AWS` prefix/suffix). `ValidKind`/`ValidRelation` pick these up automatically via `kindSet`/`relationSet`.
    - _Requirements: 9.1_
  - [ ]* 1.2 Write unit test asserting allow-list membership and provider-neutral naming
    - Assert `ValidKind(KindNetwork)`, `ValidKind(KindEntryPoint)` and `ValidRelation` for each new relation return true, and that each new string value contains no provider prefix/suffix.
    - _Requirements: 9.1_
  - [ ] 1.3 Create migration `00012_graph_visibility.sql`
    - Create `internal/store/migrations/00012_graph_visibility.sql` following the `00003_resolution.sql` goose Up/Down pattern (BIGSERIAL id, `created_at TIMESTAMPTZ DEFAULT now()`).
    - Tables: `unresolved_edges` (relation, src_key, dst_key, unmatched_side, evidence_id, run_id) with indexes on relation/src_key/dst_key/run_id; `dropped_entities` (kind, provider, evidence_id, run_id) with indexes on kind/provider/run_id; `graph_write_gaps` (relation, sync_run_id, batch_size, observed) with indexes on relation/sync_run_id.
    - Down drops all three tables.
    - _Requirements: 1.5, 1.6, 2.3_
  - [ ] 1.4 Add store save/query methods in `internal/store/visibility.go`
    - New file following the `SaveConflicts` batch-insert pattern in `internal/store/resolution.go`. Define row types `UnresolvedEdgeRow`, `DroppedEntityRow`, `GraphWriteGapRow`.
    - Implement `SaveUnresolvedEdges`, `SaveDroppedEntities`, `SaveGraphWriteGaps` (each no-op on empty input, batched via `pgx.Batch`, returning errors like `SaveConflicts`).
    - Implement query helpers filtered by relation / src_key / dst_key / run_id (unresolved), kind / provider / run_id (dropped), relation / sync_run_id (gaps).
    - _Requirements: 1.5, 1.6, 2.3_
  - [ ] 1.5 Extend `Stats` and `GetStats` with the three visibility counts
    - In `internal/store/api.go`, add `UnresolvedEdges`, `DroppedEntities`, `GraphWriteGaps int64` to `Stats` with json tags; add three `SELECT count(*)` reads in `GetStats` using the existing `scalar` helper.
    - _Requirements: 1.7, 2.6_
  - [ ]* 1.6 Write property test for persistence round trip (DB-backed)
    - **Property 3: Visibility records survive a persistence round trip**
    - **Validates: Requirements 1.5, 1.6, 2.3**
    - In `internal/store`, save random record sets and assert querying by each supported dimension returns exactly the matching subset.
  - [ ]* 1.7 Write unit test for Stats counts
    - Insert known numbers of unresolved/dropped/gap rows and assert the three `Stats` fields equal the row counts.
    - _Requirements: 1.7, 2.6_

- [ ] 2. Resolver visibility: record unresolved edges and dropped entities
  - [ ] 2.1 Record unresolved edges and dropped entities in `Deterministic.Resolve`
    - In `internal/resolve/deterministic.go`, add `UnresolvedEdge` and `DroppedEntity` types and `Unresolved []UnresolvedEdge` / `Dropped []DroppedEntity` fields on `Deterministic`; reset both at the top of `Resolve` alongside `Aliases`/`Conflicts`.
    - In the entity loop, before the `if ent.Key == "" { continue }` skip, append a `DroppedEntity{Kind, Provider, EvidenceID}` (R1.4).
    - In the edge-rewrite loop, replace `if !okS || !okD { continue }` with recording one `UnresolvedEdge{Relation, SrcKey, DstKey, UnmatchedSide, EvidenceID}` where `UnmatchedSide` is `src` when only src failed, `dst` when only dst failed (src ok), `both` when both failed; still `continue` to exclude the edge (R1.1-R1.3). Add a `firstEvidenceID` helper.
    - _Requirements: 1.1, 1.2, 1.3, 1.4_
  - [ ] 2.2 Mirror the drop-recording in `PassThrough.Resolve`
    - In `internal/resolve/resolve.go`, apply the same empty-key and unresolved-endpoint recording so the pass-through resolver is consistent (fields on `PassThrough` or shared helper). Keep drop semantics unchanged.
    - _Requirements: 1.1, 1.2, 1.3, 1.4_
  - [ ] 2.3 Persist visibility records in `Processor.persistResolution`
    - In `internal/pipeline/process.go`, map `p.resolver.Unresolved`/`p.resolver.Dropped` into `store.UnresolvedEdgeRow`/`store.DroppedEntityRow` and call `st.SaveUnresolvedEdges`/`st.SaveDroppedEntities`, mirroring `SaveConflicts`. Stamp `RunID` from the run context the same way other normalized rows are tagged. Add counts to `ProcessResult`.
    - _Requirements: 1.5, 1.6_
  - [ ]* 2.4 Write property test for resolver no-silent-loss
    - **Property 1: Resolver loses no edge silently**
    - **Validates: Requirements 1.1, 1.2, 1.3**
    - In `internal/resolve`, generate entity/edge sets with controllable key-match rates; assert every input edge is either in resolved output or recorded exactly once, never both/neither, with correct `UnmatchedSide`.
  - [ ]* 2.5 Write property test for dropped-entity accounting
    - **Property 2: Empty-key entities are accounted for, not silently skipped**
    - **Validates: Requirements 1.4**

- [ ] 3. Graph writer visibility: sync-run id and gap detection
  - [ ] 3.1 Add sync-run id and gap detection to `SyncEdges`
    - In `internal/graph/graph.go`, define `WriteGap{Relation, SyncRunID, BatchSize, Observed}`; change `SyncEdges` to return `(total int, gaps []WriteGap, err error)`.
    - Generate one `syncRunID` (UUID) per invocation. Extend the per-relation Cypher to `RETURN count(r) AS written`; read `written` from the result. When `written < len(chunk)`, append one `WriteGap` and continue to the next batch (non-fatal). Keep real driver/transport errors fatal as today.
    - _Requirements: 2.1, 2.2, 2.4, 2.5_
  - [ ] 3.2 Persist gaps from `Processor.Sync`
    - In `internal/pipeline/process.go`, capture the gaps returned by `SyncEdges`, map to `store.GraphWriteGapRow`, and call `st.SaveGraphWriteGaps`. Keeps the graph package free of a Postgres dependency.
    - _Requirements: 2.3, 2.6_
  - [ ]* 3.3 Write property test for gap accounting (mock-count writer, no live Memgraph)
    - **Property 4: Graph-write gaps are accounted for and non-fatal**
    - **Validates: Requirements 2.1, 2.2, 2.4, 2.5**
    - Drive the gap-accounting logic with sequences of (batch size, observed count) pairs; assert one gap per strictly-under batch carrying relation/sync-run/size/observed, every batch processed, no error from a gap.

- [ ] 4. Checkpoint - visibility layer complete
  - Ensure all tests pass, ask the user if questions arise.

- [ ] 5. Robust AWS key normalization
  - [ ] 5.1 Implement `normalizeAWSARN` in `internal/resolve/aliases.go`
    - Add an AWS branch to `NormalizeKey`: when `provider == "aws"` and `key` has an `arn:` prefix, return `normalizeAWSARN(key)`.
    - `normalizeAWSARN` parses `arn:<partition>:<service>:<region>:<account>:<resource>`; rewrites the partition segment to canonical `aws` (from `aws`/`aws-cn`/`aws-us-gov`) (R3.2); for IAM `role/`/`user/` resources strips path segments between the resource-type separator and the bare resource name (R3.1); preserves exact casing of the resource-name segment with no case-folding or wildcarding (R3.6). Idempotent by construction.
    - _Requirements: 3.1, 3.2, 3.6_
  - [ ]* 5.2 Write property test: IAM/partition-insensitive ARNs normalize equal
    - **Property 5: IAM/partition-insensitive ARNs normalize to the same key**
    - **Validates: Requirements 3.1, 3.2**
    - Build a reusable AWS-ARN generator (partition/service/account/path/resource-name parameters).
  - [ ]* 5.3 Write property test: ARN normalization is idempotent
    - **Property 6: ARN normalization is idempotent**
    - **Validates: Requirements 3.1, 3.2**
  - [ ]* 5.4 Write property test: case-sensitive segments never folded/wildcarded
    - **Property 7: Case-sensitive segments are never folded or wildcarded**
    - **Validates: Requirements 3.6**

- [ ] 6. Instance-profile → role mapping
  - [ ] 6.1 Implement `Instance_Profile_Mapper`
    - Create `internal/resolve/iam/aws/instanceprofile.go` with `InstanceProfileMapper{ profiles map[string][]string }` and `Resolve(profileARN) (roles []string, confidence float64, fallback bool)`. Normalize ARNs the same way as `Key_Normalizer` so partition/path variants match collected data.
    - When the profile maps to >=1 role, return those roles with confidence 1.0, fallback=false. When it maps to none, return the name-substitution-derived role (`:instance-profile/` → `:role/`) with confidence 0.7, fallback=true.
    - _Requirements: 3.3, 3.5_
  - [ ] 6.2 Add `aws_iam_instance_profile` catalog entry and wire the mapper into `AWS.Normalize`
    - Add `aws_iam_instance_profile` (id_column `arn`, scope_column `account_id`, capability `core`) to `internal/source/steampipe/catalog/aws.yaml` (R3.4).
    - In `internal/normalize/aws.go` first pass, build `profileARN -> []roleARN` from `aws_iam_instance_profile` events; replace the crude `instanceProfileRole` call in the `aws_ec2_instance` branch with `InstanceProfileMapper.Resolve`, emitting one `USES` edge per contained role (confidence 1.0), or the fallback edge (confidence 0.7, attribute `"method":"name_substitution"`).
    - _Requirements: 3.3, 3.4, 3.5_
  - [ ]* 6.3 Write property test: one USES edge per contained role at confidence 1.0
    - **Property 8: Instance-profile mapping yields one USES edge per contained role**
    - **Validates: Requirements 3.3**
  - [ ]* 6.4 Write property test: fallback is confidence-tagged
    - **Property 9: Instance-profile fallback is confidence-tagged**
    - **Validates: Requirements 3.5**

- [ ] 7. Generalize the IAM access evaluator (data-driven action catalog)
  - [ ] 7.1 Extract the action catalog into `internal/resolve/iam/aws/catalog.go`
    - Define `ActionCatalog{ Service string; ResourceForms func(arn string) []string; Representative []string; Classify map[string]Access; AdminMarkers []string; WriteMarkers []string }` as pure data, and `var Catalogs = map[string]ActionCatalog{}`.
    - Populate `Catalogs["s3"]` so it reproduces today's behavior from `policy.go` exactly: `ResourceForms` → `{bucket, bucket+"/*"}`, `Representative` → the existing S3 catalog list, admin/write markers matching `classify`/`isWriteAction`.
    - _Requirements: 5.2, 9.2_
  - [ ] 7.2 Make `Evaluate` resource-kind-agnostic; keep `EvaluateS3` as a shim
    - In `internal/resolve/iam/aws/policy.go`, add `Evaluate(doc *PolicyDocument, targetARN string, cat ActionCatalog) (Access, []string)` applying the existing Allow/Deny-with-explicit-deny-override, `resourceMatchesAny`/`globMatch` resource matching, catalog-`Representative` wildcard-action expansion, and admin>write>read precedence from `AdminMarkers`/`WriteMarkers`/`Classify`.
    - Rewrite `EvaluateS3(doc, arn)` as `return Evaluate(doc, arn, Catalogs["s3"])`. The service-match check (was `isS3Action`) becomes catalog-service-aware.
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.8_
  - [ ]* 7.3 Write property test: generalized evaluator reproduces S3 behavior
    - **Property 15: Generalized evaluator reproduces S3 behavior (model-based)**
    - **Validates: Requirements 5.1**
    - Compare `Evaluate(doc, arn, Catalogs["s3"])` against a snapshot of pre-refactor `EvaluateS3` behavior over generated policy docs; keep existing `policy` unit tests green.
  - [ ]* 7.4 Write property test: admin-over-write-over-read precedence
    - **Property 16: Access classification obeys admin-over-write-over-read precedence**
    - **Validates: Requirements 5.2**
  - [ ]* 7.5 Write property tests: edge presence, deny removal, parse resilience, no-catalog
    - **Property 17: Access edges appear exactly when access is granted** (Validates: Requirements 5.3, 5.4)
    - **Property 18: Explicit resource Deny removes the denied action** (Validates: Requirements 5.5)
    - **Property 19: An unparseable policy document does not affect the others** (Validates: Requirements 5.6)
    - **Property 20: A resource kind with no action catalog produces no access edge** (Validates: Requirements 5.8)
    - Include a malformed-policy-document generator.

- [ ] 8. IAM trust-chain modeling (CAN_ASSUME)
  - [ ] 8.1 Implement `Trust_Policy_Analyzer`
    - Create `internal/resolve/iam/aws/trust.go` reusing `PolicyDocument`/`Statement`/`stringOrSlice`; add `Principal` parsing to the statement model.
    - Compute the deny set first (principals denied `sts:AssumeRole*`), then generate Allow-derived edges only for principals not denied (R4.5). Supported actions: `sts:AssumeRole`, `sts:AssumeRoleWithSAML`, `sts:AssumeRoleWithWebIdentity`.
    - Per-case output: named IAM user/role principal → `CAN_ASSUME` principal→role, confidence 1.0 (R4.1); account-root ARN or bare 12-digit id → `CAN_ASSUME` CloudAccount→role, confidence 0.7, attribute `account_grant:true` (R4.2); service principal → excluded (R4.3); wildcard `*` → one edge from synthetic key `aws:any-identity`→role, confidence 0.7, attribute `unscoped:true` (R4.4). Edges use the generic `model.Edge` shape (R9.3). Cross-account and undiscovered principals still emit edges keyed to the principal ARN (R4.6, R4.7).
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 9.3_
  - [ ] 8.2 Wire the analyzer into the `aws_iam_role` branch of `AWS.Normalize`
    - In `internal/normalize/aws.go`, parse `assume_role_policy_std` for each `aws_iam_role` event and append the analyzer's `CAN_ASSUME` edges. Ensure the synthetic `aws:any-identity` principal entity is emitted (analog to the synthetic internet endpoint) so wildcard edges resolve. An unparseable trust policy yields no edges, non-fatally.
    - _Requirements: 4.1, 4.4, 4.7_
  - [ ]* 8.3 Write property tests for trust-policy edge generation
    - **Property 10: Named-principal trust grants produce CAN_ASSUME edges regardless of account or discovery** (Validates: Requirements 4.1, 4.6, 4.7)
    - **Property 11: Account-principal trust grants are account-scoped and labeled** (Validates: Requirements 4.2)
    - **Property 12: Service principals never produce CAN_ASSUME edges** (Validates: Requirements 4.3)
    - **Property 13: Wildcard principal produces one unscoped synthetic edge** (Validates: Requirements 4.4)
    - **Property 14: Explicit trust Deny overrides Allow for the same principal** (Validates: Requirements 4.5)
    - Build a trust-policy generator (named/account/service/wildcard principals, overlapping Allow/Deny).

- [ ] 9. Checkpoint - identity/IAM coverage complete
  - Ensure all tests pass, ask the user if questions arise.

- [ ] 10. AWS network topology (AWSNetwork normalizer)
  - [ ] 10.1 Implement the `AWSNetwork` normalizer and `network_subtype` vocabulary
    - Create `internal/normalize/awsnetwork.go`, type `AWSNetwork` (distinct from the Zeek `Network` type in `network.go`), implementing `Normalize`/`Sources` and consuming `provider == "aws"` events for the networking tables.
    - Declare the nine `network_subtype` constants (vpc, subnet, route_table, internet_gateway, nat_gateway, peering_connection, network_interface, transit_gateway, security_group) in one place with a doc comment stating the vocabulary is open-ended (R9.4). Map each construct to one `KindNetwork` entity with its distinct `network_subtype`.
    - _Requirements: 6.1, 6.8, 9.4_
  - [ ] 10.2 Emit topology edges
    - CONTAINS (VPC→Subnet) (R6.2); ATTACHED_TO (Subnet→RouteTable) (R6.3); ROUTES_TO (RouteTable→IGW/NAT/Peering/TGW-attachment target) only for those supported targets (R6.4); exactly one PEERS_WITH (requester VPC→accepter VPC) per active peering connection (R6.5); ATTACHED_TO (ENI→Compute) only when attachment state is currently-attached, excluding unattached/detaching/pending (R6.6); MEMBER_OF (Compute/ENI→SG) (R6.7). Edges to not-yet-discovered endpoints are emitted and left to Requirement 1.
    - _Requirements: 6.2, 6.3, 6.4, 6.5, 6.6, 6.7_
  - [ ] 10.3 Reconcile existing EXPOSED_TO / security-group handling with the SG Network node
    - In `internal/normalize/aws.go`, point the existing `exposedToEdges` / SG handling at the new SG `Network` entity (`network_subtype: security_group`) so the SG participates in both `MEMBER_OF` and `EXPOSED_TO` rather than being only a `SrcKey` string.
    - _Requirements: 6.8_
  - [ ] 10.4 Register `AWSNetwork` and add the eight networking catalog tables
    - Register `normalize.AWSNetwork{}` in `internal/pipeline/process.go`.
    - Add `aws_vpc`, `aws_subnet`, `aws_vpc_route_table`, `aws_vpc_internet_gateway`, `aws_vpc_nat_gateway`, `aws_vpc_peering_connection`, `aws_ec2_network_interface`, `aws_ec2_transit_gateway` to `aws.yaml` with id/scope/region columns per the design table, under `core` (ec2:Describe* already granted; SG already present).
    - _Requirements: 6.9_
  - [ ]* 10.5 Write property tests for network mapping and edges
    - **Property 21: Each network construct maps to one Network entity with a distinct subtype** (Validates: Requirements 6.1, 6.8)
    - **Property 22: Subnet-in-VPC yields a CONTAINS edge** (Validates: Requirements 6.2)
    - **Property 23: Route-table entries route only to supported targets** (Validates: Requirements 6.4)
    - **Property 24: Active peering yields exactly one PEERS_WITH per connection** (Validates: Requirements 6.5)
    - Build per-table Steampipe event fixtures/generators.

- [ ] 11. AWS data store coverage expansion
  - [ ] 11.1 Implement `Data_Store_Normalizer` helpers and wire into `AWS.Normalize`
    - Create `internal/normalize/awsdatastore.go` with helpers mapping `aws_rds_db_instance`, `aws_rds_db_cluster`, `aws_dynamodb_table`, `aws_efs_file_system`, `aws_elasticache_cluster` each to one `KindDataStore` entity carrying a `datastore_subtype` attribute (parallel to `compute_subtype`). Invoke from new cases in `AWS.Normalize`.
    - Emit one CONTAINS (account→DataStore) per store via the existing `containsEdge`; an RDS instance that is a cluster member gets its own CONTAINS independent of the cluster's (R7.6).
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 7.6_
  - [ ] 11.2 Add data-store action catalog entries so the evaluator covers them
    - Add `Catalogs["dynamodb"]`, `Catalogs["rds"]`, `Catalogs["efs"]`, `Catalogs["elasticache"]` entries in `catalog.go` (data only, no evaluator change) and iterate these DataStore targets in the `AWS.Normalize` second pass alongside S3.
    - _Requirements: 5.7, 7.1-7.5_
  - [ ] 11.3 Add the five data-store catalog tables and wire ENI/SG edges
    - Add `aws_rds_db_instance`, `aws_rds_db_cluster`, `aws_dynamodb_table`, `aws_efs_file_system`, `aws_elasticache_cluster` to `aws.yaml` under `dspm` (R7.7).
    - Ensure `AWSNetwork` emits ATTACHED_TO (ENI→DataStore) and MEMBER_OF (DataStore→SG) using the same edge shapes as R6.6/R6.7 (R7.8, R7.9).
    - _Requirements: 7.7, 7.8, 7.9_
  - [ ]* 11.4 Write property tests for data-store mapping and attachment/membership generalization
    - **Property 27: Each supported data store maps to one DataStore entity with a distinct subtype** (Validates: Requirements 7.1-7.5)
    - **Property 28: Each DataStore gets an independent account CONTAINS edge** (Validates: Requirements 7.6)
    - **Property 25: Attachment edges track attachment state over Compute and DataStore endpoints** (Validates: Requirements 6.6, 7.8)
    - **Property 26: Security-group membership yields a MEMBER_OF edge over Compute and DataStore members** (Validates: Requirements 6.7, 7.9)

- [ ] 12. AWS entry-point modeling (API Gateway)
  - [ ] 12.1 Implement `Entry_Point_Normalizer`
    - Create `internal/normalize/awsentrypoint.go` mapping each `aws_api_gateway_rest_api` and `aws_api_gatewayv2_api` to exactly one `KindEntryPoint` entity regardless of stage/resource/route/method count (R8.1).
    - EXPOSED_TO (EntryPoint→synthetic internet `0.0.0.0/0`) when a REST API is not PRIVATE-endpoint-restricted; omit when PRIVATE (R8.2, R8.4); HTTP APIs always emit EXPOSED_TO (R8.5), reusing the existing synthetic internet node.
    - CALLS (EntryPoint→each distinct target Lambda `Compute`) for Lambda-target integrations, deduped per function ARN (R8.3). CONTAINS (account→EntryPoint) via `containsEdge` (R8.6).
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.5, 8.6_
  - [ ] 12.2 Register the normalizer and add the two API Gateway catalog tables
    - Register `normalize.EntryPoint{}` (name TBD) in `internal/pipeline/process.go`.
    - Add `aws_api_gateway_rest_api` and `aws_api_gatewayv2_api` to `aws.yaml` under `workload` (R8.7).
    - _Requirements: 8.7_
  - [ ]* 12.3 Write property tests for entry-point mapping
    - **Property 29: Each API maps to exactly one EntryPoint** (Validates: Requirements 8.1, 8.6)
    - **Property 30: Internet exposure follows the endpoint configuration** (Validates: Requirements 8.2, 8.4, 8.5)
    - **Property 31: Lambda-backed APIs produce one CALLS edge per distinct target** (Validates: Requirements 8.3)

- [ ] 13. Capability gating for new read-only actions
  - [ ] 13.1 Add EFS/ElastiCache/API Gateway read-only actions to capability definitions
    - In `internal/connector/capability/capability.go`, add the EFS/ElastiCache `Describe*` read actions to the `dspm` capability bundle and the API Gateway `GET` read actions to the `workload` bundle, so the new `dspm`/`workload`-gated tables collect without AccessDenied. (VPC/EC2 network tables and `aws_iam_instance_profile` need no change — `ec2:Describe*`/IAM `List*`/`Get*` are already in `core`.)
    - _Requirements: 6.9, 7.7, 8.7_
  - [ ]* 13.2 Write unit test asserting new catalog tables carry the expected columns/capability
    - Parse `aws.yaml` and assert each new table entry exists with the expected `id_column`/`scope_column`/`capability`.
    - _Requirements: 3.4, 6.9, 7.7, 8.7_

- [ ] 14. End-to-end integration and final wiring
  - [ ] 14.1 Write integration tests across the normalize→resolve→sync path
    - In `internal/e2e` (or an integration test alongside the pipeline), prove: (a) a previously-dropped edge is now recorded as an `Unresolved_Edge_Record`; (b) a `CAN_ASSUME` chain resolves end-to-end; (c) a graph-write gap is recorded with `observed < batch_size` and a non-fatal return. Reuse the gap-accounting coverage from Property 4 for the pure logic; this task exercises wiring.
    - _Requirements: 1.1, 1.3, 2.2, 4.1_
  - [ ] 14.2 Final wiring, build, and full test suite
    - Confirm all new normalizers/analyzers are registered and all new store methods/migration are reachable from the pipeline with no orphaned code. Run `go build ./...` and the full test suite (`go test ./...`), fixing any failures.
    - _Requirements: 9.1, 9.5_

- [ ] 15. Final checkpoint - ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are test sub-tasks: optional for a faster MVP, but each maps to a specific design property or acceptance criterion.
- Property tests use `pgregory.net/rapid`, min 100 iterations, one test per property, tagged `// Feature: cloud-attack-path-graph, Property {n}: {text}`.
- Review-only items (R9.2 data/logic separation, R9.3 generic edge shape, R9.4 open-ended `network_subtype` doc comment, R9.5 AWS-only scope) are confirmed during code review against the design, not by a test — they are embedded as constraints in tasks 1.1, 7.1, 8.1, 10.1.
- Each task references specific requirements/properties for traceability. Approximate IAM/trust edges carry `confidence 0.7` and `effective:"approx"`; deterministic edges carry `confidence 1.0`.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "1.2", "1.3"] },
    { "id": 1, "tasks": ["1.4", "1.5"] },
    { "id": 2, "tasks": ["1.6", "1.7", "2.1", "3.1"] },
    { "id": 3, "tasks": ["2.2", "2.3", "3.2", "2.4", "2.5"] },
    { "id": 4, "tasks": ["3.3", "5.1"] },
    { "id": 5, "tasks": ["5.2", "5.3", "5.4", "6.1", "7.1"] },
    { "id": 6, "tasks": ["6.2", "7.2"] },
    { "id": 7, "tasks": ["6.3", "6.4", "7.3", "7.4", "7.5", "8.1"] },
    { "id": 8, "tasks": ["8.2", "10.1"] },
    { "id": 9, "tasks": ["8.3", "10.2", "10.3"] },
    { "id": 10, "tasks": ["10.4", "11.1"] },
    { "id": 11, "tasks": ["10.5", "11.2", "11.3", "12.1"] },
    { "id": 12, "tasks": ["11.4", "12.2", "13.1"] },
    { "id": 13, "tasks": ["12.3", "13.2", "14.1"] },
    { "id": 14, "tasks": ["14.2"] }
  ]
}
```
