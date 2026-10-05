// Package resolve assigns canonical IDs to entities and rewrites edge
// endpoints to those IDs. Task 4 ships a pass-through resolver that derives IDs
// from strong keys only; Task 10 replaces it with deterministic multi-key
// resolution and alias tracking.
package resolve

import (
	"fmt"
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
)

// Resolver assigns canonical IDs and merges entities that share one.
type Resolver interface {
	Resolve(in normalize.Result) (normalize.Result, error)
}

// PassThrough derives a canonical ID directly from each entity's strong key.
// It merges entities with the same canonical ID (unioning evidence and
// attributes) and rewrites edge endpoints. Edges whose endpoints cannot be
// resolved to a known entity are dropped with no silent merging.
type PassThrough struct{}

// CanonicalID builds kind:provider:normalized-native-id.
//
// Images are keyed purely by digest (image:sha256:...), since the digest is
// globally unique and the join key across ECR/K8s/SBOM. Packages keep their
// purl. Everything else is namespaced by kind and provider.
func CanonicalID(kind model.Kind, provider, key string) string {
	switch kind {
	case model.KindContainerImage:
		// key is already sha256:...
		return "image:" + key
	case model.KindPackage:
		return "package:" + key
	case model.KindVulnerability:
		// key is cve:ID
		return "vuln:" + strings.TrimPrefix(key, "cve:")
	case model.KindNetworkEndpoint:
		return "ip:" + key
	default:
		return fmt.Sprintf("%s:%s:%s", strings.ToLower(string(kind)), provider, key)
	}
}

// Resolve implements Resolver.
func (PassThrough) Resolve(in normalize.Result) (normalize.Result, error) {
	// keyIndex maps an entity Key to its canonical ID.
	keyIndex := make(map[string]string, len(in.Entities))
	merged := make(map[string]*model.Entity, len(in.Entities))
	var order []string

	for i := range in.Entities {
		ent := in.Entities[i]
		if ent.Key == "" {
			// No strong key: cannot resolve deterministically. Skip in V1
			// pass-through (Task 10 handles weak keys/aliases).
			continue
		}
		cid := CanonicalID(ent.Kind, ent.Provider, ent.Key)
		ent.CanonicalID = cid
		keyIndex[ent.Key] = cid

		if existing, ok := merged[cid]; ok {
			mergeEntity(existing, ent)
		} else {
			e := ent
			merged[cid] = &e
			order = append(order, cid)
		}
	}

	out := normalize.Result{}
	for _, cid := range order {
		out.Entities = append(out.Entities, *merged[cid])
	}

	// Rewrite edges. An endpoint resolves if its key matches a known entity
	// key; otherwise try treating the key as already-canonical.
	for _, edge := range in.Edges {
		srcID, okS := resolveEndpoint(keyIndex, merged, edge.SrcKey)
		dstID, okD := resolveEndpoint(keyIndex, merged, edge.DstKey)
		if !okS || !okD {
			continue // drop unresolved edges rather than merge silently
		}
		edge.SrcCanonicalID = srcID
		edge.DstCanonicalID = dstID
		out.Edges = append(out.Edges, edge)
	}

	return out, nil
}

func resolveEndpoint(keyIndex map[string]string, merged map[string]*model.Entity, key string) (string, bool) {
	if cid, ok := keyIndex[key]; ok {
		return cid, true
	}
	if _, ok := merged[key]; ok {
		return key, true // key was already a canonical ID
	}
	return "", false
}

// mergeEntity folds src into dst: union evidence, prefer existing name, merge
// attributes (dst wins on conflict).
func mergeEntity(dst *model.Entity, src model.Entity) {
	dst.Evidence = append(dst.Evidence, src.Evidence...)
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Attributes == nil {
		dst.Attributes = map[string]any{}
	}
	for k, v := range src.Attributes {
		existing, exists := dst.Attributes[k]
		// Add missing keys, and prefer a non-empty value over an empty one so
		// a richer source (e.g. an SBOM with the OCI source label) fills gaps
		// left by a sparser one (e.g. a scanner without the label). Never
		// overwrite an existing non-empty value with an empty one.
		if !exists || (isEmptyValue(existing) && !isEmptyValue(v)) {
			dst.Attributes[k] = v
		}
	}
}

// isEmptyValue reports whether an attribute value is "empty" for merge
// purposes (nil, empty string, empty collection).
func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}
