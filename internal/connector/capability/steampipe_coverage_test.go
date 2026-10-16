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

	// Gating is a scan-time concern: a table is collected only when the
	// capability it declares is enabled. The guard therefore checks that each
	// table's OWN declared capability grants a permission for that table's
	// service. (Core/ungated tables are validated against the always-on Core
	// bundle.) This keeps opt-in capabilities like DSPM meaningful instead of
	// forcing their tables' services into the default set.
	for _, tbl := range cat.Tables {
		svc := iamServiceForTable(tbl.Table)
		if svc == "" {
			t.Errorf("table %q: could not derive an IAM service prefix", tbl.Table)
			continue
		}
		cap := tbl.Capability
		if cap == "" {
			cap = string(Core)
		}
		granted := grantedServices([]string{string(Core), cap})
		if !granted[svc] {
			t.Errorf("table %q (capability %q) needs IAM service %q, but that capability grants no permission for it — add a permission to the capability catalog (capability.go)", tbl.Table, cap, svc)
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
	case "aws_vpc", "aws_subnet", "aws_vpc_route_table", "aws_vpc_internet_gateway",
		"aws_vpc_nat_gateway", "aws_vpc_peering_connection":
		// VPC topology tables are authorized by ec2:Describe* (DescribeVpcs,
		// DescribeSubnets, DescribeRouteTables, DescribeInternetGateways,
		// DescribeNatGateways, DescribeVpcPeeringConnections).
		return "ec2"
	case "aws_efs_file_system":
		// EFS is authorized by elasticfilesystem:Describe*.
		return "elasticfilesystem"
	case "aws_api_gateway_rest_api", "aws_api_gatewayv2_api":
		// API Gateway is authorized by apigateway:GET.
		return "apigateway"
	}
	// Default: first path segment is the IAM service (iam, s3, ec2, eks, ecs,
	// ecr, lambda, bedrock, sagemaker, ...).
	if i := strings.IndexByte(rest, '_'); i > 0 {
		return rest[:i]
	}
	return rest
}
