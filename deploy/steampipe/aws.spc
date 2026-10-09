# Steampipe AWS connection. Credentials are supplied at runtime via AWS_*
# environment variables (the connector injects the per-session assumed-role
# credentials). No profile and no baked-in keys. The plugin is referenced by
# its full GHCR path because that is the registry the plugin installs from.
connection "aws" {
  plugin  = "ghcr.io/turbot/steampipe/plugins/turbot/aws@latest"
  regions = ["*"]
}
