// Package aws implements the Cloud_Provider adapter for AWS: cross-account
// authentication via STS AssumeRole with a per-connection external id,
// least-privilege setup generation, read-only permission verification, and the
// discovery plan handed to the existing Steampipe source. It persists no
// credentials and holds no customer long-lived keys.
package aws

// IAM policy document helpers. These render plain map[string]any so they
// serialize to the exact JSON shape AWS expects while remaining easy to embed
// in the provider.Setup returned over the API.

// policyDocument builds a 2012-10-17 policy document from statements.
func policyDocument(statements ...map[string]any) map[string]any {
	return map[string]any{
		"Version":   "2012-10-17",
		"Statement": statements,
	}
}

// trustPolicy renders the customer-role trust policy: it allows the Bellu
// principal to assume the role, conditioned on a matching sts:ExternalId.
func trustPolicy(belluPrincipalARN, externalID string) map[string]any {
	return policyDocument(map[string]any{
		"Effect":    "Allow",
		"Principal": map[string]any{"AWS": belluPrincipalARN},
		"Action":    "sts:AssumeRole",
		"Condition": map[string]any{
			"StringEquals": map[string]any{"sts:ExternalId": externalID},
		},
	})
}

// permissionStatement builds one least-privilege read-only Allow statement for
// a capability. Resource is "*" (acceptable for read-only Describe/List/Get),
// but the action set is always explicitly enumerated — never "*".
func permissionStatement(sid string, actions []string) map[string]any {
	return map[string]any{
		"Sid":      sid,
		"Effect":   "Allow",
		"Action":   actions,
		"Resource": "*",
	}
}
