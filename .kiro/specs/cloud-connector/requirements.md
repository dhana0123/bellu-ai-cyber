# Requirements Document

## Introduction

The Bellu Cloud Connector is a **runtime subsystem** that onboards and accesses customer cloud accounts (AWS first; GCP and Azure as explicit future phases) and hands the resulting access off to Bellu's **existing** collection pipeline. It owns everything up to — and only up to — the point where discovery can be driven: connection lifecycle, provider abstraction, cross-account authentication, temporary-credential sessions, modular capabilities, permission verification, and discovery orchestration. Its job ends by configuring and invoking the existing Steampipe-based cloud source and letting the established flow take over: Steampipe → RawEvent → Normalizer → EntityResolver → GraphWriter → risk.

The Connector is a **distinct subsystem** from the `bellu-cloud-lab` spec. The cloud-lab is a disposable Terraform integration-test fixture that provisions real infrastructure to validate collectors; the Connector is a production runtime that connects real customer accounts for ongoing collection. The two share Bellu's canonical model and pipeline but are otherwise independent, and this spec neither modifies nor depends on `bellu-cloud-lab`.

The Connector reuses Bellu's existing interfaces and MUST NOT introduce new model Kinds, Relations, or normalizers (`internal/model/model.go`, `internal/normalize`), and MUST NOT reimplement cloud collection. It drives the existing `internal/source/steampipe` source through its `Factory`/`Source.Collect` contract. The Connector operates **exclusively in the customer plane** (customer Postgres `bellu`/`postgres-customer`) and MUST NOT touch the Global Intelligence plane (`postgres-intel`/OpenSearch). New HTTP routes are added behind the existing authenticated, RBAC-gated API server (`backend/server`, `internal/auth`). No customer long-lived AWS access keys are ever stored anywhere; cross-account access uses STS AssumeRole with a per-customer ExternalId, consistent with [AWS guidance on granting third parties access using IAM roles and a unique external ID](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_create_for-user_externalid.html) and [least-privilege best practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html). *Content was rephrased for compliance with licensing restrictions.*

### Reconciliation Decisions and Assumptions

These decisions resolve divergences between the requested architecture and what the codebase emits or exposes today. They are assumptions baked into the requirements; they do not change existing Bellu behavior.

- **No new model Kinds/Relations/normalizers (Decision D1):** The Connector produces no graph entities itself. Everything the customer account yields reaches the graph only through the existing Steampipe source and existing normalizers. The canonical set in `AllKinds`/`AllRelations` (`internal/model/model.go`) is unchanged. The Connector's own entities (Connection, capability state, verification results, discovery sessions) are **operational state in the customer Postgres**, not graph nodes.
- **Discovery drives the existing Steampipe source (Decision D2):** The Discovery Orchestrator does not contain AWS queries. It builds a config map and invokes `steampipe.Factory(cfg)` to obtain a `source.Source`, then runs it through the existing `bellu run`/ingest pipeline (`internal/pipeline`, `internal/source`, `internal/rawevent`). Steampipe is configured to use the assumed-role temporary credentials for the customer account; the provider catalog YAML (`internal/source/steampipe/catalog`) selects which tables are collected. The Connector adds no new source implementation and reimplements no collection.
- **AssumeRole uses the existing AWS SDK v2 family (Decision D3):** The AWS adapter authenticates with the AWS SDK for Go v2 already in the module (`github.com/aws/aws-sdk-go-v2` v1.47.1, `.../config`, `.../service/s3`, `.../credentials` as an indirect dep). STS AssumeRole uses `github.com/aws/aws-sdk-go-v2/service/sts` (and `credentials/stscreds` for provider-driven refresh) added from the same SDK family. The adapter does not introduce a second AWS SDK.
- **Credential handoff to Steampipe (Decision D4):** Steampipe consumes AWS credentials via its own configuration (profile/env/role). The Connector supplies assumed-role temporary credentials to the Steampipe process/connection through its configured credential mechanism and passes connection parameters (host/port/user/password/database and the provider catalog/connection) into `steampipe.Factory`. Where the running Steampipe instance requires credentials via environment or a config file, the Connector materializes short-lived credentials for the duration of a discovery session only and discards them when the session ends. No credential is written to the customer Postgres.
- **RBAC permission reuse (Decision D5):** Connector routes are gated using the existing permission catalog in `internal/auth` (`AllPermissions`). Connection management (create/generate-setup/verify/discover/delete) is treated as an integration-management action and is gated by `integrations:manage` (`PermIntegrations`); read-only connection status is gated by an existing read permission (`projects:read`/`PermProjectsRead`). This reuses the current catalog without adding new permission keys in the first phase; a dedicated `connectors:manage` permission is deferred and called out as a future refinement rather than introduced now.
- **Tenant/project model reuse (Decision D6):** "Tenant" maps to the existing organization (`OrgID` on `auth.User`, `org_id`) and "project" maps to the existing project scope used by RBAC (`ProjectID`/`projects` and `Principal.CanAccessProject`). The Connection entity references these existing identifiers rather than inventing a parallel tenancy model.
- **Connector package placement (Decision D7):** The Connector lives under `internal/connector` in the Go module `github.com/bellu/bellu-ai-cyber`, with sub-packages for the manager, provider adapters (`internal/connector/provider`, `internal/connector/provider/aws`), credential sessions, capabilities, verification, and discovery orchestration. HTTP handlers live in `backend/server`; the connector migration lives under `internal/store/migrations` as the next sequential goose migration (`00007_connector.sql`).

## Glossary

- **Connector**: The Bellu Cloud Connector subsystem delivered under `internal/connector` plus its `backend/server` routes and `internal/store/migrations` migration.
- **Connection**: The entity owned by the Connector Manager representing one customer cloud account linkage, with fields: id, tenant, project, provider, account, role, region, capabilities, status, created_at, last_verified_at.
- **Connector_Manager**: The Connector layer that owns the Connection entity and its lifecycle, tenant/project ownership, provider configuration, capability set, and connection state. It is provider-agnostic.
- **Cloud_Provider**: The common provider-adapter interface with operations CreateSetup, VerifyConnection, Authenticate, GetCredentials, CheckPermissions, and Discover, implemented per provider.
- **AWS_Adapter**: The AWS implementation of Cloud_Provider.
- **AWS_Auth_Layer**: The Connector component that validates role ARN and account id, generates/looks up the per-customer External_Id, and calls STS AssumeRole to obtain temporary credentials.
- **External_Id**: A unique per-customer/per-account value included in the customer role trust policy and in the STS AssumeRole request, following AWS third-party-access guidance.
- **Temporary_Credentials**: Short-lived credentials returned by STS AssumeRole (access key id, secret access key, session token, expiry).
- **Credential_Session_Manager**: The Connector component that obtains, temporarily caches, refreshes, expires, and revokes Temporary_Credentials.
- **Capability**: A named, modular permission bundle; one of `core`, `eks`, `workload`, or `dspm`.
- **Capability_Manager**: The Connector component that manages which Capabilities are enabled on a Connection and the required permissions each Capability implies.
- **Permission_Verifier**: The Connector component that checks each enabled Capability's required permissions and returns a per-capability, per-check result.
- **Discovery_Orchestrator**: The Connector component exposing verify, discover, refresh, retry, and cancel; it creates a Discovery_Session and drives the existing Steampipe source and Bellu pipeline.
- **Discovery_Session**: A tracked unit of a single discovery execution with its own identifier and status.
- **Steampipe_Source**: Bellu's existing cloud collector, `internal/source/steampipe`, built via `steampipe.Factory(cfg)` and run via `Source.Collect`.
- **Bellu_Pipeline**: Bellu's existing flow: ingest source → RawEvent (`internal/rawevent`) → Normalizer (`internal/normalize`) → EntityResolver (`internal/resolve`) → persist to Postgres (`internal/store`) → sync to Memgraph (`internal/graph`) → risk (`internal/risk`).
- **Customer_Plane**: The customer-facing data plane (customer Postgres `bellu`/`postgres-customer`) where the Connector operates.
- **Intel_Plane**: The Global Intelligence plane (`postgres-intel`/OpenSearch) which the Connector MUST NOT access.
- **Tenant**: An organization in the existing auth model (`OrgID`/`org_id`).
- **Project**: A project in the existing RBAC scope model (`ProjectID`/`projects`).
- **BelluConnectorRole**: The customer-side IAM role (default name) with a trust policy (Bellu principal + External_Id) and a least-privilege permission policy that Bellu assumes.
- **Setup_Instructions**: The provider-specific onboarding artifact the Connector returns (role ARN template, External_Id, trust and permission policy, and human-readable steps).
- **Connector_API**: The set of RBAC-gated HTTP routes added to `backend/server` for connection management.
- **Frontend**: The React UI "Cloud Connections" area that handles only non-secret identifiers and statuses.

## Requirements

### Requirement 1: Connector repository and package placement

**User Story:** As a Bellu platform engineer, I want the Connector organized within the existing Go module, so that it integrates cleanly without disturbing existing packages.

#### Acceptance Criteria

1. THE Connector SHALL place its code under `internal/connector` within the Go module `github.com/bellu/bellu-ai-cyber`. [Decision D7]
2. THE Connector SHALL provide separate sub-packages for the Connector_Manager, provider adapters (`internal/connector/provider` with an `aws` sub-package), the Credential_Session_Manager, the Capability_Manager, the Permission_Verifier, and the Discovery_Orchestrator. [Decision D7]
3. THE Connector SHALL implement its HTTP handlers within the existing `backend/server` package. [Decision D7]
4. THE Connector SHALL add its database schema as the next sequential goose migration file under `internal/store/migrations`, following the existing numbering after `00006_findings.sql`. [Decision D7]
5. THE Connector SHALL add zero entries to `AllKinds` and `AllRelations` in `internal/model/model.go`. [Decision D1]
6. THE Connector SHALL add zero normalizers under `internal/normalize`. [Decision D1]

### Requirement 2: Connection entity and ownership

**User Story:** As a Bellu operator, I want each customer cloud linkage represented as a single owned entity, so that connections are tracked per tenant and project.

#### Acceptance Criteria

1. THE Connector_Manager SHALL represent each customer cloud account linkage as one Connection entity carrying id, tenant, project, provider, account, role, region, capabilities, status, created_at, and last_verified_at.
2. THE Connector_Manager SHALL associate every Connection with exactly one Tenant and exactly one Project. [Decision D6]
3. THE Connector_Manager SHALL set created_at to the time the Connection is created.
4. WHEN a Connection is successfully verified, THE Connector_Manager SHALL set last_verified_at to the time verification succeeded.
5. THE Connector_Manager SHALL expose Connection lifecycle, provider configuration, Capability set, and connection state without referencing AWS-specific implementation details.
6. THE Connector_Manager SHALL depend on the Cloud_Provider interface and SHALL contain zero AWS SDK calls.

### Requirement 3: Connection state machine

**User Story:** As a Bellu operator, I want a well-defined connection lifecycle, so that connection status is predictable and auditable.

#### Acceptance Criteria

1. WHEN a Connection is created, THE Connector_Manager SHALL set its status to PENDING.
2. WHEN setup is generated and verification begins for a Connection in status PENDING, THE Connector_Manager SHALL set its status to VERIFYING.
3. WHEN verification succeeds for a Connection in status VERIFYING, THE Connector_Manager SHALL set its status to CONNECTED.
4. IF verification fails for a Connection in status VERIFYING, THEN THE Connector_Manager SHALL set its status to ERROR.
5. WHEN discovery begins for a Connection in status CONNECTED, THE Connector_Manager SHALL set its status to DISCOVERY_RUNNING.
6. WHEN discovery completes successfully for a Connection in status DISCOVERY_RUNNING, THE Connector_Manager SHALL set its status to COMPLETE.
7. IF discovery fails for a Connection in status DISCOVERY_RUNNING, THEN THE Connector_Manager SHALL set its status to ERROR.
8. WHEN retry is requested for a Connection in status ERROR, THE Connector_Manager SHALL set its status to DISCOVERY_RUNNING.
9. IF a status transition is requested that is not defined by criteria 1 through 8, THEN THE Connector_Manager SHALL reject the transition and SHALL leave the Connection status unchanged.

### Requirement 4: Provider adapter interface

**User Story:** As a Bellu architect, I want a common provider interface, so that new cloud providers are added without changing the Connector Manager.

#### Acceptance Criteria

1. THE Connector SHALL define a Cloud_Provider interface exposing CreateSetup, VerifyConnection, Authenticate, GetCredentials, CheckPermissions, and Discover operations.
2. THE Connector SHALL provide an AWS_Adapter implementation of the Cloud_Provider interface.
3. WHERE a provider other than AWS is configured, THE Connector_Manager SHALL resolve the Cloud_Provider implementation registered for that provider without AWS-specific branching in the Connector_Manager.
4. WHEN a GCP or Azure Cloud_Provider implementation is added, THE Connector SHALL integrate it by registering the new implementation and SHALL require zero changes to the Connector_Manager. [see Requirement 15]
5. IF a Connection names a provider for which no Cloud_Provider implementation is registered, THEN THE Connector_Manager SHALL reject the operation and SHALL report that the provider is unsupported.

### Requirement 5: AWS authentication via STS AssumeRole

**User Story:** As a security engineer, I want cross-account AWS access via assumed roles with a unique external id, so that Bellu never holds customer long-lived keys.

#### Acceptance Criteria

1. WHEN a Connection is created for the AWS provider, THE AWS_Auth_Layer SHALL generate a unique External_Id for that customer account.
2. WHEN authentication is requested for an AWS Connection, THE AWS_Auth_Layer SHALL look up the stored External_Id for that Connection and SHALL include it in the STS AssumeRole request.
3. WHEN authentication is requested for an AWS Connection, THE AWS_Auth_Layer SHALL call STS AssumeRole against the Connection's role ARN to obtain Temporary_Credentials. [Decision D3]
4. THE AWS_Auth_Layer SHALL validate that the configured role ARN matches the AWS IAM role ARN format before calling STS AssumeRole.
5. THE AWS_Auth_Layer SHALL validate that the configured account id is a 12-digit AWS account identifier before calling STS AssumeRole.
6. IF the role ARN fails format validation or the account id fails format validation, THEN THE AWS_Auth_Layer SHALL reject the authentication request, SHALL report which value was invalid, and SHALL make zero STS calls.
7. THE AWS_Auth_Layer SHALL obtain AWS access exclusively through STS AssumeRole and SHALL use zero customer long-lived access keys.
8. IF STS AssumeRole returns an error, THEN THE AWS_Auth_Layer SHALL report the authentication failure and SHALL return zero Temporary_Credentials.

### Requirement 6: Credential session management

**User Story:** As a security engineer, I want temporary credentials handled in a bounded session, so that no durable secrets are persisted and credentials are discarded promptly.

#### Acceptance Criteria

1. WHEN Temporary_Credentials are obtained, THE Credential_Session_Manager SHALL cache them in process memory for the lifetime of the session.
2. WHILE cached Temporary_Credentials remain unexpired, THE Credential_Session_Manager SHALL return the cached Temporary_Credentials for subsequent requests on the same Connection rather than calling STS AssumeRole again.
3. WHEN cached Temporary_Credentials reach their expiry, THE Credential_Session_Manager SHALL discard them and SHALL obtain fresh Temporary_Credentials on the next request.
4. WHEN a refresh is requested for a Connection, THE Credential_Session_Manager SHALL obtain fresh Temporary_Credentials and SHALL replace any cached Temporary_Credentials for that Connection.
5. WHEN revocation is requested for a Connection, THE Credential_Session_Manager SHALL discard all cached Temporary_Credentials for that Connection.
6. WHEN a Discovery_Session ends, THE Credential_Session_Manager SHALL discard the Temporary_Credentials materialized for that session. [Decision D4]
7. THE Credential_Session_Manager SHALL write Temporary_Credentials to zero database rows, zero log entries, and zero API responses.

### Requirement 7: Persistence of non-secret connection data only

**User Story:** As a security engineer, I want the database to hold only non-secret connection metadata, so that a database compromise never exposes cloud credentials.

#### Acceptance Criteria

1. THE Connector SHALL persist, for an AWS Connection, the role ARN, the account id, the External_Id, and Connection metadata (id, tenant, project, provider, region, capabilities, status, created_at, last_verified_at).
2. THE Connector SHALL persist zero AWS secret access keys, zero AWS access key ids, and zero STS session tokens to the customer Postgres.
3. THE Connector SHALL store all persisted Connection data in the Customer_Plane and SHALL write zero rows to the Intel_Plane. [Decision D6, Requirement 14]
4. THE Connector SHALL add its tables through the goose migration under `internal/store/migrations` and SHALL access them through the existing `internal/store` pgx pool. [Decision D7]

### Requirement 8: Modular capabilities with least privilege

**User Story:** As a customer administrator, I want to grant access progressively by capability, so that I never grant more permission than a feature needs.

#### Acceptance Criteria

1. THE Capability_Manager SHALL support the Capabilities `core`, `eks`, `workload`, and `dspm`.
2. THE Capability_Manager SHALL define the `core` Capability as covering account, regions, EC2, VPC, IAM, and S3 read permissions.
3. THE Capability_Manager SHALL allow Capabilities to be enabled on a Connection independently of one another so that permissions can be added progressively.
4. THE Capability_Manager SHALL define each Capability's required permissions as a least-privilege read-oriented set.
5. THE Capability_Manager SHALL define zero Capability that requires the AWS `AdministratorAccess` managed policy or an equivalent full-access grant.
6. WHEN a Connection is created without an explicit Capability selection, THE Capability_Manager SHALL enable the `core` Capability as the default.

### Requirement 9: Permission verification with per-capability results

**User Story:** As a customer administrator, I want to see exactly which permissions passed per capability, so that I can fix a misconfigured role before relying on discovery.

#### Acceptance Criteria

1. WHEN verification is requested for a Connection, THE Permission_Verifier SHALL check the required permissions of each enabled Capability using the Connection's Temporary_Credentials.
2. THE Permission_Verifier SHALL return, for each enabled Capability, a per-permission result among PASS and FAIL.
3. WHERE a Capability is not enabled on the Connection, THE Permission_Verifier SHALL report that Capability as NOT_ENABLED.
4. WHEN verification completes with every checked permission reported PASS, THE Permission_Verifier SHALL report overall verification as successful.
5. IF any checked permission for an enabled Capability is reported FAIL, THEN THE Permission_Verifier SHALL report overall verification as failed and SHALL include each failing permission in the result.
6. THE Permission_Verifier SHALL perform read-only permission checks and SHALL perform zero write, modify, or delete operations against the customer account.

### Requirement 10: Discovery orchestration over the existing pipeline

**User Story:** As a Bellu operator, I want discovery to drive the existing collector and pipeline, so that onboarding reuses proven collection without duplicating it.

#### Acceptance Criteria

1. THE Discovery_Orchestrator SHALL expose verify, discover, refresh, retry, and cancel operations.
2. WHEN discover is requested for a Connection in status CONNECTED, THE Discovery_Orchestrator SHALL create a Discovery_Session with a unique identifier and SHALL track its status.
3. WHEN a Discovery_Session runs, THE Discovery_Orchestrator SHALL build a configuration map and invoke `steampipe.Factory(cfg)` to obtain the existing Steampipe_Source and SHALL run it through the existing Bellu_Pipeline. [Decision D2]
4. WHEN a Discovery_Session runs, THE Discovery_Orchestrator SHALL supply the Connection's Temporary_Credentials to Steampipe through its configured credential mechanism for the duration of the session only. [Decision D4]
5. THE Discovery_Orchestrator SHALL contain zero provider-specific resource queries and SHALL delegate all cloud collection to the Steampipe_Source. [Decision D2]
6. THE Discovery_Orchestrator SHALL introduce zero new model Kinds, zero new Relations, and zero new normalizers. [Decision D1]
7. WHEN cancel is requested for a running Discovery_Session, THE Discovery_Orchestrator SHALL stop the session promptly by cancelling the context passed to `Source.Collect` and SHALL record the session as cancelled.
8. WHEN retry is requested for a Connection in status ERROR, THE Discovery_Orchestrator SHALL start a new Discovery_Session for that Connection.
9. WHEN status is requested for a Connection, THE Discovery_Orchestrator SHALL return the current Connection status and the status of its most recent Discovery_Session.

### Requirement 11: Setup generation for AWS cross-account access

**User Story:** As a customer administrator, I want generated setup artifacts for the Bellu role, so that I can create a least-privilege cross-account role without sharing keys.

#### Acceptance Criteria

1. WHEN setup is generated for an AWS Connection, THE AWS_Adapter SHALL return a role ARN template referencing the default role name `BelluConnectorRole`, the generated External_Id, and human-readable Setup_Instructions.
2. WHEN setup is generated for an AWS Connection, THE AWS_Adapter SHALL return a trust policy granting the Bellu principal permission to assume the role conditioned on the generated External_Id.
3. WHEN setup is generated for an AWS Connection, THE AWS_Adapter SHALL return a least-privilege permission policy covering the enabled Capabilities, starting with Core read-only for the `core` Capability.
4. THE AWS_Adapter SHALL generate a permission policy that excludes the AWS `AdministratorAccess` managed policy and excludes wildcard full-access (`"Action": "*"` on `"Resource": "*"`). [Requirement 8]
5. THE AWS_Adapter SHALL include the generated External_Id in both the trust policy condition and the stored Connection record, and the two SHALL match.

### Requirement 12: RBAC-gated connector API boundary

**User Story:** As a Bellu operator, I want connector routes behind the existing authenticated API, so that connection management respects tenant isolation and RBAC.

#### Acceptance Criteria

1. THE Connector_API SHALL provide routes to create a Connection, generate setup, get a Connection, verify a Connection, start discovery, get status, and delete or disable a Connection.
2. THE Connector_API SHALL register all its routes within the authenticated group of the existing `backend/server` engine so that `auth.Authenticate` applies. [Decision D5]
3. THE Connector_API SHALL gate connection-mutating routes (create, generate setup, verify, discover, delete or disable) behind the `integrations:manage` permission (`auth.PermIntegrations`). [Decision D5]
4. THE Connector_API SHALL gate connection read routes (get Connection, get status) behind the `projects:read` permission (`auth.PermProjectsRead`). [Decision D5]
5. WHEN a request targets a Connection, THE Connector_API SHALL authorize the request against the Connection's Project using the existing `Principal.CanAccessProject` check. [Decision D6]
6. IF a principal lacks access to the Connection's Project, THEN THE Connector_API SHALL deny the request and SHALL return zero Connection data for that Connection.
7. THE Connector_API SHALL return role ARN, account id, External_Id, Connection id, status, Setup_Instructions, verification results, and discovery status and SHALL return zero AWS secret access keys, access key ids, or session tokens.

### Requirement 13: Frontend boundary

**User Story:** As a customer administrator, I want a Cloud Connections area that never touches cloud credentials, so that onboarding is safe and simple.

#### Acceptance Criteria

1. THE Frontend SHALL provide a Cloud Connections area that guides AWS onboarding through the sequence account, installation method, setup, verify, connected.
2. THE Frontend SHALL handle only Connection id, role ARN, Setup_Instructions, verification status, and discovery status.
3. THE Frontend SHALL handle zero AWS secret access keys, zero access key ids, and zero STS session tokens.
4. THE Frontend SHALL obtain all Connection data exclusively through the RBAC-gated Connector_API.
5. WHERE GCP or Azure onboarding is presented, THE Frontend SHALL mark it as a future provider. [Requirement 15]

### Requirement 14: Multi-tenant isolation and plane isolation

**User Story:** As a security engineer, I want strict tenant and plane isolation, so that one customer's connection data never leaks and the intelligence plane is never touched.

#### Acceptance Criteria

1. WHEN the Connector lists or reads Connections for a principal, THE Connector SHALL return only Connections whose Tenant and Project the principal is authorized to access. [Decision D6]
2. THE Connector SHALL scope every Connection query by Tenant so that Connections of one Tenant are never returned to another Tenant.
3. THE Connector SHALL read from and write to the Customer_Plane only and SHALL open zero connections to the Intel_Plane (`postgres-intel`/OpenSearch). [Requirement 7]
4. THE Connector SHALL leave the existing Bellu model, normalizers, collectors, and pipeline unchanged. [Decision D1, D2]

### Requirement 15: GCP and Azure extensibility as future phases

**User Story:** As a Bellu program lead, I want AWS delivered first with GCP and Azure explicitly deferred, so that scope is clear and the design stays extensible.

#### Acceptance Criteria

1. THE Connector SHALL deliver the AWS_Adapter as the only implemented Cloud_Provider in the first phase.
2. THE Connector SHALL define the Cloud_Provider interface so that GCP and Azure adapters can be added later without changes to the Connector_Manager. [Requirement 4]
3. THE Connector SHALL treat GCP and Azure provider support as out of scope for the first phase and SHALL document them as future work.
4. WHERE GCP or Azure is requested before its adapter is implemented, THE Connector SHALL report the provider as not yet supported. [Requirement 4]
