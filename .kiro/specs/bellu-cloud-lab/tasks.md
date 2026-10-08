# Implementation Plan: Bellu Cloud Lab

## Overview

This plan converts the Bellu Cloud Lab design into incremental coding and
infrastructure tasks, sequenced by the design's phased delivery (Requirement 19).
**Phase 1 (AWS core) must land end-to-end before any later phase.** The Lab is a
pure *consumer* of Bellu: it introduces no new model Kinds, Relations,
normalizers, or collector behavior (Requirements 10.9, 18.3) — new table
coverage, when needed, is a catalog-YAML addition only.

Languages: the Test_Runner and its internals are **Go** (reusing `internal/model`
read-only); infrastructure is **Terraform/HCL**; property-based tests use
`pgregory.net/rapid` at **≥100 iterations** each; real-cloud integration tests
are gated behind the repo's existing `-tags=integration` convention.

Each leaf task builds on prior tasks and wires into the growing Test_Runner so no
code is orphaned. Test sub-tasks are marked optional with `*` and must not be
implemented automatically; non-`*` sub-tasks must be implemented.

## Tasks

- [ ] 1. Scaffold the Lab repository structure and build wiring
  - [ ] 1.1 Create the `bellu-cloud-lab/` directory tree and root files
    - Create provider dirs `aws/`, `gcp/`, `azure/`, each with `scenarios/`, `modules/`, `environments/` subdirectories
    - Create root `scripts/`, `README.md` (stub), `compatibility-matrix.md` (stub), `cmd/lab/`, and `internal/{contract,runner,graphquery,report}/` package directories
    - Add a `go.mod`/module declaration (or module path) for the Lab command so it can import `internal/model` read-only; do not add CloudFormation anywhere
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 18.3_

  - [ ] 1.2 Create `Makefile.lab` and include it from the repo `Makefile`
    - Add target stubs `lab-up`, `lab-test`, `lab-down` (each taking `SCENARIO=<provider>/<name>`), `integration-test-cloud`, `destroy-all`
    - Follow the existing Makefile style of `$(GO)` and `docker compose` invocations; add `include bellu-cloud-lab/Makefile.lab` to the repo `Makefile`
    - Add `pgregory.net/rapid` as a test dependency (go get / go.mod) for later PBT tasks
    - _Requirements: 11.1, 11.5, 11.6, 11.7_

- [ ] 2. Implement the scenario contract schema and validating loader
  - [ ] 2.1 Define the `contract.Scenario` schema and YAML decoding
    - In `bellu-cloud-lab/internal/contract`, define `Scenario`, `NodeExpect`, `EdgeExpect`, `AttackPath`, `RiskExpect` with the design's YAML tags
    - Implement `Load(path string) (Scenario, error)` that decodes YAML then calls `Validate()`
    - _Requirements: 3.1, 3.2_

  - [ ] 2.2 Implement `Scenario.Validate` against Bellu's allow-lists
    - Import `internal/model` read-only; use `model.ValidKind`/`model.ValidRelation`
    - Enforce all three sections present (report the missing section); validate every `expected_nodes` Kind, every `expected_edges` Relation, and every attack-path node Kind/edge Relation; on any unknown token, report each unknown token with its referencing entry and mark the whole contract invalid (no partial acceptance)
    - _Requirements: 3.3, 3.4, 3.5, 3.7, 3.8, 3.9_

  - [ ]* 2.3 Write property test for contract loader rejecting unknown Kinds/Relations
    - **Property 1: Contract loader rejects unknown Kinds and Relations**
    - Generators draw valid Kinds/Relations from `model.AllKinds`/`AllRelations` plus deliberately-invalid tokens; assert `Validate` errors, names each unknown token with its entry, and accepts none of the contract; ≥100 iterations; tag with `// Feature: bellu-cloud-lab, Property 1`
    - **Validates: Requirements 3.3, 3.4, 3.5, 3.8**

  - [ ]* 2.4 Write property test for positive validation closure
    - **Property 6: Contract Kind/Relation closure under valid inputs**
    - Generate contracts composed solely of valid Kinds/Relations with all three sections present; assert `Validate` succeeds; ≥100 iterations
    - **Validates: Requirements 3.2, 3.7**

  - [ ]* 2.5 Write unit test for missing-section reporting
    - Hand-written contract missing `expected_edges` reports that exact section; `public-ec2` fixture loads and validates
    - _Requirements: 3.2, 3.9_

- [ ] 3. Implement the result model and graph comparator
  - [ ] 3.1 Define the `report.Result` model
    - In `bellu-cloud-lab/internal/report`, define `Result`, `Status` (PASS|FAIL), `MissingNodes`, `MissingEdges`, `MissingPaths` (`AttackPathDiag`), `RiskCheck` (`RiskDiag`), `Phase`, `FailureReason`, `Timings`, `ResourcesMayRemain`
    - _Requirements: 10.5_

  - [ ] 3.2 Implement the comparator over nodes, edges, and attack paths
    - Compare queried graph to the contract: nodes by Kind/Name, edges by relation + endpoints with `min_confidence` tolerance (Decision D3), attack-path chains by node/edge presence; PASS only when every declared element is present, else FAIL listing each missing node/edge and which chain element was absent
    - _Requirements: 10.5, 7.5_

  - [ ]* 3.3 Write property test for PASS implies full contract satisfaction
    - **Property 2: PASS implies full contract satisfaction**
    - Generate random (contract, graph) pairs; assert PASS ⇔ every node/edge/chain present (edges tolerant to confidence ≥ `min_confidence`), and any absent element yields FAIL listing it; ≥100 iterations
    - **Validates: Requirements 10.5, 7.5**

- [ ] 4. Implement graph query clients (API-first, Postgres fallback)
  - [ ] 4.1 Implement `graphquery.APIClient`
    - Query the control-panel API: `GET /api/entities?kind=`, `GET /api/edges/find?relation=&like=`, `GET /api/stats`, `GET /api/graph/attack-paths`; health-probe via `GET /api/health`
    - Expose a `graphquery.Client` interface the comparator consumes; never query Memgraph directly and never touch the intel plane
    - _Requirements: 10.3, 18.2_

  - [ ] 4.2 Implement `graphquery.PostgresReader` fallback
    - Read normalized `entities`/`edges` tables from the customer Postgres DSN when `GET /api/health` is unreachable; wire selection so the runner uses API-first with Postgres fallback and only fails if both are unreachable
    - _Requirements: 10.3, 18.1_

- [ ] 5. Implement the Test_Runner orchestration
  - [ ] 5.1 Implement `runner.Runner` provisioning, pipeline, compare, teardown
    - `Run(ctx, sc)` runs `terraform apply`, polls readiness up to 600s (using modules' `created_resource_ids`), invokes the built `bellu` binary's `run` as a subprocess with a 900s budget, queries the graph via `graphquery.Client`, compares via the comparator, and **always** runs `terraform destroy`
    - Set `Phase`/`FailureReason`/`Timings`; on destroy failure set `ResourcesMayRemain` and return non-zero intent; handle apply failure, readiness timeout, and pipeline non-zero/timeout by FAIL + destroy
    - _Requirements: 10.1, 10.2, 10.3, 10.4, 10.5, 10.6, 10.7, 10.8, 10.9_

  - [ ]* 5.2 Write property test for always-invoked, idempotent teardown
    - **Property 3: Teardown is always invoked**
    - Use a mock terraform executor; for every outcome (apply failure, readiness timeout, pipeline failure, PASS, FAIL) assert `terraform destroy` is invoked exactly once and a repeated destroy is a no-op; ≥100 iterations
    - **Validates: Requirements 10.2, 10.4, 10.6, 10.7, 13.6**

  - [ ]* 5.3 Write property test for scenario-name ↔ single-contract bijection
    - **Property 5: Scenario name ↔ single contract bijection**
    - Over a generated provider `scenarios/` layout, assert each scenario name maps to exactly one directory with exactly one `scenario.yaml` whose `name` equals `<provider>/<dir>`; ≥100 iterations
    - **Validates: Requirements 3.1, 3.6**

- [ ] 6. Wire the `cmd/lab` command and Makefile targets
  - [ ] 6.1 Build `cmd/lab/main.go`
    - Parse `--scenario <provider>/<name>` and a mode (up | test | down); construct `Runner` with terraform bin, built `bellu` bin path, and `graphquery.Client`; exit codes reflect PASS/FAIL and destroy failure
    - _Requirements: 10.1, 10.6, 10.8_

  - [ ] 6.2 Flesh out `Makefile.lab` targets to call `cmd/lab`
    - `lab-up` provisions only (no assertions, no destroy); `lab-test` runs the Test_Runner; `lab-down` destroys; `integration-test-cloud` runs the runner across a selected scenario set; `destroy-all` destroys all Lab-provisioned resources
    - _Requirements: 11.1, 11.2, 11.3, 11.4, 11.5, 11.6, 13.6_

- [ ] 7. Implement reusable AWS Terraform modules (Phase 1 set)
  - [ ] 7.1 Implement network modules: `vpc`, `subnets`, `route-tables`, `igw`
    - Least-cost defaults (single VPC, 1 AZ, no NAT gateway); every taggable resource carries a `bellu-lab` tag set; each module outputs `created_resource_ids`
    - _Requirements: 2.1, 2.4, 2.5, 13.1, 13.4, 13.5, 13.7_

  - [ ] 7.2 Implement `security-group`, `ec2`, `iam`, `s3` modules
    - `ec2` defaults `t3.micro`; `iam` outputs role/user/instance-profile ids; `s3` no versioning/replication; `security-group` supports ingress open to `0.0.0.0/0`; all emit `created_resource_ids` and `bellu-lab` tags; smallest practical sizes where exposed
    - Note (do not implement yet): `rds`, `eks`, `lambda`, `cloudwatch-logs` modules are deferred to the phases whose scenarios need them
    - _Requirements: 2.2, 2.3, 2.4, 2.5, 13.1, 13.5, 13.7_

- [ ] 8. Author Phase 1 scenarios and their contracts
  - [ ] 8.1 Author `aws/scenarios/public-ec2` root module + `scenario.yaml`
    - Compose vpc + subnets + igw + route-tables + security-group (ingress `0.0.0.0/0`) + ec2 (instance profile) + iam (role + S3-read inline policy) + s3 (synthetic non-sensitive data)
    - Contract declares nodes `Compute`, `Identity`, `DataStore`, `NetworkEndpoint`; edges `USES` (Compute→Identity, Decision D1), `CAN_READ` (Identity→DataStore, min_confidence 0.7, Decision D3), `EXPOSED_TO` (SG NetworkEndpoint→`0.0.0.0/0`); empty `expected_attack_paths`
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 1.7, 12.6_

  - [ ] 8.2 Author `aws/scenarios/iam-to-s3` root module + `scenario.yaml`
    - Compose iam (role or user + attached S3 policy) + s3; contract declares nodes `Identity`, `DataStore` and one `CAN_READ|CAN_WRITE|CAN_ADMIN` edge Identity→DataStore at min_confidence 0.7 (Decision D3)
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 1.7_

  - [ ] 8.3 Add per-environment backend/tfvars for independent state
    - Add `aws/environments/<env>.backend.hcl` and `<env>.tfvars`; default to local state keyed per scenario/environment (no Terraform workspaces); document remote-backend override; no secrets committed
    - _Requirements: 1.7, 12.1_

- [ ] 9. Phase 1 documentation and credential/cost helpers
  - [ ] 9.1 Write `compatibility-matrix.md` Phase 1 AWS rows
    - Columns: cloud resource, Terraform resource, Bellu node, Bellu edge, tested; reflect actual `aws.go` coverage; flag VPC/subnet/route-table/IGW and locked-down SG as not collectible
    - _Requirements: 15.1, 15.2, 15.3, 15.4_

  - [ ] 9.2 Write `README.md` and `scripts/` helpers
    - Document native credential config for AWS/GCP/Azure (no secrets in files), the least-privilege read-only collector IAM policy (`Describe*`/`List*`/`Get*`), cost-control measures and `make destroy-all`, CI-ready commands, and the Scenario_Contract format mapping to Bellu Kinds/Relations
    - Add `scripts/steampipe-up.sh` and a credential-check helper
    - _Requirements: 12.1, 12.2, 12.5, 20.1, 20.2, 20.3, 20.4, 13.6_

- [ ] 10. Phase 1 checkpoint
  - Ensure all unit and property tests pass, ask the user if questions arise.

- [ ] 11. Phase 1 real-cloud integration tests (gates all later phases)
  - [ ]* 11.1 Integration test `aws/public-ec2` end-to-end
    - Behind `-tags=integration`: apply → `bellu run` → API-first/Postgres-fallback compare → unconditional destroy against the AWS Test_Account; 1–3 representative executions, not 100 iterations
    - _Requirements: 14.1, 14.2, 19.1_

  - [ ]* 11.2 Integration test `aws/iam-to-s3` end-to-end
    - Behind `-tags=integration`: full apply → run → compare → destroy against the Test_Account
    - _Requirements: 14.1, 14.2, 19.1_

- [ ] 12. Phase 2: vulnerable-workload and attack-path scenarios
  - [ ] 12.1 Add `ecs`/`lambda` modules and author `aws/scenarios/vulnerable-workload`
    - Compose a single compute workload running one container image with a package pinned to a version affected by a published test CVE (no malware/exploit payload); modules keep least-cost defaults, `bellu-lab` tags, `created_resource_ids`
    - _Requirements: 2.3, 6.1, 6.7, 13.1, 13.5_

  - [ ] 12.2 Wire SBOM ingestion for vulnerable-workload (Decision D2)
    - Declare the CycloneDX SBOM file path (from `make sbom`, under `out/sboms/`) as a named scenario input; supply it to Bellu via `bellu ingest` (CycloneDX normalizer); fail with an SBOM-ingestion error if the SBOM is absent, invalid CycloneDX, or missing the pinned package, with no partial correlation
    - _Requirements: 6.2, 6.3, 6.4_

  - [ ] 12.3 Author the vulnerable-workload `scenario.yaml`
    - Nodes: exactly one `Compute` or `Workload`, one `ContainerImage`, ≥1 `Package`, ≥1 `Vulnerability`; edges: `RUNS_IMAGE` (workload→image), `DEPENDS_ON` (image→pinned Package), `HAS_VULN` (Package→Vulnerability matching the CVE-ID)
    - _Requirements: 6.5, 6.6_

  - [ ] 12.4 Author `aws/scenarios/attack-path` root module + `scenario.yaml`
    - Provision one externally exposed `Compute`, a vulnerable `Package`/`Vulnerability` chain on it, one IAM `Identity` via `USES`, one S3 `DataStore` with a permission edge; declare an `expected_attack_paths` chain NetworkEndpoint→Compute→Identity→DataStore (`EXPOSED_TO`, `USES`, `CAN_*` tolerant at 0.7) with a `risk_outcome` reachable_multiplier 2.5, clamp [0,100]
    - _Requirements: 7.1, 7.2, 7.6_

  - [ ] 12.5 Implement attack-path chain + reachability risk assertion in the comparator
    - Assert the full NetworkEndpoint→Compute→Identity→DataStore chain exists (report which node/edge is missing on failure); using the real `risk.Compute`, assert the reachable score ≈ 2.5× the unreachable score (±0.5 after clamp)
    - _Requirements: 7.3, 7.4, 7.5_

  - [ ]* 12.6 Write property test for reachability-driven risk multiplier
    - **Property 4: Reachability drives the risk-score multiplier**
    - Generate `RiskContext` values feeding the real `risk.Compute`; assert `Reachable=true` score = 2.5× `Reachable=false` before clamp and within ±0.5 of 2.5 after clamping to [0,100]; ≥100 iterations
    - **Validates: Requirements 7.4**

  - [ ] 12.7 Extend `compatibility-matrix.md` with Phase 2 rows
    - Add ECS task def (`Workload`, `USES`/`RUNS_IMAGE`), Lambda (`Compute`, `USES`), and SBOM-sourced Package/Vulnerability rows
    - _Requirements: 15.1, 15.2, 15.4_

  - [ ]* 12.8 Integration tests for Phase 2 scenarios
    - Behind `-tags=integration`: vulnerable-workload (with SBOM ingestion) and attack-path end-to-end apply → run → compare → destroy against the Test_Account
    - _Requirements: 14.1, 19.2_

- [ ] 13. Phase 2 checkpoint
  - Ensure all tests pass, ask the user if questions arise.

- [ ] 14. Phase 3: EKS identity chain scenario
  - [ ] 14.1 Add the `eks` module and author `aws/scenarios/eks-identity-chain`
    - EKS module defaults to smallest control-plane/node config, no GPU; provision a minimal cluster, a Pod, a ServiceAccount annotated `eks.amazonaws.com/role-arn` for IRSA, and an S3 bucket the role can access
    - _Requirements: 8.1, 8.2, 8.5, 13.2, 13.3_

  - [ ] 14.2 Author the eks-identity-chain `scenario.yaml`
    - Nodes include `Cluster`, `Workload`, `Identity`, `DataStore`; declare the identity-resolution chain workload→AWS IAM `Identity`→`DataStore` via a permission edge (Decision D3), resolved by the k8s workload-identity normalizer
    - _Requirements: 8.3, 8.4_

  - [ ]* 14.3 Integration test `aws/eks-identity-chain`
    - Behind `-tags=integration`: full apply → run → compare → destroy against the Test_Account
    - _Requirements: 14.1, 19.3_

- [ ] 15. Phase 4: AI-agent scenario
  - [ ] 15.1 Author `aws/scenarios/ai-agent` infrastructure
    - Where a cheap/safe AWS AI service (Bedrock custom model or SageMaker model) is collectible, provision it to produce an `AIService` node; add supporting IAM identity and S3/external-endpoint resources
    - _Requirements: 9.1, 9.2_

  - [ ] 15.2 Supply AI topology via the fixture mechanism and author the contract
    - Where cloud collectors cannot produce `AIAgent`/`AIModel`/`USES_MODEL`, supply them through Bellu's AI fixture/metadata mechanism as a declared scenario input (Decision D5); contract declares nodes/edges including `AIService` and, where fixture-supplied, `AIAgent`, `AIModel`, `USES_MODEL`
    - _Requirements: 9.3, 9.4_

  - [ ]* 15.3 Integration test `aws/ai-agent`
    - Behind `-tags=integration`: apply → run (with AI fixture input) → compare → destroy against the Test_Account
    - _Requirements: 14.1, 19.4_

- [ ] 16. Phase 5: GCP modules and scenarios
  - [ ] 16.1 Implement GCP reusable modules
    - VPC, subnet, firewall, Compute Engine, IAM service accounts, Cloud Storage; least-cost defaults, labels identifying Lab resources, `created_resource_ids` outputs; GKE/Cloud SQL/Vertex AI where practical
    - _Requirements: 16.1, 16.2, 13.1, 13.2, 13.5_

  - [ ] 16.2 Author GCP scenarios structurally comparable to AWS + contracts
    - Mirror the AWS public-ec2/iam-to-s3 shapes; express contracts using `model.AllKinds`/`AllRelations`; add GCP credential auth (ADC/service-account impersonation) docs/wiring
    - _Requirements: 16.3, 16.4, 12.3_

  - [ ] 16.3 Extend `compatibility-matrix.md` with GCP rows
    - _Requirements: 15.4, 16.1_

  - [ ]* 16.4 Integration tests for GCP scenarios
    - Behind `-tags=integration` against a GCP Test_Project
    - _Requirements: 14.1, 19.5_

- [ ] 17. Phase 6: Azure modules and scenarios
  - [ ] 17.1 Implement Azure reusable modules
    - VNet, subnet, NSG, Virtual Machine, managed identity, Blob Storage; least-cost defaults, resource tags, `created_resource_ids`; AKS/Azure SQL/Azure OpenAI where practical
    - _Requirements: 17.1, 17.2, 13.1, 13.2, 13.5_

  - [ ] 17.2 Author Azure scenarios structurally comparable to AWS + contracts
    - Mirror the AWS scenario shapes; express contracts using `model.AllKinds`/`AllRelations`; add Azure credential auth (CLI/service principal/managed identity) docs/wiring
    - _Requirements: 17.3, 17.4, 12.4_

  - [ ] 17.3 Extend `compatibility-matrix.md` with Azure rows
    - _Requirements: 15.4, 17.1_

  - [ ]* 17.4 Integration tests for Azure scenarios
    - Behind `-tags=integration` against an Azure Test_Subscription
    - _Requirements: 14.1, 19.6_

- [ ] 18. Final checkpoint
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional (unit/property/integration tests) and can be skipped for a faster MVP; core implementation tasks are never optional.
- Phase 1 (tasks 1–11) is the required core and gates every later phase (Requirement 19.1). Phases 2–6 build on the identical Test_Runner, contract schema, result model, and comparator.
- Property-based tests (P1–P6) use `pgregory.net/rapid` at ≥100 iterations and validate the Lab's pure logic only (contract loader/validator, comparator, risk relationship); real-cloud behavior is covered by `-tags=integration` tests with 1–3 executions each.
- The Lab never modifies Bellu: no new model Kinds, Relations, or normalizers; new collector coverage is a catalog-YAML addition only (Requirements 10.9, 18.3).
- Emulators (LocalStack/Moto/Azurite/fake-GCP) appear in zero primary scenarios (Requirement 14.2); only optional in-memory mocks for the Lab's own pure-logic unit tests (Requirement 14.3).

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2", "2.1", "3.1", "4.1", "7.1"] },
    { "id": 2, "tasks": ["2.2", "3.2", "4.2", "7.2"] },
    { "id": 3, "tasks": ["2.3", "2.4", "2.5", "3.3", "5.1", "8.3", "9.1", "9.2"] },
    { "id": 4, "tasks": ["5.2", "5.3", "6.1", "8.1", "8.2"] },
    { "id": 5, "tasks": ["6.2", "11.1", "11.2"] },
    { "id": 6, "tasks": ["12.1", "12.4"] },
    { "id": 7, "tasks": ["12.2", "12.5"] },
    { "id": 8, "tasks": ["12.3", "12.6", "12.7"] },
    { "id": 9, "tasks": ["12.8", "14.1"] },
    { "id": 10, "tasks": ["14.2", "15.1"] },
    { "id": 11, "tasks": ["14.3", "15.2"] },
    { "id": 12, "tasks": ["15.3", "16.1", "17.1"] },
    { "id": 13, "tasks": ["16.2", "17.2"] },
    { "id": 14, "tasks": ["16.3", "16.4", "17.3", "17.4"] }
  ]
}
```
