# AWS environments

State is isolated **per scenario** (each scenario is its own root Terraform
module with its own `terraform.tfstate`) and **per environment** (via distinct
backend configs). Isolation is by state key, **not** Terraform workspaces
(Requirement 1.7).

## Local (default)

The default backend is local: `terraform.tfstate` lives under each scenario
directory. Nothing to configure — `make lab-up SCENARIO=aws/public-ec2` just
works with your ambient AWS credentials.

Pass the env tfvars when applying manually:

```bash
cd aws/scenarios/public-ec2
terraform init
terraform apply -var-file=../../environments/local.tfvars
```

## Remote backend (optional)

To share state (e.g. in CI), drop an `<env>.backend.hcl` here referencing an
S3 backend and init with it — the Lab never hardcodes backend credentials; they
come from ambient cloud auth:

```hcl
# example: prod.backend.hcl
bucket = "my-tf-state"
key    = "bellu-lab/aws/public-ec2/prod.tfstate"
region = "us-east-1"
```

```bash
terraform init -backend-config=../../environments/prod.backend.hcl
```
