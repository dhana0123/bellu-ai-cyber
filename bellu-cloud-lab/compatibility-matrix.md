# Cloud → Bellu compatibility matrix

Which provisioned cloud resources Bellu's **current** collectors + normalizers
turn into graph nodes/edges. Reflects actual coverage in
`internal/source/steampipe/catalog/aws.yaml` + `internal/normalize/aws.go`. Rows
flagged "not collectible" document a real gap rather than hiding it
(Requirement 15.3).

## AWS (Phase 1)

| Cloud resource | Terraform resource | Bellu node | Bellu edge | Tested |
| --- | --- | --- | --- | --- |
| EC2 instance | `aws_instance` | `Compute` | `USES` → role | ✅ public-ec2 |
| IAM role | `aws_iam_role` | `Identity` | `CONTAINS`, `CAN_READ/WRITE/ADMIN` → S3 | ✅ public-ec2, iam-to-s3 |
| IAM user | `aws_iam_user` | `Identity` | `CAN_*` → S3 | ⬜ (role used instead) |
| S3 bucket | `aws_s3_bucket` | `DataStore` | — | ✅ public-ec2, iam-to-s3 |
| Security group (open `0.0.0.0/0`) | `aws_security_group` | `NetworkEndpoint` (+ synthetic internet) | `EXPOSED_TO` | ✅ public-ec2 |
| Security group (locked down) | `aws_security_group` | — (no node) | — | ⚠️ not collectible |
| VPC | `aws_vpc` | — | — | ⚠️ not collectible |
| Subnet | `aws_subnet` | — | — | ⚠️ not collectible |
| Route table | `aws_route_table` | — | — | ⚠️ not collectible |
| Internet gateway | `aws_internet_gateway` | — | — | ⚠️ not collectible |

### Notes

- **VPC / subnet / route-table / IGW produce no graph nodes today** — `aws.go`
  has no normalizer case for them. The lab provisions them because they're
  required scaffolding for a reachable EC2 instance, but they are correctly
  flagged not collectible.
- A **security group becomes a `NetworkEndpoint`** only when it is open to
  `0.0.0.0/0` (the `sgOpenToWorld` path). A locked-down SG yields nothing.
- **IAM → S3 permission edges** (`CAN_READ/WRITE/ADMIN`) come from Bellu's
  approximate IAM evaluator at confidence **0.7** and ignore SCPs, permission
  boundaries, and policy conditions (Decision D3). Contracts match these edges
  on relation + endpoints only.

## Later phases (provisioned, tested in their phase)

| Cloud resource | Terraform resource | Bellu node | Bellu edge | Tested |
| --- | --- | --- | --- | --- |
| Lambda function | `aws_lambda_function` | `Compute` | `USES` | ⬜ later |
| ECS task definition | `aws_ecs_task_definition` | `Workload` | `USES`, `RUNS_IMAGE` | ⬜ vulnerable-workload |
| EKS cluster | `aws_eks_cluster` | `Cluster` | — | ⬜ eks-identity-chain |
| Bedrock / SageMaker model | `aws_bedrock_custom_model` / `aws_sagemaker_model` | `AIService` | `CONTAINS` | ⬜ ai-agent |
| Package / Vulnerability | — (via CycloneDX SBOM ingest) | `Package`, `Vulnerability` | `DEPENDS_ON`, `HAS_VULN` | ⬜ vulnerable-workload |

GCP and Azure rows are added in Phases 5 and 6.
