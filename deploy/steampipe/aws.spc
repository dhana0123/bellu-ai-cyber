# Steampipe AWS connection. Mount read-only AWS credentials (SecurityAudit
# policy is sufficient) into the steampipe container and reference a profile
# here. Credentials are never baked into the image.
connection "aws" {
  plugin = "aws"
  # Use a read-only profile. The profile is resolved from the mounted
  # ~/.aws/credentials inside the container.
  profile = "bellu-readonly"
  regions = ["us-east-1", "us-west-2"]
}
