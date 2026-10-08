# Requirements Document

## Introduction

The Bellu Cloud Lab is a disposable, standalone integration test fixture that provisions **real** cloud resources (AWS first, then GCP and Azure) using Terraform, so Bellu's existing collectors can discover, normalize, resolve, graph, and risk-score them against known-correct expectations. It is explicitly **not** an emulator-based lab (no LocalStack, Moto, fake GCP, or Azurite as the primary implementation). Each test scenario provisions a small, cheap, intentionally-shaped cloud topology, declares the Bellu graph nodes/edges and attack paths it should produce in a machine-readable `scenario.yaml`, then runs the full Bellu pipeline (`bellu run`: ingest → normalize → resolve → persist → sync → risk) and asserts the resulting graph matches the declared expectations, before tearing everything down.

The Lab lives in a separate directory tree (`bellu-cloud-lab/`) and stays completely decoupled from Bellu production/customer infrastructure and from the Global Intelligence (`postgres-intel`/OpenSearch) plane. It reuses Bellu's existing interfaces and MUST NOT introduce new Bellu model Kinds, Relations, normalizers, or collector behavior.

### Reconciliation Decisions and Assumptions

These decisions resolve divergences between the requested topology and what Bellu actually emits today. They are assumptions baked into the requirements and the scenario contracts; they do not change Bellu behavior.

- **AUTHENTICATES_AS vs USES (Decision D1):** The AWS Normalizer does **not** emit `AUTHENTICATES_AS` for compute-to-identity links. It emits `USES` (EC2/Lambda/ECS → IAM role via instance profile / `role` / `task_role_arn`). Therefore scenario contracts for compute attaching to an identity MUST assert a `USES` edge, not `AUTHENTICATES_AS`. The Lab does not invent `AUTHENTICATES_AS` behavior.
- **Packages and Vulnerabilities enter via SBOM (Decision D2):** `Package`, `Vulnerability`, `DEPENDS_ON`, and `HAS_VULN` are not produced by the Steampipe cloud collectors. They enter Bellu through CycloneDX SBOM ingestion (the CycloneDX Normalizer and the `make sbom` flow). A scenario asserting package/vuln nodes MUST declare how the SBOM for the vulnerable image reaches Bellu as an explicit scenario input.
- **IAM→S3 permission edges are approximate (Decision D3):** `CAN_READ`/`CAN_WRITE`/`CAN_ADMIN` are derived by Bellu's approximate IAM evaluator at confidence 0.7 and ignore SCPs, permission boundaries, and policy conditions. Scenario assertions on these edges MUST tolerate the approximate semantics (match on relation + endpoints, not on exact effective-permission reasoning).
- **Attack-path reachability is a risk-engine input (Decision D4):** Bellu's risk engine (`internal/risk`) consumes a `Reachable` boolean (an attack path reaches the vulnerable asset) as a scoring factor; there is no standalone attack-path engine package in the current repo. Scenario `expected_attack_paths` are expressed as expected node/edge chains in the graph plus the expected `Reachable`/severity outcome of the risk score, not as output of a separate attack-path service.
- **AI-specific nodes may need the AI fixture mechanism (Decision D5):** Where a real cheap AWS AI service (Bedrock custom model, SageMaker model) is collectible, the Lab uses it to produce an `AIService` node. `AIAgent`/`AIModel`/`USES_MODEL` topology that the cloud collectors cannot produce is supplied through Bellu's AI fixture/metadata mechanism (the fixture source plus the `aiagent`/`aiapi` normalizers and `deriveAgents` step), declared as a scenario input.

## Glossary

- **Lab**: The Bellu Cloud Lab system — the Terraform, scenario definitions, Makefile targets, and integration test runner delivered under `bellu-cloud-lab/`.
- **Scenario**: A named, self-contained test fixture (e.g. `aws/public-ec2`) consisting of Terraform configuration plus a `scenario.yaml` contract. Identified by provider and scenario name.
- **Scenario_Contract**: The machine-readable `scenario.yaml` file declaring `expected_nodes`, `expected_edges`, and `expected_attack_paths` for a Scenario.
- **Test_Runner**: The Lab component that provisions a Scenario, runs the Bellu pipeline, compares the resulting graph against the Scenario_Contract, and reports PASS or FAIL.
- **Bellu_Pipeline**: Bellu's existing batch flow invoked by `bellu run`: ingest sources → RawEvent → Normalizer → EntityResolver → persist to Postgres → sync to Memgraph → risk scoring.
- **Collector**: A Bellu source that produces RawEvents; for cloud resources this is the Steampipe-based source driven by the provider catalog YAML.
- **Compatibility_Matrix**: A deliverable table mapping each cloud resource to its Terraform resource, the Bellu node Kind and edge Relation it produces, and whether it is covered by a tested Scenario.
- **Collector_Credentials**: The least-privilege cloud credentials Bellu uses to collect from a provisioned Scenario.
- **Test_Account**: A dedicated, isolated cloud account/project/subscription used only for intentionally-insecure Lab infrastructure.
- **Node**: A canonical Bellu graph entity of a Kind defined in `internal/model/model.go`.
- **Edge**: A canonical Bellu graph relationship of a Relation defined in `internal/model/model.go`.
- **Phase**: A delivery increment ordering scenarios and providers (Phase 1 AWS core → Phase 6 Azure).

## Requirements

### Requirement 1: Lab repository structure (Phase 1)

**User Story:** As a Bellu platform engineer, I want a clearly organized standalone Terraform directory tree, so that I can add cloud scenarios without touching Bellu production code.

#### Acceptance Criteria

1. THE Lab SHALL place all its files under a top-level `bellu-cloud-lab/` directory separate from Bellu application code.
2. THE Lab SHALL provide a provider directory for each of `aws`, `gcp`, and `azure` under `bellu-cloud-lab/`.
3. THE Lab SHALL provide, within each provider directory, a `scenarios/` directory, a `modules/` directory, and an `environments/` directory.
4. THE Lab SHALL provide a `scripts/` directory and a `README.md` file at the `bellu-cloud-lab/` root.
5. THE Lab SHALL implement all infrastructure definitions using Terraform.
6. WHERE infrastructure is defined, THE Lab SHALL use CloudFormation in zero resource definitions.
7. THE Lab SHALL isolate each Scenario and each environment using independent Terraform state rather than Terraform workspaces.

### Requirement 2: Reusable AWS Terraform modules (Phase 1)

**User Story:** As a Lab author, I want reusable AWS building-block modules, so that scenarios compose cheap infrastructure consistently.

#### Acceptance Criteria

1. THE Lab SHALL provide reusable AWS Terraform modules for VPC, public subnets, private subnets, route tables, and internet gateway.
2. THE Lab SHALL provide reusable AWS Terraform modules for security groups, EC2, IAM users, IAM roles, and IAM policies.
3. THE Lab SHALL provide reusable AWS Terraform modules for S3, RDS, EKS, Lambda, and CloudWatch logging.
4. WHERE a module exposes an instance size or tier, THE Lab SHALL default that size or tier to the smallest practical option.
5. THE Lab SHALL expose Terraform outputs from each module listing the identifiers of the resources that module created.

### Requirement 3: Scenario contract format

**User Story:** As a Lab author, I want each scenario to declare its expected Bellu graph in a machine-readable file, so that assertions live with the fixture and not inside Bellu.

#### Acceptance Criteria

1. THE Lab SHALL provide exactly one `scenario.yaml` Scenario_Contract file per Scenario.
2. THE Scenario_Contract SHALL contain a top-level `expected_nodes` section, a top-level `expected_edges` section, and a top-level `expected_attack_paths` section, each present as a (possibly empty) list.
3. THE Scenario_Contract SHALL express the Kind of every entry in `expected_nodes` as one of the Kind values enumerated in `AllKinds` in `internal/model/model.go`.
4. THE Scenario_Contract SHALL express the Relation of every entry in `expected_edges` as one of the Relation values enumerated in `AllRelations` in `internal/model/model.go`.
5. THE Scenario_Contract SHALL reference, for every node and edge named in each `expected_attack_paths` chain, only Kinds enumerated in `AllKinds` and Relations enumerated in `AllRelations` in `internal/model/model.go`.
6. THE Lab SHALL store all Scenario expectations in the Scenario_Contract and in zero Bellu source files.
7. WHEN the Test_Runner loads a Scenario_Contract, THE Test_Runner SHALL validate every Kind in `expected_nodes` and every Relation in `expected_edges` against `AllKinds` and `AllRelations` in `internal/model/model.go` before provisioning any resources.
8. IF a Scenario_Contract references a Kind absent from `AllKinds` or a Relation absent from `AllRelations` in `internal/model/model.go`, THEN THE Test_Runner SHALL fail validation of that Scenario_Contract, SHALL report each unknown Kind or Relation together with the contract entry that referenced it, and SHALL mark the entire Scenario_Contract invalid without accepting any of its entries.
9. IF a Scenario_Contract is missing the `expected_nodes`, `expected_edges`, or `expected_attack_paths` section, THEN THE Test_Runner SHALL fail validation of that Scenario_Contract and SHALL report the missing section.

### Requirement 4: Scenario 001 public-ec2 (Phase 1)

**User Story:** As a Bellu engineer, I want a public EC2 reaching an S3 bucket via an IAM role, so that I can verify exposure, compute, identity, and data-store collection end-to-end.

#### Acceptance Criteria

1. THE `aws/public-ec2` Scenario SHALL provision an EC2 instance, an IAM role attached via instance profile, an S3 bucket containing only synthetic non-sensitive data, and a security group with ingress open to `0.0.0.0/0`.
2. THE `aws/public-ec2` Scenario_Contract SHALL declare `expected_nodes` of Kind `Compute`, `Identity`, `DataStore`, and `NetworkEndpoint`.
3. THE `aws/public-ec2` Scenario_Contract SHALL declare a `USES` Edge from the `Compute` Node to the `Identity` Node. [Decision D1]
4. THE `aws/public-ec2` Scenario_Contract SHALL declare a `CAN_READ` Edge from the `Identity` Node to the `DataStore` Node. [Decision D3]
5. THE `aws/public-ec2` Scenario_Contract SHALL declare an `EXPOSED_TO` Edge from the security-group `NetworkEndpoint` to the internet `NetworkEndpoint` (`0.0.0.0/0`).
6. WHEN the Test_Runner evaluates the declared `CAN_READ` Edge, THE Test_Runner SHALL match on relation and endpoints and SHALL NOT require exact effective-permission reasoning. [Decision D3]

### Requirement 5: Scenario iam-to-s3 (Phase 1)

**User Story:** As a Bellu engineer, I want a focused IAM-to-S3 permission scenario, so that the approximate IAM evaluator's permission edges are validated end-to-end.

#### Acceptance Criteria

1. THE `aws/iam-to-s3` Scenario SHALL provision an IAM role or user with an attached policy granting S3 access and at least one S3 bucket targeted by that policy.
2. THE `aws/iam-to-s3` Scenario_Contract SHALL declare `expected_nodes` of Kind `Identity` and `DataStore`.
3. THE `aws/iam-to-s3` Scenario_Contract SHALL declare a permission Edge of Relation `CAN_READ`, `CAN_WRITE`, or `CAN_ADMIN` from the `Identity` Node to the `DataStore` Node consistent with the provisioned policy. [Decision D3]
4. WHEN the Test_Runner evaluates a permission Edge, THE Test_Runner SHALL accept the Edge at Bellu's assigned confidence of 0.7. [Decision D3]

### Requirement 6: Scenario 002 vulnerable-workload (Phase 2)

**User Story:** As a Bellu engineer, I want a workload running an image with a known vulnerable package, so that asset→package→vulnerability→risk correlation is validated.

#### Acceptance Criteria

1. THE `aws/vulnerable-workload` Scenario SHALL provision exactly one compute workload that runs a single container image, where the image contains at least one package pinned to a specific version that is affected by a published test CVE identified by a CVE-ID (format `CVE-YYYY-NNNN+`).
2. THE `aws/vulnerable-workload` Scenario SHALL declare, as a named Scenario input, the file path of a CycloneDX SBOM for the container image produced by the `make sbom` flow (output under `out/sboms/`). [Decision D2]
3. WHEN the Scenario is executed, THE `aws/vulnerable-workload` Scenario SHALL supply the declared CycloneDX SBOM to Bellu through the CycloneDX SBOM ingestion path (`bellu ingest`, normalized by `internal/normalize/cyclonedx`), and SHALL NOT rely on cloud collectors to introduce `Package`, `Vulnerability`, `DEPENDS_ON`, or `HAS_VULN` elements. [Decision D2]
4. IF the declared CycloneDX SBOM is absent, is not valid CycloneDX JSON, or does not contain the pinned vulnerable package, THEN THE `aws/vulnerable-workload` Scenario SHALL fail execution and SHALL emit an error indicating the SBOM could not be ingested, without producing a partial asset→package→vulnerability correlation.
5. THE `aws/vulnerable-workload` Scenario_Contract SHALL declare `expected_nodes` containing exactly one node of Kind `Compute` or `Workload`, exactly one node of Kind `ContainerImage`, at least one node of Kind `Package`, and at least one node of Kind `Vulnerability`.
6. THE `aws/vulnerable-workload` Scenario_Contract SHALL declare `expected_edges` containing a `RUNS_IMAGE` edge from the workload node to the image node, a `DEPENDS_ON` edge from the image node to the pinned vulnerable `Package` node, and a `HAS_VULN` edge from that `Package` node to the `Vulnerability` node matching the declared CVE-ID. [Decision D2]
7. THE `aws/vulnerable-workload` Scenario SHALL reference only a package affected by a published test CVE and SHALL include no malware artifact and no exploitation payload.

### Requirement 7: Scenario 003 attack path (Phase 2)

**User Story:** As a Bellu engineer, I want an internet-to-sensitive-data attack-path fixture, so that reachability-driven risk scoring is validated.

#### Acceptance Criteria

1. THE `aws/attack-path` Scenario SHALL provision exactly one externally exposed `Compute` resource, at least one `Vulnerability` node on a `Package` reachable on that `Compute` via a `HAS_VULN`/`DEPENDS_ON` chain, exactly one IAM `Identity` connected to that `Compute` by a `USES` Edge, and exactly one S3 `DataStore` to which that `Identity` holds a `CAN_READ`, `CAN_WRITE`, or `CAN_ADMIN` Edge.
2. THE `aws/attack-path` Scenario_Contract SHALL declare an `expected_attack_paths` chain of `NetworkEndpoint` → `Compute` → `Identity` → `DataStore` using an `EXPOSED_TO` Edge (NetworkEndpoint → Compute), a `USES` Edge (Compute → Identity), and one permission Edge of type `CAN_READ`, `CAN_WRITE`, or `CAN_ADMIN` (Identity → DataStore), where the permission Edge is matched on relation type and endpoints only and is tolerant of the IAM evaluator's approximate confidence of 0.7. [Decision D1, D3]
3. WHEN the Test_Runner evaluates the attack path, THE Test_Runner SHALL assert that the full `NetworkEndpoint` → `Compute` → `Identity` → `DataStore` chain exists in the Bellu graph using the Edge types declared in criterion 2. [Decision D4]
4. WHEN the Test_Runner has confirmed the chain exists, THE Test_Runner SHALL assert that the risk score computed for the vulnerable `DataStore` with `Reachable` satisfied equals 2.5 times (±0.5 after clamping to [0,100]) the score computed for the same inputs with `Reachable` unsatisfied, reflecting the risk engine's `exposure` factor of 1.0 versus 0.4. [Decision D4]
5. IF the declared `NetworkEndpoint` → `Compute` → `Identity` → `DataStore` chain is absent or incomplete in the Bellu graph, THEN THE Test_Runner SHALL fail the scenario and SHALL report an error indicating which Edge or node of the chain is missing. [Decision D4]
6. THE `aws/attack-path` Scenario SHALL express attack-path expectations solely as graph node/edge chains plus the expected `Reachable`-driven risk outcome and SHALL NOT depend on a standalone attack-path service. [Decision D4]

### Requirement 8: Scenario 004 EKS identity chain (Phase 3)

**User Story:** As a Bellu engineer, I want an EKS IRSA chain fixture, so that workload→kubernetes-identity→cloud-identity→data-store resolution is validated.

#### Acceptance Criteria

1. THE `aws/eks-identity-chain` Scenario SHALL provision a minimal EKS cluster, a Pod, a Kubernetes ServiceAccount annotated for IRSA with an AWS IAM role ARN, and an S3 bucket the role can access.
2. THE `aws/eks-identity-chain` Scenario SHALL annotate the ServiceAccount with the `eks.amazonaws.com/role-arn` annotation so the k8s workload-identity normalizer resolves the binding.
3. THE `aws/eks-identity-chain` Scenario_Contract SHALL declare `expected_nodes` including `Cluster`, `Workload`, `Identity`, and `DataStore`.
4. THE `aws/eks-identity-chain` Scenario_Contract SHALL declare an identity-resolution chain linking the workload to the AWS IAM `Identity` and that `Identity` to the `DataStore` via a permission Edge. [Decision D3]
5. WHERE the EKS cluster exposes a node or control-plane size, THE Lab SHALL default that size to the smallest practical option.

### Requirement 9: Scenario 005 AI-agent (Phase 4)

**User Story:** As a Bellu engineer, I want an AI-agent scenario, so that AI node/edge topology connected to cloud identity and data is validated.

#### Acceptance Criteria

1. WHERE a cheap and safe AWS AI service (Bedrock custom model or SageMaker model) is collectible, THE `aws/ai-agent` Scenario SHALL provision that service to produce an `AIService` Node. [Decision D5]
2. THE `aws/ai-agent` Scenario SHALL provision supporting IAM identity and S3 or external-endpoint resources for the AI service to reference.
3. WHERE the cloud collectors cannot produce `AIAgent`, `AIModel`, or `USES_MODEL` topology, THE `aws/ai-agent` Scenario SHALL supply that topology through Bellu's AI fixture/metadata mechanism as a declared Scenario input. [Decision D5]
4. THE `aws/ai-agent` Scenario_Contract SHALL declare `expected_nodes` and `expected_edges` using Kinds and Relations defined in `internal/model/model.go`, including `AIService` and, where supplied by fixture, `AIAgent`, `AIModel`, and `USES_MODEL`.

### Requirement 10: Integration test runner

**User Story:** As a Bellu engineer, I want an automated runner that provisions, tests, and destroys a scenario, so that integration testing is one command and leaves no resources behind.

#### Acceptance Criteria

1. WHEN the Test_Runner is invoked for a Scenario, THE Test_Runner SHALL run `terraform apply` for that Scenario and poll for provisioned-resource readiness until all resources report a ready state or a readiness timeout of 600 seconds elapses, whichever occurs first.
2. IF the readiness timeout of 600 seconds elapses before all provisioned resources report a ready state, THEN THE Test_Runner SHALL report a provisioning-timeout failure, record the Scenario result as FAIL, and run `terraform destroy` for that Scenario.
3. WHEN all provisioned resources report a ready state, THE Test_Runner SHALL run the existing `bellu run` Bellu_Pipeline over the Scenario and then query the Bellu API for the resulting graph, falling back to the Postgres database only when the Bellu API is unreachable.
4. IF the Bellu_Pipeline does not complete within 900 seconds of being started, or exits with a non-zero status, THEN THE Test_Runner SHALL report a pipeline failure, record the Scenario result as FAIL, and run `terraform destroy` for that Scenario.
5. WHEN the Bellu_Pipeline completes successfully, THE Test_Runner SHALL compare the resulting graph nodes, graph relations, and risk outcomes against those declared in the Scenario_Contract, record the Scenario result as PASS when every element declared in the Scenario_Contract is present with matching values, and record the Scenario result as FAIL when any declared element is missing or has a non-matching value.
6. WHEN the comparison completes, THE Test_Runner SHALL run `terraform destroy` for that Scenario regardless of the PASS or FAIL result.
7. IF `terraform apply` fails for a Scenario, THEN THE Test_Runner SHALL report the apply failure, record the Scenario result as FAIL, and run `terraform destroy` for that Scenario.
8. IF `terraform destroy` fails for a Scenario, THEN THE Test_Runner SHALL report a destroy failure with an error indicating that resources may remain provisioned and SHALL exit with a non-zero status.
9. THE Test_Runner SHALL reuse the existing `bellu run` pipeline and SHALL introduce no new Bellu model Kinds, Relations, or normalizers.

### Requirement 11: Makefile commands

**User Story:** As a Bellu engineer, I want make targets consistent with the existing Makefile, so that running the Lab matches existing workflows.

#### Acceptance Criteria

1. THE Lab SHALL provide `make lab-up SCENARIO=<provider>/<name>`, `make lab-test SCENARIO=<provider>/<name>`, and `make lab-down SCENARIO=<provider>/<name>` targets.
2. WHEN `make lab-up SCENARIO=<provider>/<name>` is invoked, THE Lab SHALL provision the named Scenario without running assertions or destroying resources.
3. WHEN `make lab-test SCENARIO=<provider>/<name>` is invoked, THE Lab SHALL run the Test_Runner for the named Scenario.
4. WHEN `make lab-down SCENARIO=<provider>/<name>` is invoked, THE Lab SHALL destroy the named Scenario's resources.
5. THE Lab SHALL provide a `make integration-test-cloud` target that runs the Test_Runner across a selected set of Scenarios.
6. THE Lab SHALL provide a `make destroy-all` target that destroys all Lab-provisioned resources.
7. THE Lab SHALL define Lab make targets using the existing Makefile's style of `go` and `docker compose` invocations.

### Requirement 12: Credentials and security isolation

**User Story:** As a security engineer, I want credentials handled natively and insecure infra isolated, so that the Lab never leaks secrets or endangers real environments.

#### Acceptance Criteria

1. THE Lab SHALL store cloud credentials in zero Terraform files, scenario files, and committed configuration files.
2. THE Lab SHALL authenticate to AWS using an AWS profile, environment variables, or an assumed role.
3. THE Lab SHALL authenticate to GCP using Application Default Credentials or service-account impersonation.
4. THE Lab SHALL authenticate to Azure using the Azure CLI, a service principal, or a managed identity.
5. THE Lab SHALL grant Collector_Credentials the least privilege required for Bellu collection of the provisioned Scenario.
6. THE Lab SHALL provision intentionally-insecure test infrastructure only within a dedicated Test_Account.

### Requirement 13: Cost control

**User Story:** As a Bellu operator, I want the Lab to minimize spend, so that running integration tests stays cheap and nothing is left running.

#### Acceptance Criteria

1. THE Lab SHALL default compute instances to the smallest practical instance type.
2. THE Lab SHALL default managed-cluster scenarios (EKS, GKE, AKS) to the smallest practical cluster configuration.
3. THE Lab SHALL provision zero GPU resources.
4. THE Lab SHALL provision no NAT gateway and no load balancer unless a Scenario's declared expectations require one.
5. THE Lab SHALL apply resource tags or labels identifying Lab-provisioned resources on every created resource that supports tagging.
6. WHEN a Test_Runner completes a Scenario, THE Lab SHALL destroy that Scenario's resources automatically.
7. THE Lab SHALL expose Terraform outputs listing the resources created by each Scenario.

### Requirement 14: No emulators as primary implementation

**User Story:** As a Bellu engineer, I want integration tests to exercise real cloud APIs, so that collector behavior is validated against actual provider responses.

#### Acceptance Criteria

1. THE Lab SHALL provision real cloud resources through provider APIs for all primary integration-test Scenarios.
2. THE Lab SHALL use LocalStack, Moto, fake GCP, and Azurite in zero primary integration-test Scenarios.
3. WHERE emulators are used, THE Lab SHALL restrict them to optional fast unit tests outside the primary integration-test Scenarios.

### Requirement 15: Cloud-to-Bellu compatibility matrix

**User Story:** As a Bellu engineer, I want a matrix of cloud resources to Bellu nodes and edges, so that collector coverage and gaps are visible.

#### Acceptance Criteria

1. THE Lab SHALL deliver a Compatibility_Matrix with columns for cloud resource, Terraform resource, Bellu node, Bellu edge, and tested status.
2. THE Compatibility_Matrix SHALL reflect the actual coverage of Bellu's current collectors and normalizers.
3. IF a provisioned cloud resource cannot be collected by Bellu's current collectors, THEN THE Compatibility_Matrix SHALL flag that resource as not collectible.
4. THE Lab SHALL populate the Compatibility_Matrix for AWS in Phase 1 and extend it to GCP and Azure in their respective phases.

### Requirement 16: GCP scenario parity

**User Story:** As a Bellu engineer, I want structurally comparable GCP scenarios, so that multi-cloud normalization is validated.

#### Acceptance Criteria

1. THE Lab SHALL provide GCP reusable modules and scenarios covering VPC, subnet, firewall, Compute Engine, IAM service accounts, and Cloud Storage.
2. WHERE practical, THE Lab SHALL provide GCP scenarios covering GKE, Cloud SQL, and Vertex AI.
3. THE Lab SHALL structure GCP scenarios comparably to the corresponding AWS scenarios for cross-provider normalization comparison.
4. THE Lab SHALL express GCP Scenario_Contract expectations using Kinds and Relations defined in `internal/model/model.go`.

### Requirement 17: Azure scenario parity

**User Story:** As a Bellu engineer, I want structurally comparable Azure scenarios, so that multi-cloud normalization is validated across three providers.

#### Acceptance Criteria

1. THE Lab SHALL provide Azure reusable modules and scenarios covering VNet, subnet, NSG, Virtual Machine, managed identity, and Blob Storage.
2. WHERE practical, THE Lab SHALL provide Azure scenarios covering AKS, Azure SQL, and Azure OpenAI.
3. THE Lab SHALL structure Azure scenarios comparably to the corresponding AWS scenarios for cross-provider normalization comparison.
4. THE Lab SHALL express Azure Scenario_Contract expectations using Kinds and Relations defined in `internal/model/model.go`.

### Requirement 18: Architectural decoupling

**User Story:** As a Bellu architect, I want the Lab fully decoupled from production and intelligence planes, so that testing never touches customer or intelligence data.

#### Acceptance Criteria

1. THE Lab SHALL operate without connecting to Bellu production or customer infrastructure.
2. THE Lab SHALL operate without connecting to the Global Intelligence `postgres-intel` database and the Global Intelligence OpenSearch index.
3. THE Lab SHALL leave Bellu's existing architecture, model, normalizers, and collectors unchanged.

### Requirement 19: Phased delivery sequencing

**User Story:** As a Bellu program lead, I want delivery sequenced by phase, so that AWS core value lands first and multi-cloud follows.

#### Acceptance Criteria

1. THE Lab SHALL deliver Phase 1 (AWS VPC/EC2/IAM/S3 modules, `aws/public-ec2`, and `aws/iam-to-s3` working end-to-end) before any other phase.
2. THE Lab SHALL deliver Phase 2 (`aws/vulnerable-workload` and vulnerability correlation) after Phase 1 and before Phase 3.
3. THE Lab SHALL deliver Phase 3 (`aws/eks-identity-chain`) after Phase 2 and before Phase 4.
4. THE Lab SHALL deliver Phase 4 (`aws/ai-agent`) after Phase 3 and before Phase 5.
5. THE Lab SHALL deliver Phase 5 (GCP scenarios) after the first two AWS scenarios work end-to-end and after Phase 4.
6. THE Lab SHALL deliver Phase 6 (Azure scenarios) after Phase 5.

### Requirement 20: Setup, credential, and cost documentation

**User Story:** As a new Lab user, I want setup, credential, and cost documentation, so that I can run scenarios safely and cheaply.

#### Acceptance Criteria

1. THE Lab README SHALL document how to configure credentials for AWS, GCP, and Azure using native mechanisms.
2. THE Lab README SHALL document the cost-control measures and how to run `make destroy-all`.
3. THE Lab README SHALL document CI-ready commands for running a selected set of Scenarios.
4. THE Lab README SHALL document the Scenario_Contract format and how expectations map to Bellu Kinds and Relations.
