# Requirements Document

## Introduction

Bellu's discovery pipeline (Steampipe → RawEvent → Normalizer → EntityResolver → GraphWriter) already discovers cloud entities and links a subset of them into a graph (Postgres `normalized_entities`/`normalized_edges`, synced to Memgraph). The pipeline today supports basic exposure checks (security-group-open-to-world, S3-only IAM read/write/admin), but it cannot answer attack-path or blast-radius questions such as "what can an attacker reach from this exposed resource" with confidence, for two reasons:

1. **Silent edge loss.** Edge resolution in `internal/resolve` drops any edge whose source or destination key does not resolve to a known entity, with no logging or recorded trace (`internal/resolve/deterministic.go`, `internal/resolve/resolve.go`). The graph writer's `SyncEdges` (`internal/graph/graph.go`) has a second, independent silent-drop point: its Cypher `MATCH...MATCH...MERGE` creates no relationship, and raises no error, when either endpoint node is missing in Memgraph. Entities with an empty `Key` are skipped before they are ever indexed, so any edge that references them always drops. Together, these make the graph appear complete when it is not.
2. **Incomplete relationship coverage.** The graph has no identity-to-identity trust-chain modeling (`sts:AssumeRole`), no network topology (VPC, Subnet, Route Table, Internet Gateway, VPC Peering, Network Interfaces, Transit Gateway), no IAM access evaluation beyond S3, no coverage of several common AWS data stores (RDS, DynamoDB, EFS, ElastiCache), and no modeling of internet-facing entry points such as API Gateway.

This feature addresses both problems for AWS, the priority provider, while keeping the model provider-agnostic so the same relationship taxonomy extends to GCP and Azure in later phases.

The scope of AWS resource and relationship coverage targeted by this feature (identity trust chains, IAM access evaluation, network topology, data store coverage, internet-facing entry points) is informed by the publicly documented coverage of the [Cartography project](https://github.com/cartography-cncf/cartography) (Apache License 2.0), used here only as a reference for **what** categories of cloud entities and relationships matter for attack-path analysis. Cartography is a Python project; Bellu's pipeline is Go, with its own entity model, resolver, and graph writer. This feature is an independent reimplementation of equivalent modeling concepts within Bellu's existing architecture, not a port or derivative of Cartography's code. *Content was rephrased for compliance with licensing restrictions.*

### Scope and Assumptions

- **AWS first, GCP/Azure phased.** Every resource-specific requirement in this document (network topology, data stores, entry points, trust chains) applies to AWS only. `internal/model/model.go` already keeps `Kind` and `Relation` provider-independent (a `Provider` field on `Entity`, not separate node labels per provider), so no requirement in this document is AWS-specific at the model-schema level — only at the normalizer/data-source level. GCP and Azure coverage of the same relationship categories is explicitly deferred to future phases and is not detailed resource-by-resource in this document.
- **Approximate evaluation stays labeled as approximate.** Consistent with the existing `effective: "approx"` / `confidence: 0.7` convention used for the current S3 IAM evaluator (`internal/resolve/iam/aws/policy.go`, `internal/normalize/cloudaccess.go`), every new IAM-derived or trust-derived edge that ignores SCPs, permission boundaries, or conditions carries the same approximate labeling. This feature does not require full IAM policy simulation (SCPs, permission boundaries, session policies, conditions) — that remains a documented future direction.
- **AWS Organizations/SCP hierarchy is out of scope** for this feature's first increment and is noted only as a future direction.
- **Visibility, not auto-repair.** Recording dropped/unresolved edges and write gaps makes resolution failures queryable; this feature does not require the resolver to guess or force a resolution when evidence is genuinely insufficient.

## Glossary

- **Edge_Resolver**: The resolution component in `internal/resolve` (`Deterministic.Resolve` and related functions) that maps edge `SrcKey`/`DstKey` values to canonical entity IDs before edges are persisted.
- **Graph_Writer**: The Memgraph sync component in `internal/graph` (`Writer.SyncEntities`, `Writer.SyncEdges`) that writes resolved entities and edges over the Bolt protocol.
- **Key_Normalizer**: The key-normalization logic (`internal/resolve/aliases.go` `NormalizeKey` and related ARN-handling functions) that collapses different native-key representations of the same AWS resource to one comparable form.
- **Unresolved_Edge_Record**: A persisted record describing one edge that the Edge_Resolver declined to resolve because its source key, destination key, or both did not match any known entity.
- **Graph_Write_Gap_Record**: A persisted record describing one batch of edges submitted to the Graph_Writer for which the number of relationships actually created or matched in Memgraph was less than the number of edges submitted in that batch.
- **Trust_Policy_Analyzer**: The component that parses an AWS IAM role's trust policy (`assume_role_policy_std`) and derives CAN_ASSUME edges between Identity entities.
- **CAN_ASSUME**: The canonical relation type representing that one Identity is permitted, per a trust policy, to assume another Identity (an IAM role) via `sts:AssumeRole` or `sts:AssumeRoleWithSAML`/`sts:AssumeRoleWithWebIdentity`.
- **IAM_Access_Evaluator**: The generalized policy evaluator (the successor to today's S3-only `internal/resolve/iam/aws/policy.go`) that classifies an identity's allowed access (read, write, admin) against a target resource's ARN for a given AWS resource kind.
- **Instance_Profile_Mapper**: The component that resolves an EC2 instance profile ARN to the IAM role ARN(s) it actually contains, using collected instance-profile data rather than string substitution.
- **Network_Normalizer**: The normalizer that maps AWS networking Steampipe tables (VPC, Subnet, Route Table, Internet Gateway, VPC Peering Connection, Network Interface, Transit Gateway, Security Group) to Network entities and their relationships.
- **Network**: The canonical node kind representing an AWS networking construct (VPC, Subnet, Route Table, Internet Gateway, NAT Gateway, Transit Gateway, VPC Peering Connection, Network Interface, or Security Group), distinguished by a `network_subtype` attribute.
- **ROUTES_TO**: The canonical relation type representing that a Route Table entry directs traffic toward a target (Internet Gateway, NAT Gateway, VPC Peering Connection, or Transit Gateway attachment).
- **PEERS_WITH**: The canonical relation type representing that two VPCs are connected by an active VPC Peering Connection.
- **MEMBER_OF**: The canonical relation type representing that a Compute resource or Network Interface is a member of a Security Group.
- **ATTACHED_TO**: The canonical relation type representing that a Network Interface is attached to a Compute resource, or that a Subnet is associated with a Route Table.
- **Data_Store_Normalizer**: The extension of the AWS normalizer that maps additional AWS data-store Steampipe tables (RDS instance/cluster, DynamoDB table, EFS file system, ElastiCache cluster) to DataStore entities.
- **Entry_Point_Normalizer**: The normalizer that maps internet-facing entry resources (starting with API Gateway) to EntryPoint entities and their backend-integration edges.
- **EntryPoint**: The canonical node kind representing a resource that accepts traffic originating from outside the AWS account's network boundary (for example, an API Gateway REST or HTTP API).
- **Steampipe_Catalog**: The table catalog in `internal/source/steampipe/catalog/aws.yaml` that declares which Steampipe tables are collected and under which Capability.
- **Dropped_Entity_Record**: A persisted record describing one entity a normalizer produced with an empty `Key`, logged by the Edge_Resolver before the entity is excluded from resolution.

## Requirements

### Requirement 1: Visibility into unresolved edges

**User Story:** As a security engineer, I want every edge that fails to resolve to be recorded and queryable, so that I can tell the difference between "no relationship exists" and "the pipeline silently lost a relationship."

#### Acceptance Criteria

1. IF the Edge_Resolver cannot match an edge's `SrcKey` to a known entity, THEN THE Edge_Resolver SHALL create one Unresolved_Edge_Record carrying the edge's relation, `SrcKey`, `DstKey`, an unmatched-side indicator set to `src` (or to `both` if `DstKey` also fails to match), and the edge's evidence reference.
2. IF the Edge_Resolver cannot match an edge's `DstKey` to a known entity and `SrcKey` matched successfully, THEN THE Edge_Resolver SHALL create one Unresolved_Edge_Record carrying the edge's relation, `SrcKey`, `DstKey`, an unmatched-side indicator set to `dst`, and the edge's evidence reference.
3. IF either endpoint of an edge is unresolved, THEN THE Edge_Resolver SHALL exclude that edge from the resolved output, in addition to recording its Unresolved_Edge_Record.
4. IF a normalizer produces an entity whose `Key` is empty, THEN THE Edge_Resolver SHALL create one Dropped_Entity_Record carrying the entity's Kind, Provider, and evidence reference, before excluding the entity from resolution.
5. THE Store SHALL persist Unresolved_Edge_Records in Postgres in a form queryable by relation, by `SrcKey` or `DstKey`, and by run, following the existing pattern used for `resolution_conflicts` (`internal/store/migrations/00003_resolution.sql`).
6. THE Store SHALL persist Dropped_Entity_Records in Postgres in a form queryable by Kind, by Provider, and by run, following the same existing pattern used for `resolution_conflicts` (`internal/store/migrations/00003_resolution.sql`).
7. THE Store SHALL make the count of Unresolved_Edge_Records and the count of Dropped_Entity_Records available as separate counts alongside the existing graph statistics (`internal/store/api.go` `Stats`), so pipeline health can be assessed without a separate query tool.

### Requirement 2: Visibility into graph-write gaps

**User Story:** As a security engineer, I want to know when a resolved edge was accepted by the resolver but still failed to materialize in Memgraph, so that the attack-path graph is not silently incomplete even after resolution succeeds.

#### Acceptance Criteria

1. WHEN the Graph_Writer submits a batch of edges to Memgraph via `SyncEdges`, THE Graph_Writer SHALL assign a unique sync-run identifier to that `SyncEdges` invocation and SHALL compare the number of relationships the write actually created or matched against the number of edges submitted in that batch.
2. IF the number of relationships created or matched in a batch is less than the number of edges submitted, THEN THE Graph_Writer SHALL create one Graph_Write_Gap_Record per batch identifying the relation type, the sync-run identifier, the batch size, and the observed count.
3. THE Store SHALL persist Graph_Write_Gap_Records in a form queryable by relation type and by sync-run identifier.
4. THE Graph_Writer SHALL NOT raise a batch write as a fatal error solely because of a graph-write gap.
5. THE Graph_Writer SHALL continue syncing subsequent batches within the same sync run after recording a Graph_Write_Gap_Record for an earlier batch.
6. THE Store SHALL make the total count of Graph_Write_Gap_Records across all sync runs available alongside the existing graph statistics (`internal/store/api.go` `Stats`).

### Requirement 3: Robust AWS key normalization

**User Story:** As a security engineer, I want ARN and resource-key comparisons to tolerate the representational variants AWS produces, so that edges stop being dropped due to superficial key mismatches rather than genuine missing relationships.

#### Acceptance Criteria

1. THE Key_Normalizer SHALL compute a path-stripped form of an IAM ARN (removing any path segments between the resource-type separator and the resource name) and SHALL use that path-stripped form, combined with account id and resource name, to match two IAM role or user ARNs that differ only in IAM path.
2. IF an AWS ARN is observed with a partition segment other than `aws` (for example `aws-cn` or `aws-us-gov`), THEN THE Key_Normalizer SHALL normalize the partition segment to a canonical form before comparison, such that two ARNs with the same account id and resource id but different partition segments are treated as a match.
3. WHEN an EC2 instance's `aws_ec2_instance.iam_instance_profile_arn` is present, THE Instance_Profile_Mapper SHALL resolve it to the IAM role ARN(s) contained in that instance profile using collected instance-profile membership data; WHEN the instance profile contains exactly one role, THE Instance_Profile_Mapper SHALL use that role's ARN; WHEN the instance profile contains more than one role, THE Instance_Profile_Mapper SHALL create one USES edge per contained role.
4. THE Steampipe_Catalog SHALL collect the `aws_iam_instance_profile` table (or equivalent instance-profile-to-role mapping data) so the Instance_Profile_Mapper has the data it needs for Requirement 3.3.
5. IF the Instance_Profile_Mapper cannot resolve an instance profile ARN to any role ARN from collected data, THEN THE Instance_Profile_Mapper SHALL fall back to the existing name-substitution heuristic and SHALL set the resulting USES edge's confidence to 0.7 rather than the 1.0 confidence used for a mapping-table-derived resolution.
6. THE Key_Normalizer SHALL compare resource name segments that AWS treats as case-sensitive (for example S3 bucket names and IAM role/user names) using exact, case-sensitive string equality, and SHALL NOT apply case-folding or wildcard matching to these segments.

### Requirement 4: IAM trust-chain modeling (CAN_ASSUME)

**User Story:** As a security engineer, I want IAM role trust relationships modeled as graph edges, so that I can trace lateral-movement and privilege-escalation paths between identities.

#### Acceptance Criteria

1. WHEN an AWS IAM role's trust policy (`assume_role_policy_std`) contains an Allow statement whose Action includes `sts:AssumeRole`, `sts:AssumeRoleWithSAML`, or `sts:AssumeRoleWithWebIdentity` and whose Principal is an AWS IAM user ARN or IAM role ARN, THE Trust_Policy_Analyzer SHALL create one CAN_ASSUME edge from the Principal identity to the role, for each such named Principal, with confidence 1.0.
2. WHEN a trust policy's Principal is an AWS account root ARN (for example `arn:aws:iam::111122223333:root`) or a bare 12-digit AWS account id, THE Trust_Policy_Analyzer SHALL create a CAN_ASSUME edge from the CloudAccount entity for that account to the role, with confidence 0.7, and SHALL set an attribute on the edge recording that the grant applies to the account's identities broadly rather than to one named identity.
3. WHEN a trust policy's Principal is an AWS service principal (for example `ec2.amazonaws.com`, `lambda.amazonaws.com`), THE Trust_Policy_Analyzer SHALL exclude that statement from CAN_ASSUME edge generation, because a service principal is not an attacker-controllable identity in this model.
4. WHEN a trust policy's Principal is the wildcard value `*`, THE Trust_Policy_Analyzer SHALL create one CAN_ASSUME edge from a synthetic "any identity" principal key to the role, with confidence 0.7, and SHALL set an attribute on the edge recording that the grant is unscoped.
5. IF a trust policy statement's Effect is Deny for a given Principal, THEN THE Trust_Policy_Analyzer SHALL exclude that Principal from CAN_ASSUME edge generation for this role even when another statement in the same policy has an Allow Effect for that same Principal.
6. WHEN a trust policy's Principal ARN references an identity in a different AWS account than the role being trusted, THE Trust_Policy_Analyzer SHALL still create the CAN_ASSUME edge, so that cross-account assume-role paths are visible even when the trusting and trusted accounts are discovered in different runs.
7. IF a trust policy's Principal ARN references an identity that has not been discovered by any normalizer, THEN THE Trust_Policy_Analyzer SHALL emit the CAN_ASSUME edge keyed to that Principal ARN and rely on Requirement 1 to make the resulting unresolved reference visible, rather than silently discarding the trust relationship.

### Requirement 5: Generalized IAM access evaluation

**User Story:** As a security engineer, I want IAM access evaluation to cover every resource kind the pipeline already discovers, so that blast-radius queries are not limited to S3.

#### Acceptance Criteria

1. THE IAM_Access_Evaluator SHALL evaluate an identity's attached and inline policy statements against a target resource ARN for any AWS resource kind that has both an ARN and a defined action catalog, using the same Allow/Deny, wildcard-resource, and wildcard-action matching rules already implemented for S3.
2. WHEN the IAM_Access_Evaluator classifies allowed actions on a target resource, THE IAM_Access_Evaluator SHALL use a resource-kind-specific action catalog (equivalent in structure to the existing S3 action catalog) to classify the result as read, write, or admin access, applying admin-over-write-over-read precedence when allowed actions span more than one classification tier.
3. IF the set of allowed, non-denied actions for an identity against a target resource is non-empty, THEN THE IAM_Access_Evaluator SHALL produce one CAN_READ, CAN_WRITE, or CAN_ADMIN edge (per the classification in Criterion 2) from the identity to the target resource, using the same edge shape, confidence (0.7), and `effective: "approx"` attribute convention as the existing S3 edges.
4. IF the set of allowed, non-denied actions for an identity against a target resource is empty, THEN THE IAM_Access_Evaluator SHALL NOT produce a CAN_READ, CAN_WRITE, or CAN_ADMIN edge for that identity-resource pair.
5. IF an explicit Deny statement matches a target resource and action for a resource kind, THEN THE IAM_Access_Evaluator SHALL exclude the denied action from the allowed-action set for that resource kind, consistent with existing S3 behavior.
6. IF an identity's policy document cannot be parsed, THEN THE IAM_Access_Evaluator SHALL exclude that policy document from the evaluation and SHALL continue evaluating the identity's other policy documents, consistent with existing S3 behavior.
7. WHERE a new AWS data store kind is added to the Data_Store_Normalizer (Requirement 7), THE IAM_Access_Evaluator SHALL evaluate identity access against that data store kind by adding a new action-catalog entry for that resource kind, without requiring a new evaluator implementation.
8. IF an AWS resource kind the normalizer discovers has no defined action catalog, THEN THE IAM_Access_Evaluator SHALL NOT produce any CAN_READ, CAN_WRITE, or CAN_ADMIN edge for that resource kind.

### Requirement 6: AWS network topology modeling

**User Story:** As a security engineer, I want VPC-level network topology modeled as graph entities and relationships, so that I can trace whether an exposed resource is actually network-reachable, not just whether its security group allows it.

#### Acceptance Criteria

1. THE Network_Normalizer SHALL map each discovered AWS VPC, Subnet, Route Table, Internet Gateway, NAT Gateway, VPC Peering Connection, Network Interface, Transit Gateway, and Security Group to one Network entity, setting that entity's `network_subtype` attribute to one of nine distinct values, one per resource type listed, so that any two Network entities of different resource types are distinguishable by `network_subtype` alone.
2. WHEN a Subnet belongs to a VPC, THE Network_Normalizer SHALL create a CONTAINS edge from the VPC's Network entity to the Subnet's Network entity.
3. WHEN a Route Table is associated with a Subnet, THE Network_Normalizer SHALL create an ATTACHED_TO edge from the Subnet's Network entity to the Route Table's Network entity.
4. WHEN a Route Table entry's target is an Internet Gateway, NAT Gateway, VPC Peering Connection, or Transit Gateway attachment, THE Network_Normalizer SHALL create a ROUTES_TO edge from the Route Table's Network entity to the target's Network entity.
5. WHEN a VPC Peering Connection is active between two VPCs, THE Network_Normalizer SHALL create exactly one PEERS_WITH edge, from the requesting VPC's Network entity to the accepter VPC's Network entity, per active VPC Peering Connection.
6. WHEN a Network Interface's attachment state indicates it is currently attached to a Compute resource (excluding Network Interfaces reported as unattached, detaching, or pending-attachment), THE Network_Normalizer SHALL create an ATTACHED_TO edge from the Network Interface's Network entity to the Compute entity.
7. WHEN a Compute resource or Network Interface is a member of a Security Group, THE Network_Normalizer SHALL create a MEMBER_OF edge from the Compute or Network Interface entity to the Security Group's Network entity.
8. THE Network_Normalizer SHALL represent each Security Group as a Network entity rather than only as a `SrcKey` string, so a Security Group can participate in both MEMBER_OF and the existing EXPOSED_TO relationship.
9. THE Steampipe_Catalog SHALL add table entries for `aws_vpc`, `aws_subnet`, `aws_vpc_route_table`, `aws_vpc_internet_gateway`, `aws_vpc_nat_gateway`, `aws_vpc_peering_connection`, `aws_ec2_network_interface`, and `aws_ec2_transit_gateway`, each under an existing or newly declared Capability consistent with the Steampipe_Catalog's capability-gating convention.

### Requirement 7: AWS data store coverage expansion

**User Story:** As a security engineer, I want the data stores an organization actually uses beyond S3 to be visible in the graph, so that blast-radius analysis to data is not limited to S3 buckets.

#### Acceptance Criteria

1. THE Data_Store_Normalizer SHALL map each discovered AWS RDS instance to a DataStore entity.
2. THE Data_Store_Normalizer SHALL map each discovered AWS RDS cluster to a DataStore entity.
3. THE Data_Store_Normalizer SHALL map each discovered AWS DynamoDB table to a DataStore entity.
4. THE Data_Store_Normalizer SHALL map each discovered AWS EFS file system to a DataStore entity.
5. THE Data_Store_Normalizer SHALL map each discovered AWS ElastiCache cluster to a DataStore entity.
6. THE Data_Store_Normalizer SHALL create a CONTAINS edge from the owning CloudAccount entity to each DataStore entity created under Criteria 1 through 5, consistent with the existing S3 CONTAINS pattern, creating a CONTAINS edge for an RDS instance that is a member of an RDS cluster independently of the CONTAINS edge created for that cluster's own DataStore entity.
7. THE Steampipe_Catalog SHALL add table entries for `aws_rds_db_instance`, `aws_rds_db_cluster`, `aws_dynamodb_table`, `aws_efs_file_system`, and `aws_elasticache_cluster`, each under an existing or newly declared Capability.
8. WHEN a Network Interface is attached to a DataStore entity created under Criteria 1 through 5, THE Network_Normalizer SHALL create an ATTACHED_TO edge from the Network Interface's Network entity to that DataStore entity, using the same edge shape Requirement 6.6 defines for a Network Interface attached to a Compute resource.
9. WHEN a DataStore entity created under Criteria 1 through 5 is a member of a Security Group, THE Network_Normalizer SHALL create a MEMBER_OF edge from the DataStore entity to the Security Group's Network entity, using the same edge shape Requirement 6.7 defines for a Compute resource that is a member of a Security Group.

### Requirement 8: AWS entry-point modeling

**User Story:** As a security engineer, I want internet-facing entry points modeled explicitly, so that I can start an attack-path query from "the internet" rather than only from a security group flagged as open.

#### Acceptance Criteria

1. THE Entry_Point_Normalizer SHALL map each discovered AWS API Gateway REST API and HTTP API resource to exactly one EntryPoint entity, regardless of the number of stages, resources, routes, or methods defined within that API.
2. IF an API Gateway REST API's endpoint configuration is not restricted to a VPC-only (private) endpoint, THEN THE Entry_Point_Normalizer SHALL create an EXPOSED_TO edge from that REST API's EntryPoint entity to the existing synthetic internet NetworkEndpoint (`0.0.0.0/0`), consistent with the existing security-group exposure convention.
3. WHEN an API Gateway REST API or HTTP API has one or more integrations whose target is a Lambda function, THE Entry_Point_Normalizer SHALL create one CALLS edge from that API's EntryPoint entity to each distinct target Lambda function's Compute entity.
4. WHERE an API Gateway REST API's endpoint configuration is restricted to a VPC-only (private) endpoint, THE Entry_Point_Normalizer SHALL omit the internet EXPOSED_TO edge for that EntryPoint entity.
5. THE Entry_Point_Normalizer SHALL create an EXPOSED_TO edge from each HTTP API's EntryPoint entity to the existing synthetic internet NetworkEndpoint (`0.0.0.0/0`), because AWS API Gateway HTTP APIs provide no VPC-only (private) endpoint option and are always reachable from outside the VPC boundary.
6. THE Entry_Point_Normalizer SHALL create a CONTAINS edge from the owning CloudAccount entity to each EntryPoint entity, consistent with the existing S3 CONTAINS pattern.
7. THE Steampipe_Catalog SHALL add table entries for the AWS API Gateway REST API table (`aws_api_gateway_rest_api`) and the AWS API Gateway HTTP API table (`aws_api_gatewayv2_api`), each under an existing or newly declared Capability.

### Requirement 9: Provider-agnostic extensibility

**User Story:** As a Bellu platform engineer, I want the new entity kinds, relation types, and evaluators introduced for AWS to be reusable for GCP and Azure, so that later phases extend the same model instead of forking it.

#### Acceptance Criteria

1. THE Network, EntryPoint, CAN_ASSUME, ROUTES_TO, PEERS_WITH, MEMBER_OF, and ATTACHED_TO additions SHALL be added as additional values in the shared, provider-independent `Kind`/`Relation` allow-lists in `internal/model/model.go` (`AllKinds`, `AllRelations`), with constant names and string values that carry no provider-specific prefix or suffix (for example, not `Network_AWS` or `AWS_ROUTES_TO`), rather than being added to a separate, provider-specific type system.
2. THE IAM_Access_Evaluator's resource-kind-specific action-catalog structure (Requirement 5.2) SHALL associate each AWS resource kind with its own action-to-classification (read/write/admin) mapping as data, kept separate from the evaluator's matching and classification code, such that adding a GCP or Azure resource kind's action catalog requires only adding one new mapping entry and SHALL NOT require modifying the evaluator's existing matching or classification logic.
3. THE Trust_Policy_Analyzer's CAN_ASSUME edge SHALL use the same generic edge shape (`Relation`, `SrcKey`, `DstKey`, `Attributes`, `Confidence`, `Evidence`) defined for every other relation in `internal/model/model.go`, and any additional attribute recorded on a CAN_ASSUME edge (such as the account-level-grant attribute described in Requirement 4.2) SHALL represent a concept applicable to any provider's identity-trust model rather than an AWS-only concept, so that a GCP service-account impersonation analyzer or Azure managed-identity trust analyzer can emit CAN_ASSUME edges of the same shape in a later phase without a schema change.
4. THE Network entity's `network_subtype` attribute vocabulary SHALL be documented, at the point in the codebase where `network_subtype` values are declared or enumerated, as open-ended, stating explicitly that adding a new `network_subtype` value for a GCP or Azure networking construct is an additive change and SHALL NOT require modifying the Network `Kind` definition or the `AllKinds` allow-list.
5. THIS feature SHALL implement Requirements 4 through 8 for AWS resources only; a GCP or Azure Network_Normalizer, Entry_Point_Normalizer, Data_Store_Normalizer, Trust_Policy_Analyzer, or IAM_Access_Evaluator action catalog SHALL NOT be implemented as part of this feature and is deferred to a future phase.
