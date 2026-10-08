# terraform init -backend-config=environments/prod/us-east-1.backend.hcl
# Replace <STATE_BUCKET> with your Terraform state bucket (versioned, encrypted).
bucket       = "<STATE_BUCKET>"
key          = "global-intel/prod/us-east-1/terraform.tfstate"
region       = "us-east-1"
encrypt      = true
use_lockfile = true
