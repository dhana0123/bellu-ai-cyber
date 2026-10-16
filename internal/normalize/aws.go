package normalize

import (
	"encoding/json"
	"fmt"
	"strings"

	iam "github.com/bellu/bellu-ai-cyber/internal/resolve/iam/aws"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// AWS maps Steampipe AWS RawEvents to canonical entities and edges.
//
// Kinds:  CloudAccount, Identity, DataStore (S3), Compute (EC2/Lambda),
//
//	Cluster (EKS), Workload (ECS task def), ContainerImage (ECR digest),
//	AIService (Bedrock/SageMaker).
//
// Edges:  CONTAINS (account -> resources), RUNS_IMAGE (ECS/Lambda -> image),
//
//	USES (Lambda/ECS/EC2 -> role), EXPOSED_TO (SG open to 0.0.0.0/0),
//	CAN_READ/CAN_WRITE/CAN_ADMIN (role -> S3, via IAM evaluator).
type AWS struct{}

// Sources implements Normalizer.
func (AWS) Sources() []string { return []string{"steampipe"} }

// Normalize implements Normalizer. It only processes aws_* resource types so a
// shared Steampipe source can carry multiple providers.
func (AWS) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result

	// Collect S3 buckets so IAM edges can target them.
	var buckets []model.Entity
	// Collect identities with their policies for the second pass.
	type idPolicy struct {
		roleKey  string
		policies []json.RawMessage
		ev       rawevent.RawEvent
	}
	var identities []idPolicy

	// Track every account id seen (from resource ARNs) and whether a real
	// CloudAccount node was emitted for it, so we can synthesize any missing
	// account node at the end — guaranteeing every resource's CONTAINS edge has
	// an account to resolve to.
	accountSeen := map[string]bool{} // account id -> seen on any resource
	accountNode := map[string]bool{} // account id -> a CloudAccount node exists
	var firstAcctEvidence *rawevent.RawEvent

	for _, e := range events {
		if e.Provider != "aws" {
			continue
		}
		attrs := payloadMap(e)
		if acct := childAccountID(e, attrs); acct != "" {
			accountSeen[acct] = true
			if firstAcctEvidence == nil {
				ev := e
				firstAcctEvidence = &ev
			}
		}
		switch e.ResourceType {
		case "aws_account":
			ent := awsAccount(e, attrs)
			res.Entities = append(res.Entities, ent)
			if id := firstNonEmpty(stringAttr(attrs, "account_id"), accountIDFromARN(stringAttr(attrs, "arn"))); id != "" {
				accountNode[id] = true
			}
		case "aws_iam_role", "aws_iam_user":
			ent := awsIdentity(e, attrs)
			res.Entities = append(res.Entities, ent)
			// account CONTAINS identity
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
			identities = append(identities, idPolicy{
				roleKey:  ent.Key,
				policies: extractPolicies(attrs),
				ev:       e,
			})
		case "aws_s3_bucket":
			ent := awsDataStore(e, attrs)
			res.Entities = append(res.Entities, ent)
			buckets = append(buckets, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "aws_ec2_instance":
			ent := awsCompute(e, attrs, "ec2")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
			if role := instanceProfileRole(attrs); role != "" {
				res.Edges = append(res.Edges, usesEdge(e, ent.Key, role))
			}
		case "aws_lambda_function":
			ent := awsCompute(e, attrs, "lambda")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
			if role := stringAttr(attrs, "role"); role != "" {
				res.Edges = append(res.Edges, usesEdge(e, ent.Key, role))
			}
		case "aws_eks_cluster":
			ent := awsCluster(e, attrs)
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "aws_ecs_task_definition":
			ent := awsWorkload(e, attrs)
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
			if role := stringAttr(attrs, "task_role_arn"); role != "" {
				res.Edges = append(res.Edges, usesEdge(e, ent.Key, role))
			}
			for _, dig := range taskImageDigests(attrs) {
				res.Edges = append(res.Edges, runsImageEdge(e, ent.Key, dig))
			}
		case "aws_ecr_image":
			if dig := stringAttr(attrs, "image_digest"); dig != "" {
				res.Entities = append(res.Entities, awsImage(e, attrs, dig))
			}
		case "aws_vpc_security_group":
			if sgOpenToWorld(attrs) {
				// Represent the SG and the internet as NetworkEndpoints so the
				// EXPOSED_TO edge resolves and is queryable in V1.
				sg := model.Entity{
					Kind: model.KindNetworkEndpoint, Key: e.ResourceID, Provider: "aws",
					Name: e.ResourceID, Attributes: attrs, Evidence: EvidenceFrom(e),
				}
				internet := model.Entity{
					Kind: model.KindNetworkEndpoint, Key: "0.0.0.0/0", Provider: "network",
					Name: "internet (0.0.0.0/0)", Attributes: map[string]any{"cidr": "0.0.0.0/0"},
					Evidence: EvidenceFrom(e),
				}
				res.Entities = append(res.Entities, sg, internet)
				res.Edges = append(res.Edges, exposedToEdges(e, attrs)...)
			}
		case "aws_bedrock_custom_model", "aws_sagemaker_model":
			ent := awsAIService(e, attrs)
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		}
	}

	// Second pass: IAM permission edges (identity -> S3), approximate.
	for _, ip := range identities {
		for _, b := range buckets {
			bucketARN := b.Key
			var best iam.Access
			var bestActions []string
			for _, pol := range ip.policies {
				doc, err := iam.ParsePolicy(pol)
				if err != nil {
					continue
				}
				acc, actions := iam.EvaluateS3(doc, bucketARN)
				best = maxAccess(best, acc)
				if len(actions) > len(bestActions) {
					bestActions = actions
				}
			}
			if best == iam.AccessNone {
				continue
			}
			res.Edges = append(res.Edges, accessEdge(ip.ev, ip.roleKey, bucketARN, best, bestActions))
		}
	}

	// Determine the run's dominant account id (every discovery runs against one
	// account). Resources whose ARN has no account segment — notably S3 buckets
	// (arn:aws:s3:::bucket) — can't self-identify, so their CONTAINS edge was
	// left with an empty source. Repair those from the dominant account so every
	// resource links to its account.
	defaultAcct := ""
	for acct := range accountSeen {
		if defaultAcct == "" || acct < defaultAcct {
			defaultAcct = acct
		}
	}
	if defaultAcct != "" {
		accountSeen[defaultAcct] = true
		for i := range res.Edges {
			if res.Edges[i].Relation == model.RelContains && res.Edges[i].SrcKey == "" {
				res.Edges[i].SrcKey = accountKey(defaultAcct)
			}
		}
	}

	// Synthesize a CloudAccount node for any account id seen on resources but
	// without its own aws_account row (or whose row had no usable id). Without
	// this, the CONTAINS edges from that account would be dropped by the
	// resolver and the graph would show resources with no account connection.
	for acct := range accountSeen {
		if accountNode[acct] {
			continue
		}
		ent := model.Entity{
			Kind: model.KindCloudAccount, Key: accountKey(acct), Provider: "aws",
			Name: acct, Attributes: map[string]any{"account_id": acct, "synthesized": true},
		}
		if firstAcctEvidence != nil {
			ent.Evidence = EvidenceFrom(*firstAcctEvidence)
		}
		res.Entities = append(res.Entities, ent)
		accountNode[acct] = true
	}

	return res, nil
}

// ---- entity builders ----

func awsAccount(e rawevent.RawEvent, attrs map[string]any) model.Entity {
	// Key the account by its stable 12-digit id so every resource can link to
	// it (resources carry the account id in their ARN). Fall back to a derived
	// id or the raw resource id when the account id isn't directly present.
	id := firstNonEmpty(
		stringAttr(attrs, "account_id"),
		accountIDFromARN(stringAttr(attrs, "arn")),
		accountIDFromARN(e.ResourceID),
	)
	key := e.ResourceID
	name := firstNonEmpty(stringAttr(attrs, "title"), e.ResourceID)
	if id != "" {
		key = accountKey(id)
		name = id
	}
	return model.Entity{
		Kind: model.KindCloudAccount, Key: key, Provider: "aws",
		Name: name, Attributes: attrs, Evidence: EvidenceFrom(e),
	}
}

// accountKey is the stable canonical key for an AWS account node, derived from
// the 12-digit account id so every resource's CONTAINS edge resolves to it.
func accountKey(accountID string) string {
	return "aws-account:" + accountID
}

// accountIDFromARN extracts the 12-digit account id from an AWS ARN
// ("arn:aws:<service>:<region>:<account-id>:<resource>"). Returns "" when the
// input is not a parseable ARN with an account segment.
func accountIDFromARN(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return ""
	}
	acct := parts[4]
	if len(acct) == 12 && isAllDigits(acct) {
		return acct
	}
	return ""
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// childAccountID derives the account id that owns a resource event, trying the
// account_id attr first, then the resource ARN, then any arn attr.
func childAccountID(e rawevent.RawEvent, attrs map[string]any) string {
	return firstNonEmpty(
		stringAttr(attrs, "account_id"),
		accountIDFromARN(e.ResourceID),
		accountIDFromARN(stringAttr(attrs, "arn")),
	)
}

func awsIdentity(e rawevent.RawEvent, attrs map[string]any) model.Entity {
	return model.Entity{
		Kind: model.KindIdentity, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "name"), stringAttr(attrs, "title"), e.ResourceID),
		Attributes: attrs, Evidence: EvidenceFrom(e),
	}
}

func awsDataStore(e rawevent.RawEvent, attrs map[string]any) model.Entity {
	return model.Entity{
		Kind: model.KindDataStore, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "name"), e.ResourceID),
		Attributes: attrs, Evidence: EvidenceFrom(e),
	}
}

func awsCompute(e rawevent.RawEvent, attrs map[string]any, subtype string) model.Entity {
	a := attrs
	a["compute_subtype"] = subtype
	return model.Entity{
		Kind: model.KindCompute, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "name"), stringAttr(attrs, "title"), e.ResourceID),
		Attributes: a, Evidence: EvidenceFrom(e),
	}
}

func awsCluster(e rawevent.RawEvent, attrs map[string]any) model.Entity {
	return model.Entity{
		Kind: model.KindCluster, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "name"), e.ResourceID),
		Attributes: attrs, Evidence: EvidenceFrom(e),
	}
}

func awsWorkload(e rawevent.RawEvent, attrs map[string]any) model.Entity {
	return model.Entity{
		Kind: model.KindWorkload, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "family"), e.ResourceID),
		Attributes: attrs, Evidence: EvidenceFrom(e),
	}
}

func awsImage(e rawevent.RawEvent, attrs map[string]any, digest string) model.Entity {
	return model.Entity{
		Kind: model.KindContainerImage, Key: digest, Provider: "image",
		Name:       firstNonEmpty(stringAttr(attrs, "repository_name"), digest),
		Attributes: attrs, Evidence: EvidenceFrom(e),
	}
}

func awsAIService(e rawevent.RawEvent, attrs map[string]any) model.Entity {
	return model.Entity{
		Kind: model.KindAIService, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "name"), stringAttr(attrs, "model_name"), e.ResourceID),
		Attributes: attrs, Evidence: EvidenceFrom(e),
	}
}

// ---- edge builders ----

// containsEdge links the owning account to a child resource. The account is
// derived from the resource's ARN (every AWS ARN embeds the account id), so the
// edge resolves even when the aws_account row carried no usable id and
// regardless of whether the collector populated the scope column.
func containsEdge(e rawevent.RawEvent, childKey string) model.Edge {
	// Source the edge from the owning account, derived from the resource ARN.
	// When the ARN has no account segment (e.g. S3 bucket ARNs), leave the
	// source empty; a post-pass in Normalize fills it from the run's dominant
	// account so the edge still resolves.
	src := ""
	if acct := childAccountID(e, payloadMap(e)); acct != "" {
		src = accountKey(acct)
	}
	return model.Edge{
		Relation: model.RelContains, SrcKey: src, DstKey: childKey,
		Confidence: 1.0, Evidence: EvidenceFrom(e),
	}
}

func usesEdge(e rawevent.RawEvent, computeKey, roleKey string) model.Edge {
	return model.Edge{
		Relation: model.RelUses, SrcKey: computeKey, DstKey: roleKey,
		Confidence: 1.0, Evidence: EvidenceFrom(e),
	}
}

func runsImageEdge(e rawevent.RawEvent, workloadKey, digest string) model.Edge {
	return model.Edge{
		Relation: model.RelRunsImage, SrcKey: workloadKey, DstKey: digest,
		Confidence: 1.0, Evidence: EvidenceFrom(e),
	}
}

func accessEdge(e rawevent.RawEvent, roleKey, bucketARN string, acc iam.Access, actions []string) model.Edge {
	rel := model.RelCanRead
	switch acc {
	case iam.AccessWrite:
		rel = model.RelCanWrite
	case iam.AccessAdmin:
		rel = model.RelCanAdmin
	}
	return model.Edge{
		Relation: rel, SrcKey: roleKey, DstKey: bucketARN,
		Attributes: map[string]any{
			"actions":   actions,
			"effective": "approx", // ignores SCPs, boundaries, conditions
		},
		Confidence: 0.7, // approximate evaluation
		Evidence:   EvidenceFrom(e),
	}
}

// exposedToEdges emits EXPOSED_TO for security groups with ingress open to
// 0.0.0.0/0.
func exposedToEdges(e rawevent.RawEvent, attrs map[string]any) []model.Edge {
	// The SG itself isn't a node in V1; we attach EXPOSED_TO from the SG id to
	// a synthetic internet endpoint so the exposure is queryable. We model the
	// SG as the source key and 0.0.0.0/0 as a NetworkEndpoint-style key.
	if !sgOpenToWorld(attrs) {
		return nil
	}
	return []model.Edge{{
		Relation:   model.RelExposedTo,
		SrcKey:     e.ResourceID, // security group id
		DstKey:     "0.0.0.0/0",
		Attributes: map[string]any{"source": "security_group_ingress"},
		Confidence: 1.0,
		Evidence:   EvidenceFrom(e),
	}}
}

// ---- extraction helpers ----

func extractPolicies(attrs map[string]any) []json.RawMessage {
	var out []json.RawMessage
	// Inline policies: map of name -> document (aws_iam_role.inline_policies_std).
	for _, key := range []string{"inline_policies_std", "inline_policies"} {
		if v, ok := attrs[key]; ok {
			out = append(out, policyDocsFrom(v)...)
		}
	}
	// Attached policy documents, when Steampipe hydrates them.
	for _, key := range []string{"attached_policy_documents", "assume_role_policy_std"} {
		if v, ok := attrs[key]; ok {
			out = append(out, policyDocsFrom(v)...)
		}
	}
	return out
}

// policyDocsFrom coerces various shapes (object, array, map of name->doc) into
// policy document JSON blobs.
func policyDocsFrom(v any) []json.RawMessage {
	switch t := v.(type) {
	case map[string]any:
		// Could be a single policy doc (has Statement) or a name->doc map.
		if _, hasStmt := t["Statement"]; hasStmt {
			b, _ := json.Marshal(t)
			return []json.RawMessage{b}
		}
		var out []json.RawMessage
		for _, doc := range t {
			b, _ := json.Marshal(doc)
			out = append(out, b)
		}
		return out
	case []any:
		var out []json.RawMessage
		for _, doc := range t {
			b, _ := json.Marshal(doc)
			out = append(out, b)
		}
		return out
	default:
		return nil
	}
}

func instanceProfileRole(attrs map[string]any) string {
	if iam, ok := attrs["iam_instance_profile_arn"].(string); ok && iam != "" {
		// Instance profile ARN -> role name is approximate; return profile ARN
		// so the edge still links. In practice the role share the name.
		return strings.Replace(iam, ":instance-profile/", ":role/", 1)
	}
	return ""
}

func taskImageDigests(attrs map[string]any) []string {
	var out []string
	defs, ok := attrs["container_definitions"].([]any)
	if !ok {
		return nil
	}
	for _, d := range defs {
		cd, ok := d.(map[string]any)
		if !ok {
			continue
		}
		img, _ := cd["image"].(string)
		if i := strings.Index(img, "@sha256:"); i >= 0 {
			out = append(out, img[i+1:])
		}
	}
	return out
}

func sgOpenToWorld(attrs map[string]any) bool {
	perms, ok := attrs["ip_permissions"].([]any)
	if !ok {
		return false
	}
	for _, p := range perms {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		ranges, ok := pm["IpRanges"].([]any)
		if !ok {
			continue
		}
		for _, r := range ranges {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if cidr, _ := rm["CidrIp"].(string); cidr == "0.0.0.0/0" {
				return true
			}
		}
	}
	return false
}

func maxAccess(a, b iam.Access) iam.Access {
	rank := map[iam.Access]int{iam.AccessNone: 0, iam.AccessRead: 1, iam.AccessWrite: 2, iam.AccessAdmin: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var _ = fmt.Sprintf
