# The AWS provider region comes from var.region so one codebase deploys to any
# region. Every created resource is tagged with the standard set via
# default_tags, so operators can filter the whole stack by env/region/stack.
provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Stack       = "global-intel"
      Environment = var.environment
      Region      = var.region
      ManagedBy   = "terraform"
      Service     = "global-intel"
    }
  }
}

provider "random" {}
