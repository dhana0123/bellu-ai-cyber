# terraform init -backend-config=environments/dev/ap-south-1.backend.hcl
# Replace <STATE_BUCKET> with your Terraform state bucket (versioned, encrypted).
bucket       = "<STATE_BUCKET>"
key          = "global-intel/dev/ap-south-1/terraform.tfstate"
region       = "ap-south-1"
encrypt      = true
use_lockfile = true
