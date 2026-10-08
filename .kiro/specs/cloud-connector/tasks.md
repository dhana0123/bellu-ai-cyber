# Implementation Plan: Cloud Connector

## Overview

This plan builds the Bellu Cloud Connector bottom-up inside the existing Go module `github.com/bellu/bellu-ai-cyber`, so each layer compiles and tests green against the layers beneath it before the next is added. It starts with the goose migration, then the provider-agnostic core (state machine, capabilities, credential sessions, provider registry), then the AWS adapter and its sub-layers, then persistence, discovery orchestration, the manager wiring, and finally the RBAC-gated HTTP boundary and server wiring. Every step reuses the existing Steampipe source and `bellu run` pipeline, adds zero model Kinds/Relations/normalizers, and persists zero cloud secrets. Testing is example/table-driven for deterministic logic plus `//go:build integration` testcontainers tests for the migration and repository; there is no property-based testing (the design does not define correctness properties). Test sub-tasks are marked optional with `*`.

## Tasks

- [x] 1. Add the connector database migration
  - [x] 1.1 Create `internal/store/migrations/00007_connector.sql`
    - Add the next sequential goose migration after `00006_findings.sql`, picked up automatically via the existing `//go:embed migrations/*.sql`
    - Create `connector_connections` (id, org_id FK `organizations(id)`, project_id FK `projects(id)`, provider, account_id, role_arn, external_id, region, capabilities TEXT[] default `ARRAY['core']`, status default `PENDING`, created_at default now(), last_verified_at), plus `connector_capabilities`, `connector_verification_results`, and `connector_discovery_sessions` with `ON DELETE CASCADE` FKs, indexes, and the unique `(org_id, provider, account_id)` constraint
    - Include the NO-SECRETS header comment and define zero credential columns; add the `-- +goose Down` drops in reverse dependency order
    - _Requirements: R1.4, R7.1, R7.2, R7.3, R7.4, R2.2, R14.2, R14.3_
  - [ ]* 1.2 Write migration integration test (`//go:build integration`, testcontainers postgres:16-alpine)
    - Mirror `internal/store/store_integration_test.go`: `store.Open` + `Store.Migrate` applies `00007` cleanly on top of `00001`–`00006`; assert the four `connector_*` tables and expected columns/FKs exist
    - Add a schema introspection check asserting no column in any `connector_*` table matches credential patterns (`secret`, `access_key`, `session_token`)
    - _Requirements: R1.4, R7.2, R7.4_

- [ ] 2. Implement connector core types and errors
  - [x] 2.1 Create `internal/connector/errors.go`
    - Define sentinel errors `ErrUnsupportedProvider`, `ErrInvalidTransition`, `ErrNotFound`, `ErrInvalidValue` for handler-to-HTTP mapping
    - _Requirements: R3.9, R4.5, R5.6, R15.4_
  - [ ] 2.2 Create `internal/connector/connection.go`
    - Define the `Connection` struct (non-secret fields only, `OrgID` with `json:"-"`, no credential field), the `Status` enum, the `Event` enum, the `allowedTransitions` map, and `Status.Next(Event)` returning `ErrInvalidTransition` with status unchanged on invalid transitions
    - _Requirements: R2.1, R2.5, R3.1, R3.2, R3.3, R3.4, R3.5, R3.6, R3.7, R3.8, R3.9, R7.2, R12.7_
  - [ ]* 2.3 Write state-machine unit tests (table-driven)
    - Every `(from, event)` pair in `allowedTransitions` asserts the resulting status; every pair not in the table asserts `ErrInvalidTransition` and unchanged status
    - _Requirements: R3.1, R3.2, R3.3, R3.4, R3.5, R3.6, R3.7, R3.8, R3.9_

- [ ] 3. Implement the capability catalog
  - [ ] 3.1 Create `internal/connector/capability/capability.go`
    - Define the `Capability` enum and `Definition` type; populate the `Catalog` for `core`/`eks`/`workload`/`dspm` with least-privilege read-oriented permission sets (core covers account, regions, EC2, VPC, IAM, S3 reads) and per-capability verify probes; add a default-selection helper returning `["core"]`
    - _Requirements: R8.1, R8.2, R8.3, R8.4, R8.5, R8.6_
  - [ ]* 3.2 Write capability unit tests
    - Assert catalog contains the four capabilities and core's read set; assert no definition contains `AdministratorAccess` or `*` action on `*` resource; assert default selection yields `["core"]`
    - _Requirements: R8.1, R8.2, R8.5, R8.6_

- [ ] 4. Implement the credential session manager
  - [ ] 4.1 Create `internal/connector/session/session.go`
    - Implement the in-memory `Manager` (map keyed by ConnectionID, mutex, injectable `now func() time.Time`) with `Obtain`, `Get`, `Refresh`, and `Revoke`; cache unexpired credentials, discard+re-obtain on expiry, replace on refresh, delete on revoke; never write to DB/logs/responses
    - Define the provider-agnostic `Credentials` type here or in the provider package per the design layout
    - _Requirements: R6.1, R6.2, R6.3, R6.4, R6.5, R6.6, R6.7_
  - [ ]* 4.2 Write session-manager unit tests (injected clock)
    - Assert cached unexpired creds returned without re-authenticating; expired creds discarded and refreshed; `Refresh` replaces the entry; `Revoke` clears it; a spy confirms zero DB/log/response writes
    - _Requirements: R6.2, R6.3, R6.4, R6.5, R6.6, R6.7_

- [ ] 5. Implement the provider interface and registry
  - [ ] 5.1 Create `internal/connector/provider/provider.go` and `internal/connector/provider/registry.go`
    - Define the `CloudProvider` interface (CreateSetup, VerifyConnection, Authenticate, GetCredentials, CheckPermissions, Discover, Name) and the supporting non-secret types (`Target`, `Setup`, `SetupInput`, `VerifyResult`, `DiscoveryPlan`); implement `Registry` with `Register` and `Resolve`
    - _Requirements: R4.1, R4.3, R4.4, R15.2_
  - [ ]* 5.2 Write registry unit tests
    - Assert `Register` then `Resolve` returns the provider; `Resolve` of an unregistered name reports not-found so the manager can return `ErrUnsupportedProvider`
    - _Requirements: R4.3, R4.5, R15.4_

- [ ] 6. Implement the AWS adapter and its sub-layers
  - [ ] 6.1 Create `internal/connector/provider/aws/policy.go`
    - Define IAM policy document types and a renderer for trust and permission policies
    - _Requirements: R11.2, R11.3, R11.4_
  - [ ] 6.2 Create `internal/connector/provider/aws/setup.go`
    - Generate the `BelluConnectorRole` ARN template, the trust policy conditioned on the generated `ExternalId`, and the Core least-privilege read-only permission policy plus statements for other enabled capabilities; assert by construction that no statement is `"Action": "*"` on `"Resource": "*"` and `AdministratorAccess` is never referenced; emit human-readable instructions
    - _Requirements: R11.1, R11.2, R11.3, R11.4, R11.5, R8.4, R8.5_
  - [ ] 6.3 Create `internal/connector/provider/aws/auth.go`
    - Add dependencies `github.com/aws/aws-sdk-go-v2/service/sts` and `github.com/aws/aws-sdk-go-v2/credentials/stscreds` from the existing SDK family; define the narrow `STSAPI` interface (mirroring the `s3API` pattern in `cmd/global-intel/raw/s3store.go`); implement `AuthLayer.Authenticate` validating role ARN and 12-digit account id regexes before any STS call, making zero STS calls on validation failure (naming the invalid field), calling `STSAPI.AssumeRole` with the stored `ExternalId`, and returning zero credentials on STS error; generate the per-connection `ExternalId` with `crypto/rand`
    - _Requirements: R5.1, R5.2, R5.3, R5.4, R5.5, R5.6, R5.7, R5.8_
  - [ ] 6.4 Create `internal/connector/provider/aws/verify.go`
    - Implement read-only permission probes per enabled capability and shape results into `VerifyResult`/`CapabilityResult`/`PermissionResult` with `PASS`/`FAIL`, `NOT_ENABLED` for disabled capabilities, `OK` true iff all PASS, and a `Failures` list of every failing permission; issue zero write/modify/delete calls
    - _Requirements: R9.1, R9.2, R9.3, R9.4, R9.5, R9.6_
  - [ ] 6.5 Create `internal/connector/provider/aws/adapter.go`
    - Implement `CloudProvider` by composing auth, session, capability, verify, and setup; `GetCredentials` goes through the session cache; `Discover` builds the `DiscoveryPlan` (steampipe config map + session-only `CredentialEnv`) without provider-specific resource queries; `Name()` returns `"aws"`
    - _Requirements: R4.2, R10.4, R10.5, R15.1_
  - [ ]* 6.6 Write AWS adapter/auth/setup/verify unit tests with a fake STSAPI
    - Table-driven ARN/account validation (invalid inputs rejected, offending field named, zero STS calls via a counting fake); success returns in-memory creds, failure returns zero creds; assert the `ExternalId` passed to the fake equals the stored value; verifier result shaping; setup policy excludes `AdministratorAccess` and wildcard full access and the trust-policy ExternalId equals the stored value
    - _Requirements: R5.2, R5.3, R5.4, R5.5, R5.6, R5.8, R9.2, R9.3, R9.4, R9.5, R11.2, R11.4, R11.5, R8.5_

- [ ] 7. Checkpoint
  - Ensure all tests pass, ask the user if questions arise.

- [ ] 8. Implement the connector repository
  - [ ] 8.1 Create `internal/connector/repository.go`
    - Implement `NewRepository(*store.Store)` over `store.Store.Pool()`; add org-scoped `Insert`, `Get`, `ListByOrg`, `UpdateStatus`, `SetVerified`, `Delete`, plus `SaveVerification` and `RecordSession`; every read/write filters by `org_id`; persist non-secret fields only
    - _Requirements: R7.1, R7.3, R7.4, R2.1, R2.3, R2.4, R14.1, R14.2, R14.3_
  - [ ]* 8.2 Write repository integration tests (`//go:build integration`, testcontainers)
    - Insert → Get → ListByOrg → UpdateStatus → SetVerified → Delete round-trip asserting non-secret fields persist and `last_verified_at` set on verification; a connection under org A is never returned to a query scoped to org B
    - _Requirements: R2.1, R2.2, R2.3, R2.4, R7.1, R14.1, R14.2_

- [ ] 9. Implement the discovery orchestrator
  - [ ] 9.1 Create `internal/connector/discovery/session.go` and `internal/connector/discovery/orchestrator.go`
    - Define the `Session` type (unique id, status, started/ended, stored `cancel`) and the `Orchestrator` exposing verify/discover/refresh/retry/cancel plus `Latest`; obtain session-only credentials, build the plan via the provider, call the existing `steampipe.Factory(cfg)` to get a `source.Source`, and run it through an injectable `PipelineRunner` that wraps the existing `internal/pipeline`/`bellu run` wiring; create a cancellable context passed to `Source.Collect`; on `Cancel` invoke the stored cancel and record `cancelled`; map success/failure to `complete`/`failed`; revoke session credentials at any terminal state; contain zero provider-specific queries and add zero model Kinds/Relations/normalizers
    - _Requirements: R10.1, R10.2, R10.3, R10.4, R10.5, R10.6, R10.7, R10.8, R10.9, R6.6_
  - [ ]* 9.2 Write orchestrator unit tests with fake `source.Source` and fake `PipelineRunner`
    - Assert a `running` session with a unique id is created; credentials supplied for the session and revoked at end; `Cancel` cancels the context passed to `Collect` and marks `cancelled`; success/failure map to `complete`/`failed`; `Retry` from `ERROR` starts a new session; no provider-specific queries issued
    - _Requirements: R10.2, R10.4, R10.5, R10.7, R10.8, R6.6_

- [ ] 10. Implement the connector manager
  - [ ] 10.1 Create `internal/connector/manager.go`
    - Wire `Repository` + provider `Registry` + capability manager + discovery orchestrator; implement `Create` (default `["core"]`, `CreatedAt`, `Status=PENDING`), `GenerateSetup` (→ `VERIFYING`), `Verify` (→ `CONNECTED`/`ERROR`, `SetVerified` on success), `Discover`, `Retry`, `Cancel`, `Status`, `Get`, `Delete`; drive all status changes through `Status.Next`; `resolveProvider` returns `ErrUnsupportedProvider` for unknown providers; contain zero AWS SDK calls
    - _Requirements: R2.1, R2.3, R2.4, R2.5, R2.6, R3.1, R3.2, R3.3, R3.4, R3.5, R3.6, R3.7, R3.8, R4.3, R4.5, R8.6, R10.2, R10.8, R10.9, R15.1, R15.4_
  - [ ]* 10.2 Write manager unit tests with a fake CloudProvider
    - Assert lifecycle (create → setup → verify → discover) drives expected statuses; default capability `["core"]`; `SetVerified` on verify success; unsupported-provider rejection; provider-agnostic resolution with zero AWS SDK involvement
    - _Requirements: R2.3, R2.4, R2.6, R3.1, R3.2, R3.3, R3.4, R4.3, R4.5, R8.6, R15.4_

- [ ] 11. Checkpoint
  - Ensure all tests pass, ask the user if questions arise.

- [ ] 12. Wire the RBAC-gated connector API
  - [ ] 12.1 Extend `backend/server/server.go` with a nil-safe connector
    - Add `connector *connector.Manager` to `Server` and `Connector *connector.Manager` to `Options`; register `s.registerConnectorRoutes(authed)` inside `Handler()`'s authed group next to `registerAdminRoutes`
    - _Requirements: R12.2_
  - [ ] 12.2 Create `backend/server/connector_handlers.go`
    - Implement `registerConnectorRoutes` gating mutations (create, setup, verify, discover, cancel, delete) behind `auth.PermIntegrations` and reads (get, status) behind `auth.PermProjectsRead`; implement the `connectionForRequest` helper (nil connector → `503`; tenant-scoped `Get` via `Principal.User.OrgID`; `404` when missing; `403` with no body when `Principal.CanAccessProject` fails); implement each handler mapping sentinel errors to `400`/`404`/`500`; route `discover` on an `ERROR` connection to `Retry`; return role ARN/account id/external id/id/status/setup/verification/session only and never any secret
    - _Requirements: R12.1, R12.2, R12.3, R12.4, R12.5, R12.6, R12.7, R13.2, R13.3, R13.4, R10.9_
  - [ ] 12.3 Construct the manager in `backend/cmd/bellu-api/main.go`
    - After the store is open, construct the provider registry, register the AWS adapter (only phase-one provider), build the `connector.Manager`, and pass it into server `Options`
    - _Requirements: R12.2, R15.1_
  - [ ]* 12.4 Write connector handler tests (unit + `//go:build integration`)
    - Consistent with existing `backend/server` tests: assert nil connector → `503`; missing permission denied; cross-project access → `403` with no connection body; create/setup/verify/discover/status happy paths; assert no response body contains a secret access key, access key id, or session token
    - _Requirements: R12.2, R12.3, R12.4, R12.5, R12.6, R12.7_

- [ ] 13. Final verification and formatting
  - Run `go build ./...`, `go vet ./...`, and `go test ./...`; run the `integration`-tagged tests for the connector and store packages; `gofmt` the touched files; confirm `internal/model/model.go` and `internal/normalize` are unchanged (zero new Kinds/Relations/normalizers)
  - _Requirements: R1.5, R1.6, R10.6, R14.4_

## Notes

- Tasks marked with `*` are optional test sub-tasks and can be skipped for a faster MVP; core implementation tasks are never optional.
- Each task references the specific requirements it satisfies for traceability; the plan collectively covers R1–R15.
- The plan is bottom-up: migration → core types → capabilities → sessions → provider registry → AWS adapter → repository → discovery → manager → API/server wiring → final verification, so every layer compiles and tests against the layers beneath it.
- Property-based testing is intentionally omitted: the design defines no correctness properties and the Testing Strategy specifies table-driven unit tests plus `//go:build integration` testcontainers tests only.
- Frontend (R13) is satisfied by the API contract (no credentials are ever returned); no React implementation tasks are included because the repo focus is the Go backend. R13 is covered by the handler contract in task 12.
- Checkpoints (tasks 7 and 11) ensure incremental validation before the next layer.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "2.1"] },
    { "id": 1, "tasks": ["1.2", "2.2", "3.1", "4.1", "5.1"] },
    { "id": 2, "tasks": ["2.3", "3.2", "4.2", "5.2", "6.1"] },
    { "id": 3, "tasks": ["6.2", "6.3", "6.4"] },
    { "id": 4, "tasks": ["6.5", "8.1"] },
    { "id": 5, "tasks": ["6.6", "8.2", "9.1"] },
    { "id": 6, "tasks": ["9.2", "10.1"] },
    { "id": 7, "tasks": ["10.2", "12.1"] },
    { "id": 8, "tasks": ["12.2", "12.3"] },
    { "id": 9, "tasks": ["12.4"] }
  ]
}
```
