package normalize

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// networkSubtype values classify a Network entity. This vocabulary is
// OPEN-ENDED: adding a new value (e.g. for a GCP or Azure networking construct)
// is an additive change and MUST NOT require modifying model.KindNetwork or the
// model.AllKinds allow-list (R9.4). The nine AWS values are declared here, in
// one place.
const (
	subVPC               = "vpc"
	subSubnet            = "subnet"
	subRouteTable        = "route_table"
	subInternetGateway   = "internet_gateway"
	subNatGateway        = "nat_gateway"
	subPeeringConnection = "peering_connection"
	subNetworkInterface  = "network_interface"
	subTransitGateway    = "transit_gateway"
	subSecurityGroup     = "security_group"
)

// AWSNetwork maps Steampipe AWS networking tables to Network entities and the
// topology edges between them (R6). It is a distinct type from the Zeek-flow
// Network normalizer in network.go: AWSNetwork consumes provider=="aws"
// Steampipe events and emits model.KindNetwork nodes.
//
// Edges (deterministic topology, confidence 1.0):
//
//	CONTAINS     VPC -> Subnet
//	ATTACHED_TO  Subnet -> RouteTable (route-table associations)
//	ROUTES_TO    RouteTable -> igw/nat/pcx/tgw target
//	PEERS_WITH   requester VPC -> accepter VPC (one per active peering)
//	ATTACHED_TO  ENI -> Compute (only when attachment is "attached")
//	MEMBER_OF    Compute/ENI -> SecurityGroup
type AWSNetwork struct{}

// Sources implements Normalizer.
func (AWSNetwork) Sources() []string { return []string{"cloudquery"} }

// Normalize implements Normalizer.
func (AWSNetwork) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result

	for _, e := range events {
		if e.Provider != "aws" {
			continue
		}
		attrs := payloadMap(e)
		switch e.ResourceType {
		case "aws_vpc":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subVPC))
		case "aws_subnet":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subSubnet))
			// CONTAINS VPC -> Subnet.
			if vpc := stringAttr(attrs, "vpc_id"); vpc != "" {
				res.Edges = append(res.Edges, netEdge(e, model.RelContains, vpc, e.ResourceID))
			}
		case "aws_vpc_route_table":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subRouteTable))
			res.Edges = append(res.Edges, routeTableEdges(e, attrs)...)
		case "aws_vpc_internet_gateway":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subInternetGateway))
		case "aws_vpc_nat_gateway":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subNatGateway))
		case "aws_vpc_peering_connection":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subPeeringConnection))
			if edge, ok := peeringEdge(e, attrs); ok {
				res.Edges = append(res.Edges, edge)
			}
		case "aws_ec2_network_interface":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subNetworkInterface))
			res.Edges = append(res.Edges, eniEdges(e, attrs)...)
		case "aws_ec2_transit_gateway":
			res.Entities = append(res.Entities, networkEntity(e, attrs, subTransitGateway))
		case "aws_vpc_security_group":
			// The SG is owned here as a Network node (network_subtype
			// security_group) so MEMBER_OF edges can target it. aws.go keeps
			// only the internet NetworkEndpoint + EXPOSED_TO edge (see the SG
			// branch there). Emitted for every SG, not only open ones.
			res.Entities = append(res.Entities, networkEntity(e, attrs, subSecurityGroup))
			// Compute/ENI -> SG membership is emitted from the member side
			// (ec2 instance / ENI), not here.
		}
	}

	// Compute -> SG membership from EC2 instances (MEMBER_OF). The AWS
	// normalizer owns the Compute node; AWSNetwork owns the SG node and the
	// membership edge, keyed by the instance ARN/id and the SG group id.
	for _, e := range events {
		if e.Provider != "aws" || e.ResourceType != "aws_ec2_instance" {
			continue
		}
		attrs := payloadMap(e)
		for _, sg := range securityGroupIDs(attrs) {
			res.Edges = append(res.Edges, netEdge(e, model.RelMemberOf, e.ResourceID, sg))
		}
	}

	return res, nil
}

// networkEntity builds a KindNetwork entity with the given network_subtype.
func networkEntity(e rawevent.RawEvent, attrs map[string]any, subtype string) model.Entity {
	a := attrs
	if a == nil {
		a = map[string]any{}
	}
	a["network_subtype"] = subtype
	return model.Entity{
		Kind: model.KindNetwork, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "title"), stringAttr(attrs, "tags_name"), e.ResourceID),
		Attributes: a, Evidence: EvidenceFrom(e),
	}
}

// netEdge builds a deterministic topology edge (confidence 1.0).
func netEdge(e rawevent.RawEvent, rel model.Relation, src, dst string) model.Edge {
	return model.Edge{
		Relation: rel, SrcKey: src, DstKey: dst,
		Confidence: 1.0, Evidence: EvidenceFrom(e),
	}
}

// routeTableEdges emits ATTACHED_TO (Subnet -> RouteTable) for each associated
// subnet and ROUTES_TO (RouteTable -> target) for each route entry whose target
// is an igw-/nat-/pcx-/tgw- attachment.
func routeTableEdges(e rawevent.RawEvent, attrs map[string]any) []model.Edge {
	var out []model.Edge
	// Subnet associations: ATTACHED_TO Subnet -> RouteTable.
	for _, assoc := range toAnySlice(attrs["associations"]) {
		am, ok := assoc.(map[string]any)
		if !ok {
			continue
		}
		if subnet := firstNonEmpty(stringAttr(am, "SubnetId"), stringAttr(am, "subnet_id")); subnet != "" {
			out = append(out, netEdge(e, model.RelAttachedTo, subnet, e.ResourceID))
		}
	}
	// Routes: ROUTES_TO RouteTable -> gateway/nat/peering/transit target.
	for _, route := range toAnySlice(attrs["routes"]) {
		rm, ok := route.(map[string]any)
		if !ok {
			continue
		}
		target := routeTarget(rm)
		if target == "" {
			continue
		}
		out = append(out, netEdge(e, model.RelRoutesTo, e.ResourceID, target))
	}
	return out
}

// routeTarget picks the forwarding target of a route entry, preferring explicit
// typed targets and validating the id prefix so only igw/nat/pcx/tgw targets
// produce ROUTES_TO edges (local/blackhole routes are skipped).
func routeTarget(rm map[string]any) string {
	candidates := []string{
		firstNonEmpty(stringAttr(rm, "GatewayId"), stringAttr(rm, "gateway_id")),
		firstNonEmpty(stringAttr(rm, "NatGatewayId"), stringAttr(rm, "nat_gateway_id")),
		firstNonEmpty(stringAttr(rm, "VpcPeeringConnectionId"), stringAttr(rm, "vpc_peering_connection_id")),
		firstNonEmpty(stringAttr(rm, "TransitGatewayId"), stringAttr(rm, "transit_gateway_id")),
	}
	for _, c := range candidates {
		if isRouteTarget(c) {
			return c
		}
	}
	return ""
}

func isRouteTarget(id string) bool {
	for _, p := range []string{"igw-", "nat-", "pcx-", "tgw-"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// peeringEdge emits exactly one PEERS_WITH (requester VPC -> accepter VPC) for
// an active peering connection (status.code == "active").
func peeringEdge(e rawevent.RawEvent, attrs map[string]any) (model.Edge, bool) {
	if !peeringActive(attrs) {
		return model.Edge{}, false
	}
	req := vpcIDFrom(attrs, "requester_vpc_info", "RequesterVpcId", "requester_vpc_id")
	acc := vpcIDFrom(attrs, "accepter_vpc_info", "AccepterVpcId", "accepter_vpc_id")
	if req == "" || acc == "" {
		return model.Edge{}, false
	}
	return netEdge(e, model.RelPeersWith, req, acc), true
}

func peeringActive(attrs map[string]any) bool {
	st, ok := attrs["status"].(map[string]any)
	if !ok {
		// Some plugin versions flatten status_code.
		return strings.EqualFold(stringAttr(attrs, "status_code"), "active")
	}
	return strings.EqualFold(firstNonEmpty(stringAttr(st, "Code"), stringAttr(st, "code")), "active")
}

// vpcIDFrom reads a VPC id either from a nested info object (infoKey.VpcId) or a
// flattened column.
func vpcIDFrom(attrs map[string]any, infoKey, nestedKey, flatKey string) string {
	if info, ok := attrs[infoKey].(map[string]any); ok {
		if v := firstNonEmpty(stringAttr(info, "VpcId"), stringAttr(info, "vpc_id")); v != "" {
			return v
		}
	}
	return firstNonEmpty(stringAttr(attrs, nestedKey), stringAttr(attrs, flatKey))
}

// eniEdges emits ATTACHED_TO (ENI -> Compute) when the ENI's attachment status
// indicates it is attached, and MEMBER_OF (ENI -> SG) for each attached SG.
func eniEdges(e rawevent.RawEvent, attrs map[string]any) []model.Edge {
	var out []model.Edge
	if inst, ok := eniAttachedInstance(attrs); ok && inst != "" {
		out = append(out, netEdge(e, model.RelAttachedTo, e.ResourceID, inst))
	}
	for _, sg := range securityGroupIDs(attrs) {
		out = append(out, netEdge(e, model.RelMemberOf, e.ResourceID, sg))
	}
	return out
}

// eniAttachedInstance returns the attached instance id/ARN only when the ENI's
// attachment status is "attached" (excluding attaching/detaching/available).
func eniAttachedInstance(attrs map[string]any) (string, bool) {
	att, ok := attrs["attachment"].(map[string]any)
	if !ok {
		// Flattened columns fallback.
		status := strings.ToLower(stringAttr(attrs, "attachment_status"))
		if status != "attached" {
			return "", false
		}
		return firstNonEmpty(stringAttr(attrs, "attached_instance_id"), stringAttr(attrs, "instance_id")), true
	}
	status := strings.ToLower(firstNonEmpty(stringAttr(att, "Status"), stringAttr(att, "status")))
	if status != "attached" {
		return "", false
	}
	return firstNonEmpty(stringAttr(att, "InstanceId"), stringAttr(att, "instance_id")), true
}

// securityGroupIDs extracts SG group ids from a resource's security group list,
// tolerating the several Steampipe shapes (list of {GroupId}, list of ids, or
// flattened arrays).
func securityGroupIDs(attrs map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, key := range []string{"groups", "security_groups", "vpc_security_groups"} {
		for _, g := range toAnySlice(attrs[key]) {
			switch gv := g.(type) {
			case map[string]any:
				add(firstNonEmpty(stringAttr(gv, "GroupId"), stringAttr(gv, "group_id"),
					stringAttr(gv, "VpcSecurityGroupId"), stringAttr(gv, "vpc_security_group_id")))
			case string:
				add(gv)
			}
		}
	}
	for _, key := range []string{"security_group_ids", "group_ids"} {
		for _, id := range toStringSlice(attrs[key]) {
			add(id)
		}
	}
	return out
}

// toAnySlice coerces a value to []any (nil when it is not a slice).
func toAnySlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
