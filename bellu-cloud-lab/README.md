# Bellu Cloud Lab

A standalone, **disposable** integration-test harness. It provisions **real**
cloud resources with Terraform, lets Bellu's existing collectors discover them,
runs the unmodified `bellu run` pipeline, asserts the resulting graph and risk
outcomes against a machine-readable `scenario.yaml` contract, then **destroys
everything** regardless of outcome.

The Lab is a *consumer* of Bellu. It adds no new model Kinds, Relations,
normalizers, or collector behavior. Every expectation lives in a scenario
contract, validated against Bellu's own allow-lists (`internal/model`).

> ⚠️ The Lab provisions intentionally-insecure infrastructure (e.g. a security
> group open to `0.0.0.0/0`). Run it **only** in a dedicated, isolated test
> account — never in production or a shared account.

## Layout

```
bellu-cloud-lab/
├── cmd/lab/              # the Test_Runner command
├── internal/
│   ├── contract/         # scenario.yaml schema + validating loader
│   ├── graphquery/       # Bellu API client + Postgres fallback reader
│   ├── report/           # PASS/FAIL result model
│   └── runner/           # terraform + bellu run orchestration
├── aws/
│   ├── modules/          # reusable building blocks (vpc, ec2, iam, s3, ...)
│   ├── scenarios/        # root modules + scenario.yaml per scenario
│   └── environments/     # per-env backend + tfvars (independent state)
├── gcp/   azure/          # later phases, same shape
├── scripts/
├── Makefile.lab          # included from the repo Makefile
└── compatibility-matrix.md
```

## Prerequisites

- **Terraform** ≥ 1.5 on `PATH`.
- The built **`bellu`** binary (`make build` → `bin/bellu`).
- A running **Steampipe** service with the `aws` connection (the collectors
  compose profile, or `scripts/steampipe-up.sh`).
- Running Bellu customer plane (Postgres + Memgraph + control-panel API) so the
  runner can execute `bellu run` and query the graph.

## Credentials (no secrets in files)

The Lab writes **zero** secrets into Terraform, scenario, or committed config
files. Authenticate natively:

- **AWS** — an AWS profile (`AWS_PROFILE`), environment variables
  (`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_REGION`), or an assumed
  role. Steampipe's AWS plugin uses the same ambient credentials.
- **GCP** (later) — Application Default Credentials or service-account
  impersonation.
- **Azure** (later) — Azure CLI, a service principal, or a managed identity.

The collector only needs least-privilege read-only access (`Describe*` /
`List*` / `Get*`).

## Usage

```bash
# Provision a scenario only (no assertions, no destroy)
make lab-up   SCENARIO=aws/public-ec2

# Full cycle: apply → bellu run → compare → destroy
make lab-test SCENARIO=aws/public-ec2

# Destroy a scenario's resources
make lab-down SCENARIO=aws/public-ec2

# Run the runner across the Phase 1 scenario set
make integration-test-cloud

# Destroy everything the Lab provisioned
make destroy-all
```

## Cost control

- Smallest practical instance types (EC2 defaults to `t3.micro`).
- No NAT gateway, no load balancer, no GPU unless a scenario requires it.
- Every taggable resource carries a `bellu-lab` tag set.
- The runner **always** runs `terraform destroy` when a scenario finishes.

## Scenario contract format

Each scenario has exactly one `scenario.yaml` declaring the Bellu graph it
should produce. Kinds and Relations must be values from `internal/model`
(`AllKinds` / `AllRelations`); the loader rejects any unknown token before
provisioning.

```yaml
name: aws/public-ec2
expected_nodes:
  - { kind: Compute,         name: public-ec2 }
  - { kind: Identity,        name: public-ec2-role }
  - { kind: DataStore,       name: public-ec2-bucket }
  - { kind: NetworkEndpoint, name: "0.0.0.0/0" }
expected_edges:
  - { relation: USES,       from: Compute,  to: Identity }
  - { relation: CAN_READ,   from: Identity, to: DataStore, min_confidence: 0.7 }
  - { relation: EXPOSED_TO, from: NetworkEndpoint, to: "0.0.0.0/0" }
expected_attack_paths: []
```

See `compatibility-matrix.md` for which cloud resources map to which Bellu
nodes/edges today.
