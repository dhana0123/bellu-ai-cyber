package normalize

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize/k8swi"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// K8s maps Steampipe Kubernetes RawEvents to canonical entities and edges and
// bridges workload identities to cloud identities.
//
// Kinds:  Workload (pods, deployments), Identity (service accounts).
// Edges:  RUNS_IN (pod -> cluster), RUNS_IMAGE (pod -> image digest),
//         USES (workload -> service account), AUTHENTICATES_AS (SA -> cloud
//         identity via IRSA / GKE WI / Azure WI annotations).
//
// The cluster the events belong to is taken from the Scope/collector or an
// explicit "cluster_id" attribute so pods can link to the cloud Cluster node.
type K8s struct{}

// Sources implements Normalizer.
func (K8s) Sources() []string { return []string{"steampipe"} }

// Normalize implements Normalizer, filtering to k8s provider events.
func (K8s) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result

	// Map service account "namespace/name" -> canonical SA key, so pods and
	// deployments that reference a SA by name can link to it. Built in a first
	// pass because the input order is not guaranteed (the evidence store may
	// return pods before service accounts).
	saByName := map[string]string{}
	for _, e := range events {
		if e.Provider != "k8s" || e.ResourceType != "kubernetes_service_account" {
			continue
		}
		attrs := payloadMap(e)
		saByName[saNameKey(stringAttr(attrs, "namespace"), stringAttr(attrs, "name"))] = "k8s:sa:" + e.ResourceID
	}

	for _, e := range events {
		if e.Provider != "k8s" {
			continue
		}
		attrs := payloadMap(e)
		switch e.ResourceType {
		case "kubernetes_namespace":
			// Namespaces aren't a canonical kind; skipped as nodes in V1.
		case "kubernetes_service_account":
			ent, bindings := k8sServiceAccount(e, attrs)
			res.Entities = append(res.Entities, ent)
			// AUTHENTICATES_AS edges to cloud identities.
			for _, b := range bindings {
				res.Edges = append(res.Edges, model.Edge{
					Relation:   model.RelAuthenticatesAs,
					SrcKey:     ent.Key,
					DstKey:     b.CloudIdentityKey,
					Attributes: map[string]any{"annotation": b.AnnotationKey, "provider": string(b.Provider)},
					Confidence: 1.0,
					Evidence:   EvidenceFrom(e),
				})
			}
		case "kubernetes_pod":
			ent := k8sWorkload(e, attrs, "pod")
			res.Entities = append(res.Entities, ent)
			// RUNS_IN cluster.
			if cluster := clusterKey(e, attrs); cluster != "" {
				res.Edges = append(res.Edges, model.Edge{
					Relation: model.RelRunsIn, SrcKey: ent.Key, DstKey: cluster,
					Confidence: 1.0, Evidence: EvidenceFrom(e),
				})
			}
			// RUNS_IMAGE by digest (imageID carries the digest).
			for _, dig := range podImageDigests(attrs) {
				res.Edges = append(res.Edges, model.Edge{
					Relation: model.RelRunsImage, SrcKey: ent.Key, DstKey: dig,
					Confidence: 1.0, Evidence: EvidenceFrom(e),
				})
			}
			// USES service account.
			if sa := stringAttr(attrs, "service_account_name"); sa != "" {
				res.Edges = append(res.Edges, usesSAEdge(e, ent.Key, stringAttr(attrs, "namespace"), sa, saByName))
			}
		case "kubernetes_deployment":
			ent := k8sWorkload(e, attrs, "deployment")
			res.Entities = append(res.Entities, ent)
			if sa := deploymentSA(attrs); sa != "" {
				res.Edges = append(res.Edges, usesSAEdge(e, ent.Key, stringAttr(attrs, "namespace"), sa, saByName))
			}
		}
	}

	return res, nil
}

// k8sWorkload builds a Workload entity for a pod or deployment. The pod IP is
// surfaced as "pod_ip" so the resolver can map Zeek flows to this workload.
func k8sWorkload(e rawevent.RawEvent, attrs map[string]any, subtype string) model.Entity {
	a := attrs
	a["workload_subtype"] = subtype
	if ip := podIP(attrs); ip != "" {
		a["pod_ip"] = ip
	}
	return model.Entity{
		Kind: model.KindWorkload, Key: "k8s:" + e.ResourceID, Provider: "k8s",
		Name: firstNonEmpty(stringAttr(attrs, "name"), e.ResourceID),
		Attributes: a, Evidence: EvidenceFrom(e),
	}
}

// podIP extracts the pod IP from common Steampipe shapes.
func podIP(attrs map[string]any) string {
	if ip := stringAttr(attrs, "pod_ip"); ip != "" {
		return ip
	}
	if st, ok := attrs["status"].(map[string]any); ok {
		if ip, ok := st["podIP"].(string); ok {
			return ip
		}
		if ip, ok := st["pod_ip"].(string); ok {
			return ip
		}
	}
	return ""
}

// k8sServiceAccount builds an Identity entity and parses WI bindings.
func k8sServiceAccount(e rawevent.RawEvent, attrs map[string]any) (model.Entity, []k8swi.Binding) {
	ent := model.Entity{
		Kind: model.KindIdentity, Key: "k8s:sa:" + e.ResourceID, Provider: "k8s",
		Name: firstNonEmpty(stringAttr(attrs, "name"), e.ResourceID),
		Attributes: attrs, Evidence: EvidenceFrom(e),
	}
	return ent, k8swi.Parse(annotationsOf(attrs))
}

// usesSAEdge links a workload to its service account identity.
func usesSAEdge(e rawevent.RawEvent, workloadKey, ns, saName string, saByName map[string]string) model.Edge {
	dst := saByName[saNameKey(ns, saName)]
	if dst == "" {
		// Fall back to a derived key; resolver will drop if no entity matches.
		dst = "k8s:sa:" + ns + "/" + saName
	}
	return model.Edge{
		Relation: model.RelUses, SrcKey: workloadKey, DstKey: dst,
		Confidence: 1.0, Evidence: EvidenceFrom(e),
	}
}

// ---- extraction helpers ----

func saNameKey(ns, name string) string { return ns + "/" + name }

// annotationsOf extracts the metadata.annotations map (Steampipe exposes it as
// "annotations" at the row level).
func annotationsOf(attrs map[string]any) map[string]string {
	out := map[string]string{}
	raw, ok := attrs["annotations"].(map[string]any)
	if !ok {
		if md, ok := attrs["metadata"].(map[string]any); ok {
			raw, _ = md["annotations"].(map[string]any)
		}
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// clusterKey returns the cloud Cluster node key this event belongs to, from an
// explicit cluster_arn/cluster_id attribute or the collector's cluster name.
func clusterKey(e rawevent.RawEvent, attrs map[string]any) string {
	for _, k := range []string{"cluster_arn", "cluster_id", "cluster_name"} {
		if v := stringAttr(attrs, k); v != "" {
			return v
		}
	}
	return ""
}

// podImageDigests extracts image digests from containerStatuses[].imageID,
// which has the form "<repo>@sha256:<digest>".
func podImageDigests(attrs map[string]any) []string {
	var out []string
	statuses := firstSlice(attrs, "container_statuses", "containerStatuses")
	for _, s := range statuses {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		imageID := firstString(sm, "imageID", "image_id")
		if i := strings.Index(imageID, "@sha256:"); i >= 0 {
			out = append(out, imageID[i+1:])
		}
	}
	// Also check spec containers for pinned digests.
	containers := podContainers(attrs)
	for _, c := range containers {
		img, _ := c["image"].(string)
		if i := strings.Index(img, "@sha256:"); i >= 0 {
			out = append(out, img[i+1:])
		}
	}
	return dedupeStrings(out)
}

func podContainers(attrs map[string]any) []map[string]any {
	var out []map[string]any
	raw := firstSlice(attrs, "containers")
	if raw == nil {
		if spec, ok := attrs["spec"].(map[string]any); ok {
			raw, _ = spec["containers"].([]any)
		}
	}
	for _, c := range raw {
		if cm, ok := c.(map[string]any); ok {
			out = append(out, cm)
		}
	}
	return out
}

func deploymentSA(attrs map[string]any) string {
	// deployment.spec.template.spec.serviceAccountName
	spec, ok := attrs["spec"].(map[string]any)
	if !ok {
		return stringAttr(attrs, "service_account_name")
	}
	tmpl, ok := spec["template"].(map[string]any)
	if !ok {
		return ""
	}
	tspec, ok := tmpl["spec"].(map[string]any)
	if !ok {
		return ""
	}
	return firstString(tspec, "serviceAccountName", "service_account_name")
}

// ---- small generic helpers ----

func firstSlice(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if v, ok := m[k].([]any); ok {
			return v
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
