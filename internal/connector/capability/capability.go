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
	Name Capability
	// Title is the human-readable card title shown in onboarding (e.g.
	// "Core Security"). The UI shows capability cards, never raw IAM actions.
	Title string
	// Description is the human-readable card description.
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

// Card is the UI-facing, non-secret view of a capability for onboarding. It
// carries only a title and description so the wizard shows capability cards,
// never raw IAM actions.
type Card struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

// Catalog is the authoritative set of capabilities and their permission bundles.
var Catalog = map[Capability]Definition{
	Core: {
		Name:        Core,
		Title:       "Core Security",
		Description: "Account, EC2, VPC, IAM, and S3 visibility.",
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
		Title:       "EKS Scanning",
		Description: "Kubernetes / EKS cluster visibility.",
		Permissions: []string{
			"eks:ListClusters",
			"eks:DescribeCluster",
		},
	},
	Workload: {
		Name:        Workload,
		Title:       "Workload Scanning",
		Description: "ECS / Lambda workload visibility.",
		Permissions: []string{
			"ecs:ListClusters",
			"ecs:ListServices",
			"lambda:ListFunctions",
		},
	},
	DSPM: {
		Name:        DSPM,
		Title:       "DSPM",
		Description: "Scan supported data stores for sensitive data.",
		Permissions: []string{
			"s3:GetBucketTagging",
			"s3:GetBucketPublicAccessBlock",
			"rds:DescribeDBInstances",
		},
	},
}

// cardOrder is the stable display order for capability cards.
var cardOrder = []Capability{Core, DSPM, Workload, EKS}

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

// Cards returns the UI-facing capability catalog in stable display order, with
// core marked as the default selection. It exposes no IAM actions.
func Cards() []Card {
	out := make([]Card, 0, len(cardOrder))
	for _, name := range cardOrder {
		def := Catalog[name]
		out = append(out, Card{
			Name:        string(name),
			Title:       def.Title,
			Description: def.Description,
			Default:     name == Core,
		})
	}
	return out
}
