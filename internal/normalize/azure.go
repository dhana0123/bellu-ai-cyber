package normalize

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// Azure maps Steampipe Azure RawEvents to the same canonical kinds as AWS,
// proving the model is cloud-independent. Only attribute values differ by
// provider; node labels never do.
//
// Data access comes from role assignments: a principal (Identity) assigned a
// data role (e.g. Storage Blob Data Reader) at a storage account scope yields
// CAN_READ. Role classification lives in a shared table.
type Azure struct{}

// Sources implements Normalizer.
func (Azure) Sources() []string { return []string{"steampipe"} }

// Normalize implements Normalizer, filtering to azure provider events.
func (Azure) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result

	// Index storage accounts by lowercased id so role-assignment scopes match.
	stores := map[string]bool{}

	type assignment struct {
		principalID string
		roleDefID   string
		scope       string
		ev          rawevent.RawEvent
	}
	var assignments []assignment
	// roleDefID -> role name (for classification).
	roleNames := map[string]string{}

	for _, e := range events {
		if e.Provider != "azure" {
			continue
		}
		attrs := payloadMap(e)
		switch e.ResourceType {
		case "azure_subscription":
			res.Entities = append(res.Entities, azEntity(e, attrs, model.KindCloudAccount, "name"))
		case "azure_managed_identity":
			ent := azEntity(e, attrs, model.KindIdentity, "display_name")
			// Key the identity by its principal GUID when present, since role
			// assignments reference principal_id, not the resource id.
			if pid := stringAttr(attrs, "principal_id"); pid != "" {
				ent.Key = pid
			}
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "azure_storage_account":
			ent := azEntity(e, attrs, model.KindDataStore, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
			stores[strings.ToLower(ent.Key)] = true
		case "azure_compute_virtual_machine":
			ent := azEntity(e, attrs, model.KindCompute, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "azure_kubernetes_cluster":
			ent := azEntity(e, attrs, model.KindCluster, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "azure_container_app":
			ent := azEntity(e, attrs, model.KindWorkload, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "azure_openai":
			ent := azEntity(e, attrs, model.KindAIService, "name")
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, containsEdge(e, ent.Key))
		case "azure_role_definition":
			roleNames[strings.ToLower(e.ResourceID)] = firstNonEmpty(
				stringAttr(attrs, "role_name"), stringAttr(attrs, "name"))
		case "azure_role_assignment":
			assignments = append(assignments, assignment{
				principalID: stringAttr(attrs, "principal_id"),
				roleDefID:   stringAttr(attrs, "role_definition_id"),
				scope:       stringAttr(attrs, "scope"),
				ev:          e,
			})
		}
	}

	// Derive CAN_* edges from role assignments scoped to a known storage
	// account. The principal id maps to the identity key.
	for _, a := range assignments {
		scope := strings.ToLower(a.scope)
		if !stores[scope] {
			continue
		}
		roleName := roleNames[strings.ToLower(a.roleDefID)]
		acc := classifyAzureRole(roleName)
		if acc == "" {
			continue
		}
		// DstKey must match the DataStore entity key (lowercased resource id).
		res.Edges = append(res.Edges, cloudAccessEdge(a.ev, a.principalID, scope, acc, roleName))
	}

	return res, nil
}

// azEntity builds a canonical entity for an Azure resource. Azure resource IDs
// are lowercased for stable keys (full normalization in Task 10).
func azEntity(e rawevent.RawEvent, attrs map[string]any, kind model.Kind, nameKey string) model.Entity {
	return model.Entity{
		Kind:       kind,
		Key:        strings.ToLower(e.ResourceID),
		Provider:   "azure",
		Name:       firstNonEmpty(stringAttr(attrs, nameKey), stringAttr(attrs, "name"), e.ResourceID),
		Attributes: attrs,
		Evidence:   EvidenceFrom(e),
	}
}

// classifyAzureRole maps an Azure built-in data role to an access level.
func classifyAzureRole(role string) accessLevel {
	r := strings.ToLower(role)
	switch {
	case strings.Contains(r, "owner"), strings.Contains(r, "contributor") && strings.Contains(r, "storage"),
		r == "contributor", strings.Contains(r, "storage account contributor"):
		return accessAdmin
	case strings.Contains(r, "blob data contributor"), strings.Contains(r, "data contributor"),
		strings.Contains(r, "data owner"):
		return accessWrite
	case strings.Contains(r, "blob data reader"), strings.Contains(r, "data reader"),
		strings.Contains(r, "reader"):
		return accessRead
	}
	return ""
}
