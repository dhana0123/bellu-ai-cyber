package normalize

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// GCP maps Steampipe GCP RawEvents to the canonical kinds, same as AWS/Azure.
//
// Data access comes from IAM policy bindings: a member (serviceAccount) granted
// a storage role (e.g. roles/storage.objectViewer) at a project or bucket scope
// yields CAN_READ. V1 project-level bindings grant access to every bucket in
// the project (approximate).
type GCP struct{}

// Sources implements Normalizer.
func (GCP) Sources() []string { return []string{"cloudquery"} }

// Normalize implements Normalizer, filtering to gcp provider events.
func (GCP) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result

	// Buckets by key, and the project each belongs to.
	type bucket struct{ key, project string }
	var buckets []bucket
	// service account email set for member resolution.
	saKeys := map[string]bool{}

	type binding struct {
		member string
		role   string
		scope  string // project id
		ev     rawevent.RawEvent
	}
	var bindings []binding

	for _, e := range events {
		if e.Provider != "gcp" {
			continue
		}
		attrs := payloadMap(e)
		switch e.ResourceType {
		case "gcp_project":
			res.Entities = append(res.Entities, gcpEntity(e, attrs, model.KindCloudAccount, "name"))
		case "gcp_service_account":
			ent := gcpEntity(e, attrs, model.KindIdentity, "display_name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
			saKeys[ent.Key] = true
		case "gcp_storage_bucket":
			ent := gcpEntity(e, attrs, model.KindDataStore, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
			buckets = append(buckets, bucket{key: ent.Key, project: e.Scope})
		case "gcp_compute_instance":
			ent := gcpEntity(e, attrs, model.KindCompute, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "gcp_kubernetes_cluster":
			ent := gcpEntity(e, attrs, model.KindCluster, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "gcp_cloud_run_service":
			ent := gcpEntity(e, attrs, model.KindWorkload, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "gcp_vertex_ai_model":
			ent := gcpEntity(e, attrs, model.KindAIService, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "gcp_iam_binding":
			for _, b := range extractGCPBindings(attrs) {
				b.scope = e.Scope
				b.ev = e
				bindings = append(bindings, binding(b))
			}
		}
	}

	// Project-level bindings grant access to all buckets in that project.
	for _, b := range bindings {
		acc := classifyGCPRole(b.role)
		if acc == "" {
			continue
		}
		member := normalizeGCPMember(b.member)
		if member == "" {
			continue
		}
		for _, bk := range buckets {
			if bk.project != b.scope {
				continue
			}
			res.Edges = append(res.Edges, cloudAccessEdge(b.ev, member, bk.key, acc, b.role))
		}
	}

	return res, nil
}

func gcpEntity(e rawevent.RawEvent, attrs map[string]any, kind model.Kind, nameKey string) model.Entity {
	return model.Entity{
		Kind:       kind,
		Key:        e.ResourceID,
		Provider:   "gcp",
		Name:       firstNonEmpty(stringAttr(attrs, nameKey), stringAttr(attrs, "name"), e.ResourceID),
		Attributes: attrs,
		Evidence:   EvidenceFrom(e),
	}
}

// gcpBinding is a flattened IAM binding.
type gcpBinding struct {
	member string
	role   string
	scope  string
	ev     rawevent.RawEvent
}

// extractGCPBindings flattens an IAM policy's bindings[] into member/role pairs.
func extractGCPBindings(attrs map[string]any) []gcpBinding {
	var out []gcpBinding
	// Steampipe gcp_project_iam_policy exposes "bindings" as a list of
	// {role, members[]}.
	raw, ok := attrs["bindings"].([]any)
	if !ok {
		// Some versions nest under iam_policy.bindings.
		if pol, ok := attrs["iam_policy"].(map[string]any); ok {
			raw, _ = pol["bindings"].([]any)
		}
	}
	for _, b := range raw {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		role, _ := bm["role"].(string)
		members, _ := bm["members"].([]any)
		for _, m := range members {
			ms, _ := m.(string)
			out = append(out, gcpBinding{member: ms, role: role})
		}
	}
	return out
}

// normalizeGCPMember turns "serviceAccount:foo@bar.iam.gserviceaccount.com"
// into the bare email used as the identity key.
func normalizeGCPMember(member string) string {
	if i := strings.Index(member, ":"); i >= 0 {
		prefix := member[:i]
		if prefix == "serviceAccount" || prefix == "user" {
			return member[i+1:]
		}
	}
	return ""
}

// classifyGCPRole maps a GCP storage IAM role to an access level.
func classifyGCPRole(role string) accessLevel {
	r := strings.ToLower(role)
	switch {
	case r == "roles/owner", r == "roles/editor", r == "roles/storage.admin":
		return accessAdmin
	case strings.Contains(r, "storage.objectadmin"), strings.Contains(r, "storage.objectcreator"),
		strings.Contains(r, "objectuser"):
		return accessWrite
	case strings.Contains(r, "storage.objectviewer"), strings.Contains(r, "viewer"),
		strings.Contains(r, "legacyobjectreader"):
		return accessRead
	}
	return ""
}
