package capability

import (
	"strings"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/source/steampipe"
)

// TestCatalogCoversSteampipeAWSTables is the guard that ends permission
// trial-and-error: it loads the Steampipe AWS catalog (the exact tables Bellu
// queries during discovery) and asserts that, for every table, the enabled
// capabilities grant a permission for that table's AWS service. If someone adds
// a table to internal/source/steampipe/catalog/aws.yaml (e.g. a new AI service)
// without granting its permission here, this test fails at build time instead
// of surfacing as an AccessDenied in production.
//
// It checks service-prefix coverage (the common failure mode: a whole service
// ungranted, like bedrock/eks). It does not assert per-action completeness —
// the per-table action lists are derived from the plugin source and encoded in
// the capability bundles directly.
func TestCatalogCoversSteampipeAWSTables(t *testing.T) {
	cat, err := steampipe.LoadCatalog("aws.yaml")
	if err != nil {
		t.Fatalf("load steampipe aws catalog: %v", err)
	}

	// The capabilities a default connection enables. Discovery runs with these,
	// so every queried table's service must be covered by their union.
	granted := grantedServices(DefaultSelection())

	// Map a Steampipe table name to the IAM service that authorizes it. The
	// prefix after "aws_" is usually the IAM service, with a few known aliases.
	for _, tbl := range cat.Tables {
		svc := iamServiceForTable(tbl.Table)
		if svc == "" {
			t.Errorf("table %q: could not derive an IAM service prefix", tbl.Table)
			continue
		}
		if !granted[svc] {
			t.Errorf("table %q needs IAM service %q, but no enabled capability grants it — add a permission to the capability catalog (capability.go)", tbl.Table, svc)
		}
	}
}

// grantedServices returns the set of IAM service prefixes granted by the given
// capabilities (e.g. "ec2", "s3", "bedrock"), derived from each permission's
// "<service>:<action>" form.
func grantedServices(caps []string) map[string]bool {
	out := map[string]bool{}
	for _, c := range caps {
		def, ok := Lookup(c)
		if !ok {
			continue
		}
		for _, perm := range def.Permissions {
			if i := strings.IndexByte(perm, ':'); i > 0 {
				out[perm[:i]] = true
			}
		}
	}
	return out
}

// iamServiceForTable maps a Steampipe aws_* table name to the IAM service that
// authorizes its API calls. Most follow aws_<service>_<resource>; the aliases
// cover tables whose plugin name differs from the IAM service namespace.
func iamServiceForTable(table string) string {
	rest := strings.TrimPrefix(table, "aws_")
	if rest == table {
		return "" // not an aws_ table
	}
	// Known tables whose authorizing IAM service isn't simply the first segment.
	switch table {
	case "aws_account":
		// aws_account authorizes via iam:ListAccountAliases (+ organizations).
		return "iam"
	case "aws_vpc_security_group":
		// VPC security groups are authorized by ec2:DescribeSecurityGroups.
		return "ec2"
	}
	// Default: first path segment is the IAM service (iam, s3, ec2, eks, ecs,
	// ecr, lambda, bedrock, sagemaker, ...).
	if i := strings.IndexByte(rest, '_'); i > 0 {
		return rest[:i]
	}
	return rest
}
