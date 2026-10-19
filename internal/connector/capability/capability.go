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
	AI       Capability = "ai"
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
	// Permissions below are the IAM actions the discovery collector's List/Get
	// calls require for the tables Bellu queries (see the CloudQuery mapping
	// internal/source/cloudquery/mapping/aws.yaml). Each table's required actions
	// come from the plugin's per-table `Tags{service,action}` metadata, so this
	// set is exhaustive for discovery — no trial-and-error AccessDenied.
	Core: {
		Name:        Core,
		Title:       "Core Security",
		Description: "Account, EC2, VPC, IAM, and S3 visibility.",
		Permissions: []string{
			"sts:GetCallerIdentity",
			"account:ListRegions",
			"account:GetContactInformation",
			// aws_ec2_instance: DescribeInstances/InstanceAttribute/InstanceStatus
			// + GetLaunchTemplateData; aws_vpc_security_group: DescribeSecurityGroups.
			"ec2:Describe*",
			"ec2:GetLaunchTemplateData",
			// aws_iam_role/user/policy: List*/Get* across roles, users, policies,
			// attached + inline policies, instance profiles, MFA, login profiles.
			"iam:Get*",
			"iam:List*",
			"iam:GenerateCredentialReport",
			"iam:GenerateServiceLastAccessedDetails",
			// aws_s3_bucket: ListAllMyBuckets + a wide set of per-bucket Get*
			// (acl, policy, policyStatus, versioning, encryption, lifecycle,
			// replication, logging, website, notification, ownership, objectLock).
			"s3:List*",
			"s3:Get*",
			// aws_account org details.
			"organizations:DescribeOrganization",
			// Supporting posture reads (KMS/CloudTrail/Config/tags).
			"kms:DescribeKey",
			"kms:ListKeys",
			"kms:ListAliases",
			"cloudtrail:DescribeTrails",
			"cloudtrail:GetTrailStatus",
			"config:Describe*",
			"tag:GetResources",
		},
	},
	EKS: {
		Name:        EKS,
		Title:       "EKS Scanning",
		Description: "Kubernetes / EKS cluster visibility.",
		Permissions: []string{
			// aws_eks_cluster: ListClusters + DescribeCluster.
			"eks:List*",
			"eks:Describe*",
		},
	},
	Workload: {
		Name:        Workload,
		Title:       "Workload Scanning",
		Description: "ECS / Lambda / ECR workload visibility.",
		Permissions: []string{
			// aws_ecs_task_definition: ListTaskDefinitions + DescribeTaskDefinition.
			"ecs:List*",
			"ecs:Describe*",
			// aws_lambda_function: ListFunctions, GetFunction, GetPolicy,
			// GetFunctionUrlConfig.
			"lambda:List*",
			"lambda:GetFunction*",
			"lambda:GetPolicy",
			// aws_ecr_image: parent DescribeRepositories + DescribeImages.
			"ecr:Describe*",
			"ecr:List*",
			"ecr:GetRepositoryPolicy",
			// aws_api_gateway_rest_api / aws_api_gatewayv2_api: read via GET.
			"apigateway:GET",
			// aws_inspector2_finding: Inspector v2 CVE findings for EC2, ECR
			// images and Lambda (the cloud-native vulnerability source).
			"inspector2:ListFindings",
		},
	},
	DSPM: {
		Name:        DSPM,
		Title:       "DSPM",
		Description: "Scan supported data stores for sensitive data.",
		Permissions: []string{
			"s3:GetBucketTagging",
			"s3:GetBucketPublicAccessBlock",
			"rds:Describe*",
			"rds:ListTagsForResource",
			"dynamodb:ListTables",
			"dynamodb:DescribeTable",
			// aws_efs_file_system: DescribeFileSystems (+ policy/mount targets).
			"elasticfilesystem:Describe*",
			// aws_elasticache_cluster: DescribeCacheClusters (+ replication groups).
			"elasticache:Describe*",
		},
	},
	AI: {
		Name:        AI,
		Title:       "AI Services",
		Description: "Amazon Bedrock & SageMaker model/endpoint visibility.",
		Permissions: []string{
			// aws_bedrock_custom_model: ListCustomModels + GetCustomModel (List*/Get*
			// also covers foundation models, agents, guardrails read-only).
			"bedrock:List*",
			"bedrock:Get*",
			// aws_sagemaker_model: ListModels, DescribeModel, ListTags.
			"sagemaker:List*",
			"sagemaker:Describe*",
		},
	},
}

// cardOrder is the stable display order for capability cards.
var cardOrder = []Capability{Core, AI, DSPM, Workload, EKS}

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
// without an explicit selection. Core plus the read-only compute-visibility
// capabilities (EKS, Workload) are enabled by default so discovery can query
// EKS/ECS/Lambda without an AccessDenied; DSPM stays opt-in because it reaches
// into data stores. All remain least-privilege, read-only bundles.
func DefaultSelection() []string {
	return []string{string(Core), string(EKS), string(Workload), string(AI)}
}

// AllCapabilities returns every defined capability name in stable catalog order
// (core first). Used to grant the role the full read-only permission union at
// connect time, so capability selection is a scan-time gate, not a role change.
func AllCapabilities() []string {
	out := make([]string, 0, len(cardOrder))
	for _, name := range cardOrder {
		out = append(out, string(name))
	}
	return out
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
