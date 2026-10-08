// Package capability defines the Connector's modular, least-privilege
// capability catalog. Capabilities are granted progressively so a connection
// never requires more permission than a feature needs; no capability requires
// AdministratorAccess or wildcard full access.
package capability

// Capability is a named, modular permission bundle.
type Capability string

const (
	Core     Capability = "core"
	EKS      Capability = "eks"
	Workload Capability = "workload"
	DSPM     Capability = "dspm"
)

// Definition is a capability's least-privilege, read-oriented permission set
// plus the concrete read-only probes the Permission Verifier runs for it.
type Definition struct {
	Name        Capability
	Description string
	// Permissions are the IAM actions the capability requires. They are least
	// privilege and read-oriented; none is a write/modify/delete action, and
	// none is AdministratorAccess or a "*" wildcard.
	Permissions []string
	// Probes are the read-only API checks the Permission Verifier issues to
	// confirm the capability's permissions are granted. Each probe maps to one
	// of Permissions.
	Probes []Probe
}

// Probe is a single read-only permission check for a capability.
type Probe struct {
	// Permission is the IAM action this probe exercises.
	Permission string
}

// Catalog is the authoritative set of capabilities and their permission bundles.
var Catalog = map[Capability]Definition{
	Core: {
		Name:        Core,
		Description: "Account, regions, EC2, VPC, IAM, and S3 read access.",
		Permissions: []string{
			"sts:GetCallerIdentity",
			"account:ListRegions",
			"ec2:DescribeInstances",
			"ec2:DescribeVpcs",
			"ec2:DescribeSecurityGroups",
			"iam:GetAccountSummary",
			"iam:ListRoles",
			"iam:ListUsers",
			"iam:ListPolicies",
			"s3:ListAllMyBuckets",
			"s3:GetBucketPolicyStatus",
		},
	},
	EKS: {
		Name:        EKS,
		Description: "EKS cluster read access.",
		Permissions: []string{
			"eks:ListClusters",
			"eks:DescribeCluster",
		},
	},
	Workload: {
		Name:        Workload,
		Description: "Container and serverless workload read access.",
		Permissions: []string{
			"ecs:ListClusters",
			"ecs:ListServices",
			"lambda:ListFunctions",
		},
	},
	DSPM: {
		Name:        DSPM,
		Description: "Data-store posture read access.",
		Permissions: []string{
			"s3:GetBucketTagging",
			"s3:GetBucketPublicAccessBlock",
			"rds:DescribeDBInstances",
		},
	},
}

// init populates each Definition's Probes from its Permissions so the verifier
// has a concrete read-only probe per required permission.
func init() {
	for name, def := range Catalog {
		probes := make([]Probe, 0, len(def.Permissions))
		for _, p := range def.Permissions {
			probes = append(probes, Probe{Permission: p})
		}
		def.Probes = probes
		Catalog[name] = def
	}
}

// DefaultSelection is the capability set enabled when a connection is created
// without an explicit selection.
func DefaultSelection() []string {
	return []string{string(Core)}
}

// Known reports whether name is a defined capability.
func Known(name string) bool {
	_, ok := Catalog[Capability(name)]
	return ok
}

// Lookup returns the Definition for a capability name, and whether it exists.
func Lookup(name string) (Definition, bool) {
	d, ok := Catalog[Capability(name)]
	return d, ok
}
