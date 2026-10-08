# global-intel — AWS deployment (Terraform)

Infrastructure for the Global Intelligence ingestion plane. Terraform provisions
the stack; the `global-intel` CLI (image built from `Dockerfile.intel`) fetches,
normalizes, stores, indexes, and releases intelligence. Infra and ingestion stay
separate: Terraform never fetches data; it schedules the pipeline that does.

## What it creates

- **VPC** — public/private subnets across 2 AZs, NAT (shared in dev, per-AZ in
  prod), and an S3 gateway endpoint. Security groups: task = egress-only;
  RDS/OpenSearch accept ingress only from the task SG.
- **S3** — private, KMS-encrypted, versioned `raw` (Object Lock = immutable
  upstream payloads) and `releases` (signed `.tar.zst`) buckets; TLS-only.
- **RDS PostgreSQL** — canonical system of record; Multi-AZ + deletion
  protection in prod.
- **Amazon OpenSearch** — fast search/index layer (rebuildable from Postgres);
  VPC-internal, encrypted, HTTPS-only.
- **ECS Fargate** — cluster + task definition running the `global-intel` CLI;
  ECR repo for the image.
- **EventBridge** — scheduled daily full-run that `RunTask`s the Fargate task.
- **Secrets Manager + KMS** — generated DB credentials + DSN; one CMK encrypts
  RDS/OpenSearch/S3/secret at rest.
- **CloudWatch Logs** — task logs with configurable retention.

## Layout

```
terraform/
  main.tf versions.tf providers.tf variables.tf outputs.tf
  modules/{vpc,s3,rds,opensearch,ecs,eventbridge,iam,secrets,cloudwatch}/
  environments/{dev,staging,prod}/{us-east-1,ap-south-1}.{tfvars,backend.hcl}
```

One reusable root module; sizing/HA come entirely from the per-env/region
tfvars. State is in S3 with **native locking** (`use_lockfile = true`, no
DynamoDB), one explicit state key per env+region
(`global-intel/<env>/<region>/terraform.tfstate`). No Terraform workspaces —
explicit keys keep prod isolation hard to break by accident.

## Prerequisites

1. A **versioned, encrypted S3 bucket** for Terraform state (one per account is
   fine). Put its name in every `environments/*/*.backend.hcl` (replace
   `<STATE_BUCKET>`).
2. `terraform >= 1.11`, `aws` CLI, `docker`, and AWS credentials for the target
   account/region.

## Deploy

```bash
# dev / us-east-1
terraform -chdir=terraform init -backend-config=environments/dev/us-east-1.backend.hcl
terraform -chdir=terraform plan  -var-file=environments/dev/us-east-1.tfvars
terraform -chdir=terraform apply -var-file=environments/dev/us-east-1.tfvars

# or use the helper (also builds + pushes the global-intel image):
./deploy.sh dev us-east-1 apply
./deploy.sh prod ap-south-1 apply
```

First apply on a fresh account creates the ECR repo; re-run `deploy.sh` to build,
push the image, and roll the task definition to it. Alternatively set
`container_image` in the tfvars to a pre-built image URI.

## Adding a region

Copy an existing pair, e.g. `environments/prod/us-east-1.{tfvars,backend.hcl}` →
`environments/prod/eu-west-1.*`, change `region`, the `key` in the backend file,
and `vpc_cidr` (keep CIDRs distinct per region). Then
`./deploy.sh prod eu-west-1 apply`. No module changes needed.

## App ↔ infra contract

The task definition injects these to the `global-intel` container:
`BELLU_RAW_BUCKET`, `BELLU_RELEASES_BUCKET`, `BELLU_SEARCH_ENDPOINT`,
`BELLU_ENVIRONMENT`, `AWS_REGION`, and (from Secrets Manager)
`BELLU_DB_HOST/PORT/NAME/USER/PASSWORD` plus `BELLU_POSTGRES_DSN`.

The CLI reads `BELLU_POSTGRES_DSN` for the database and, when `BELLU_RAW_BUCKET`
is set (as the task definition sets it), stores raw intelligence payloads in
that S3 bucket via the task role — content-addressed and immutable, matching the
raw bucket's Object Lock. Set the optional `BELLU_RAW_PREFIX` to namespace the
keys. With no `BELLU_RAW_BUCKET` (local dev) the CLI falls back to the
filesystem raw store under `--raw-dir`.

Search indexing is folded into the `run` pipeline: when `BELLU_SEARCH_ENDPOINT`
is set (the task definition sets it), a successful run indexes the just-persisted
canonical corpus into OpenSearch within the same Fargate task — no separate
scheduled job. PostgreSQL stays the system of record and is written first; the
index is updated only after that commit, so an index failure fails the run
(INDEX_FAILED) without corrupting the authoritative store, and the next run (or
`global-intel update --rebuild`) catches the index up. With no endpoint (local
dev) indexing is skipped.
