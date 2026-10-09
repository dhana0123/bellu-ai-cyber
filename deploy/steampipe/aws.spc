# Steampipe AWS connection. Credentials are supplied at runtime via AWS_*
# environment variables (the connector injects the per-session assumed-role
# credentials). No profile and no baked-in keys.
connection "aws" {
  plugin  = "aws"
  regions = ["*"]
}
