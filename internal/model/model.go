// Package model defines the canonical graph model: node kinds, relation types,
// and the Entity, Edge and Evidence value types that normalizers produce and
// the graph writer consumes.
//
// The model is provider-independent. Attribute vocabularies (CycloneDX, OCSF)
// live inside the Attributes maps, never as node labels.
package model

import "time"

// Kind is a canonical node kind. Every node also carries the shared :Entity
// label in the graph.
type Kind string

const (
	KindCloudAccount    Kind = "CloudAccount"
	KindIdentity        Kind = "Identity"
	KindDataStore       Kind = "DataStore"
	KindCompute         Kind = "Compute"
	KindCluster         Kind = "Cluster"
	KindWorkload        Kind = "Workload"
	KindContainerImage  Kind = "ContainerImage"
	KindRepository      Kind = "Repository"
	KindPackage         Kind = "Package"
	KindVulnerability   Kind = "Vulnerability"
	KindAIAgent         Kind = "AIAgent"
	KindAIModel         Kind = "AIModel"
	KindPrompt          Kind = "Prompt"
	KindMCPServer       Kind = "MCPServer"
	KindAIService       Kind = "AIService"
	KindNetworkEndpoint Kind = "NetworkEndpoint"
	KindExternalService Kind = "ExternalService"
	// KindNetwork is a cloud networking construct (VPC, subnet, route table,
	// gateway, peering connection, network interface, security group, ...),
	// distinguished by a provider-neutral network_subtype attribute. The
	// subtype vocabulary is open-ended so GCP/Azure constructs are additive.
	KindNetwork Kind = "Network"
	// KindEntryPoint is a resource that accepts traffic from outside the
	// account's network boundary (e.g. an API Gateway REST or HTTP API).
	KindEntryPoint Kind = "EntryPoint"
)

// AllKinds is the vetted allow-list of node labels. The graph writer only ever
// applies labels from this set, never raw input.
var AllKinds = []Kind{
	KindCloudAccount, KindIdentity, KindDataStore, KindCompute, KindCluster,
	KindWorkload, KindContainerImage, KindRepository, KindPackage,
	KindVulnerability, KindAIAgent, KindAIModel, KindPrompt, KindMCPServer,
	KindAIService, KindNetworkEndpoint, KindExternalService,
	KindNetwork, KindEntryPoint,
}

// Relation is a canonical edge type.
type Relation string

const (
	RelContains        Relation = "CONTAINS"
	RelRunsIn          Relation = "RUNS_IN"
	RelRunsImage       Relation = "RUNS_IMAGE"
	RelBuiltFrom       Relation = "BUILT_FROM"
	RelDependsOn       Relation = "DEPENDS_ON"
	RelHasVuln         Relation = "HAS_VULN"
	RelUses            Relation = "USES"
	RelAuthenticatesAs Relation = "AUTHENTICATES_AS"
	RelCanRead         Relation = "CAN_READ"
	RelCanWrite        Relation = "CAN_WRITE"
	RelCanAdmin        Relation = "CAN_ADMIN"
	RelCalls           Relation = "CALLS"
	RelUsesModel       Relation = "USES_MODEL"
	RelConnectsTo      Relation = "CONNECTS_TO"
	RelResolvesTo      Relation = "RESOLVES_TO"
	RelExposedTo       Relation = "EXPOSED_TO"
	// RelCanAssume: one identity may assume another (e.g. sts:AssumeRole).
	// Provider-neutral: GCP SA impersonation / Azure managed-identity trust
	// emit the same relation.
	RelCanAssume Relation = "CAN_ASSUME"
	// RelRoutesTo: a route table entry directs traffic to a target gateway or
	// peering/transit attachment.
	RelRoutesTo Relation = "ROUTES_TO"
	// RelPeersWith: two VPCs are connected by an active peering connection.
	RelPeersWith Relation = "PEERS_WITH"
	// RelMemberOf: a resource or network interface is a member of a security
	// group.
	RelMemberOf Relation = "MEMBER_OF"
	// RelAttachedTo: a network interface is attached to a resource, or a subnet
	// is associated with a route table.
	RelAttachedTo Relation = "ATTACHED_TO"
)

// AllRelations is the vetted allow-list of relationship types.
var AllRelations = []Relation{
	RelContains, RelRunsIn, RelRunsImage, RelBuiltFrom, RelDependsOn,
	RelHasVuln, RelUses, RelAuthenticatesAs, RelCanRead, RelCanWrite,
	RelCanAdmin, RelCalls, RelUsesModel, RelConnectsTo, RelResolvesTo,
	RelExposedTo, RelCanAssume, RelRoutesTo, RelPeersWith, RelMemberOf,
	RelAttachedTo,
}

var (
	kindSet     = toSet(AllKinds)
	relationSet = toSet(AllRelations)
)

// ValidKind reports whether k is in the allow-list.
func ValidKind(k Kind) bool { _, ok := kindSet[k]; return ok }

// ValidRelation reports whether r is in the allow-list.
func ValidRelation(r Relation) bool { _, ok := relationSet[r]; return ok }

// Evidence links a graph element back to the raw event that justifies it.
type Evidence struct {
	// RawEventID references raw_events.id in Postgres.
	RawEventID string
	Source     string
	Collector  string
	ObservedAt time.Time
	RunID      string
}

// Entity is a canonical node emitted by a normalizer. CanonicalID is assigned
// by the resolver; normalizers set a provisional ID from a strong key when one
// exists and leave resolution to assign the final value.
type Entity struct {
	// Kind is the node label.
	Kind Kind
	// Key is the strong native identifier the normalizer observed (ARN, purl,
	// digest, ...). The resolver turns this into a CanonicalID.
	Key string
	// CanonicalID is the resolved graph identity (kind:provider:native-id).
	// Set by the resolver.
	CanonicalID string
	// Provider is aws | azure | gcp | k8s | image | repo | network.
	Provider string
	// Name is a human-readable label.
	Name string
	// Attributes carries CycloneDX/OCSF-vocabulary fields.
	Attributes map[string]any
	// Evidence is the raw events backing this entity.
	Evidence []Evidence
}

// Edge is a canonical relationship emitted by a normalizer. SrcKey and DstKey
// are strong native keys (same space as Entity.Key); the resolver maps them to
// canonical IDs. Every edge must carry at least one Evidence.
type Edge struct {
	Relation   Relation
	SrcKey     string
	DstKey     string
	SrcCanonicalID string
	DstCanonicalID string
	Attributes map[string]any
	// Confidence in [0,1]; strong deterministic edges are 1.0.
	Confidence float64
	Evidence   []Evidence
}

func toSet[T comparable](items []T) map[T]struct{} {
	m := make(map[T]struct{}, len(items))
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
}
