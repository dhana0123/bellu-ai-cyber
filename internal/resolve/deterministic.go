package resolve

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
)

// Alias records that a source observed an entity under alias_key and that it
// resolved to canonical_id. Aliases are time-bounded for reused keys (IPs).
type Alias struct {
	AliasKey    string
	CanonicalID string
	Source      string
}

// Conflict records a case where the resolver declined to merge or found
// inconsistent evidence, for later review. The resolver never merges silently
// on weak keys.
type Conflict struct {
	Key    string
	Reason string
	Detail string
}

// Deterministic is the production resolver. It normalizes strong keys (image
// digests, Azure IDs, repo URLs), merges entities that resolve to the same
// canonical id, records aliases, derives repo->image BUILT_FROM links from OCI
// source labels (or a configured fallback), and logs conflicts.
type Deterministic struct {
	// RepoImageMappings is a fallback map of normalized repo URL -> image
	// digest, used when the OCI source label is absent. Weakest link.
	RepoImageMappings map[string]string

	// Captured outputs for persistence/inspection.
	Aliases   []Alias
	Conflicts []Conflict
}

// NewDeterministic returns a resolver with optional repo->image fallback
// mappings.
func NewDeterministic(mappings map[string]string) *Deterministic {
	return &Deterministic{RepoImageMappings: mappings}
}

// Resolve implements Resolver.
func (d *Deterministic) Resolve(in normalize.Result) (normalize.Result, error) {
	d.Aliases = nil
	d.Conflicts = nil

	// keyIndex maps BOTH the raw key and the normalized key to the canonical id
	// so edges referencing either form resolve.
	keyIndex := make(map[string]string, len(in.Entities)*2)
	merged := make(map[string]*model.Entity, len(in.Entities))
	var order []string

	// Track repos and images for the weak BUILT_FROM linking pass.
	repoURLs := map[string]string{}   // normalized repo url -> canonical id
	imageByDigest := map[string]string{} // sha256 digest -> canonical id
	ipOwner := map[string]string{}       // ip -> owning workload/compute canonical id

	for i := range in.Entities {
		ent := in.Entities[i]
		if ent.Key == "" {
			continue
		}
		normKey := NormalizeKey(ent.Kind, ent.Provider, ent.Key)
		cid := CanonicalID(ent.Kind, ent.Provider, normKey)
		ent.CanonicalID = cid

		// Index both forms; record an alias when the raw key differed.
		keyIndex[ent.Key] = cid
		keyIndex[normKey] = cid
		for _, ev := range ent.Evidence {
			if ent.Key != normKey {
				d.Aliases = append(d.Aliases, Alias{AliasKey: ent.Key, CanonicalID: cid, Source: ev.Source})
			}
		}

		if existing, ok := merged[cid]; ok {
			d.detectConflict(existing, ent)
			mergeEntity(existing, ent)
		} else {
			e := ent
			merged[cid] = &e
			order = append(order, cid)
		}

		switch ent.Kind {
		case model.KindRepository:
			repoURLs[normKey] = cid
		case model.KindContainerImage:
			imageByDigest[normalizeImageKey(normKey)] = cid
		}

		// IP alias: a Workload/Compute with an "ip" (or "pod_ip") attribute is
		// the real owner of that address. Index IP -> this entity's canonical
		// id so Zeek flows keyed by IP resolve to the workload rather than a
		// bare NetworkEndpoint. IPs are reused over time, so this is recorded
		// as a (time-bounded) alias in Postgres by the caller.
		if ent.Kind == model.KindWorkload || ent.Kind == model.KindCompute {
			for _, ipKey := range []string{"ip", "pod_ip", "private_ip"} {
				if ip, ok := ent.Attributes[ipKey].(string); ok && ip != "" {
					ipOwner[ip] = cid
					for _, ev := range ent.Evidence {
						d.Aliases = append(d.Aliases, Alias{AliasKey: "ip:" + ip, CanonicalID: cid, Source: ev.Source})
					}
				}
			}
		}
	}

	out := normalize.Result{}
	for _, cid := range order {
		out.Entities = append(out.Entities, *merged[cid])
	}

	// Rewrite edges, normalizing endpoint keys the same way. For CONNECTS_TO,
	// a source IP owned by a workload/compute resolves to that owner so network
	// flows attach to the real workload node.
	for _, edge := range in.Edges {
		srcID, okS := d.resolveEndpointIP(keyIndex, merged, ipOwner, edge.SrcKey)
		dstID, okD := d.resolveEndpointIP(keyIndex, merged, ipOwner, edge.DstKey)
		if !okS || !okD {
			continue
		}
		edge.SrcCanonicalID = srcID
		edge.DstCanonicalID = dstID
		out.Edges = append(out.Edges, edge)
	}

	// Weak link: repo -> image BUILT_FROM. OCI source label first, then the
	// configured fallback. confidence 0.7, never merges nodes.
	out.Edges = append(out.Edges, d.deriveBuiltFrom(merged, repoURLs, imageByDigest)...)

	return out, nil
}

// resolveEndpointIP first checks whether the key is an IP owned by a workload/
// compute; if so it resolves to that owner. Otherwise it defers to the normal
// endpoint resolution.
func (d *Deterministic) resolveEndpointIP(keyIndex map[string]string, merged map[string]*model.Entity, ipOwner map[string]string, key string) (string, bool) {
	if cid, ok := ipOwner[key]; ok {
		return cid, true
	}
	return d.resolveEndpoint(keyIndex, merged, key)
}

// resolveEndpoint resolves an edge endpoint by raw key, normalized key, or
// treating the value as an already-canonical id.
func (d *Deterministic) resolveEndpoint(keyIndex map[string]string, merged map[string]*model.Entity, key string) (string, bool) {
	if cid, ok := keyIndex[key]; ok {
		return cid, true
	}
	// Try normalizing as an image digest or repo URL (common edge endpoints).
	if nk := normalizeImageKey(key); nk != key {
		if cid, ok := keyIndex[nk]; ok {
			return cid, true
		}
	}
	if nk := NormalizeRepoURL(key); nk != key {
		if cid, ok := keyIndex[nk]; ok {
			return cid, true
		}
	}
	if _, ok := merged[key]; ok {
		return key, true
	}
	return "", false
}

// deriveBuiltFrom links each image to the repository it was built from, using
// the OCI org.opencontainers.image.source label captured on the image's
// attributes (repo_url), falling back to the configured mappings.
func (d *Deterministic) deriveBuiltFrom(merged map[string]*model.Entity, repoURLs, imageByDigest map[string]string) []model.Edge {
	var edges []model.Edge
	for cid, ent := range merged {
		if ent.Kind != model.KindContainerImage {
			continue
		}
		// 1) OCI source label on the image.
		repoURL := imageRepoLabel(ent)
		if repoURL != "" {
			repoCID, ok := repoURLs[NormalizeRepoURL(repoURL)]
			if ok {
				edges = append(edges, builtFromEdge(ent, repoCID, cid, "oci_label"))
				continue
			}
		}
		// 2) Configured fallback mapping (normalized repo url -> digest).
		for repoURL, digest := range d.RepoImageMappings {
			if normalizeImageKey(digest) == imageDigestOf(ent) {
				if repoCID, ok := repoURLs[NormalizeRepoURL(repoURL)]; ok {
					edges = append(edges, builtFromEdge(ent, repoCID, cid, "mappings_yaml"))
				}
			}
		}
	}
	return edges
}

func builtFromEdge(img *model.Entity, repoCID, imgCID, method string) model.Edge {
	// Image BUILT_FROM Repository. Use the image's evidence.
	return model.Edge{
		Relation:       model.RelBuiltFrom,
		SrcCanonicalID: imgCID,
		DstCanonicalID: repoCID,
		SrcKey:         img.CanonicalID,
		DstKey:         repoCID,
		Attributes:     map[string]any{"method": method},
		Confidence:     0.7, // weakest link
		Evidence:       img.Evidence,
	}
}

func imageRepoLabel(ent *model.Entity) string {
	if v, ok := ent.Attributes["repo_url"].(string); ok {
		return v
	}
	return ""
}

func imageDigestOf(ent *model.Entity) string {
	return normalizeImageKey(ent.Key)
}

// detectConflict records (without blocking) suspicious merges, e.g. two
// entities of different kinds mapping to the same canonical id.
func (d *Deterministic) detectConflict(existing *model.Entity, incoming model.Entity) {
	if existing.Kind != incoming.Kind {
		d.Conflicts = append(d.Conflicts, Conflict{
			Key:    existing.CanonicalID,
			Reason: "kind_mismatch",
			Detail: string(existing.Kind) + " vs " + string(incoming.Kind),
		})
	}
	if existing.Name != "" && incoming.Name != "" && existing.Name != incoming.Name &&
		!strings.EqualFold(existing.Name, incoming.Name) {
		d.Conflicts = append(d.Conflicts, Conflict{
			Key:    existing.CanonicalID,
			Reason: "name_mismatch",
			Detail: existing.Name + " vs " + incoming.Name,
		})
	}
}
