package normalize

import (
	"encoding/json"
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	cdxsrc "github.com/bellu/bellu-ai-cyber/internal/source/cyclonedx"
)

// CycloneDX maps CycloneDX RawEvents to canonical entities and edges.
//
//	image subject    -> ContainerImage
//	repo subject     -> Repository
//	library/package  -> Package (keyed by purl)
//	ML model         -> AIModel
//	prompt data      -> Prompt
//	MCP service      -> MCPServer
//	AI service       -> AIService
//	dependency       -> DEPENDS_ON
//	vulnerability    -> Vulnerability + HAS_VULN
//	model dependency -> USES_MODEL
type CycloneDX struct{}

// Sources implements Normalizer.
func (CycloneDX) Sources() []string { return []string{"cyclonedx"} }

// Normalize implements Normalizer.
func (CycloneDX) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result
	// Track which refs/purls are models so dependency edges can be typed as
	// USES_MODEL instead of DEPENDS_ON.
	modelKeys := map[string]bool{}
	// Map bom-ref -> canonical key so dependency refs (which may be bom-refs
	// or purls) resolve to the entity key we assigned.
	refToKey := map[string]string{}

	// First pass: entities.
	for _, e := range events {
		switch e.ResourceType {
		case cdxsrc.RTImage:
			ent := imageEntity(e)
			res.Entities = append(res.Entities, ent)
			registerRefs(refToKey, e, ent.Key)
		case cdxsrc.RTRepository:
			ent := repoEntity(e)
			res.Entities = append(res.Entities, ent)
			registerRefs(refToKey, e, ent.Key)
		case cdxsrc.RTComponent:
			ent, isModel := componentEntity(e)
			res.Entities = append(res.Entities, ent)
			registerRefs(refToKey, e, ent.Key)
			if isModel {
				modelKeys[ent.Key] = true
			}
		case cdxsrc.RTService:
			ent := serviceEntity(e)
			res.Entities = append(res.Entities, ent)
			registerRefs(refToKey, e, ent.Key)
		case cdxsrc.RTVuln:
			ent, edges := vulnEntityAndEdges(e)
			res.Entities = append(res.Entities, ent)
			res.Edges = append(res.Edges, edges...)
		}
	}

	// Second pass: dependency edges.
	for _, e := range events {
		if e.ResourceType != cdxsrc.RTDependency {
			continue
		}
		var p struct {
			Ref       string   `json:"ref"`
			DependsOn []string `json:"depends_on"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		src := resolveRef(refToKey, p.Ref)
		for _, dep := range p.DependsOn {
			dst := resolveRef(refToKey, dep)
			rel := model.RelDependsOn
			if modelKeys[dst] {
				rel = model.RelUsesModel
			}
			res.Edges = append(res.Edges, model.Edge{
				Relation:   rel,
				SrcKey:     src,
				DstKey:     dst,
				Confidence: 1.0,
				Evidence:   EvidenceFrom(e),
			})
		}
	}

	return res, nil
}

func imageEntity(e rawevent.RawEvent) model.Entity {
	attrs := payloadMap(e)
	return model.Entity{
		Kind:       model.KindContainerImage,
		Key:        e.ResourceID, // sha256:... digest
		Provider:   "image",
		Name:       stringAttr(attrs, "name"),
		Attributes: attrs,
		Evidence:   EvidenceFrom(e),
	}
}

func repoEntity(e rawevent.RawEvent) model.Entity {
	attrs := payloadMap(e)
	return model.Entity{
		Kind:       model.KindRepository,
		Key:        e.ResourceID, // repo url or purl
		Provider:   "repo",
		Name:       stringAttr(attrs, "name"),
		Attributes: attrs,
		Evidence:   EvidenceFrom(e),
	}
}

// componentEntity maps a component to Package, AIModel or Prompt. It returns
// whether the component is an AI model.
func componentEntity(e rawevent.RawEvent) (model.Entity, bool) {
	attrs := payloadMap(e)
	typ := stringAttr(attrs, "type")
	name := stringAttr(attrs, "name")

	switch {
	case typ == "machine-learning-model":
		return model.Entity{
			Kind:       model.KindAIModel,
			Key:        e.ResourceID,
			Provider:   "repo",
			Name:       name,
			Attributes: attrs,
			Evidence:   EvidenceFrom(e),
		}, true
	case isPrompt(typ, name, attrs):
		return model.Entity{
			Kind:       model.KindPrompt,
			Key:        e.ResourceID,
			Provider:   "repo",
			Name:       name,
			Attributes: attrs,
			Evidence:   EvidenceFrom(e),
		}, false
	default:
		return model.Entity{
			Kind:       model.KindPackage,
			Key:        e.ResourceID, // purl
			Provider:   "repo",
			Name:       name,
			Attributes: attrs,
			Evidence:   EvidenceFrom(e),
		}, false
	}
}

// isPrompt detects prompt artifacts in AI-BOM output.
func isPrompt(typ, name string, attrs map[string]any) bool {
	if props, ok := attrs["properties"].(map[string]any); ok {
		for k := range props {
			if strings.HasPrefix(k, "ai:prompt") {
				return true
			}
		}
	}
	ln := strings.ToLower(name)
	return typ == "data" && (strings.Contains(ln, "prompt"))
}

func serviceEntity(e rawevent.RawEvent) model.Entity {
	attrs := payloadMap(e)
	kind := model.KindAIService
	if isMCP(attrs) {
		kind = model.KindMCPServer
	}
	return model.Entity{
		Kind:       kind,
		Key:        e.ResourceID,
		Provider:   "repo",
		Name:       stringAttr(attrs, "name"),
		Attributes: attrs,
		Evidence:   EvidenceFrom(e),
	}
}

func isMCP(attrs map[string]any) bool {
	if strings.EqualFold(stringAttr(attrs, "group"), "mcp") {
		return true
	}
	if props, ok := attrs["properties"].(map[string]any); ok {
		for k := range props {
			if strings.HasPrefix(k, "ai:mcp") {
				return true
			}
		}
	}
	return false
}

func vulnEntityAndEdges(e rawevent.RawEvent) (model.Entity, []model.Edge) {
	attrs := payloadMap(e)
	id := stringAttr(attrs, "id")
	ent := model.Entity{
		Kind:       model.KindVulnerability,
		Key:        "cve:" + id,
		Provider:   "repo",
		Name:       id,
		Attributes: attrs,
		Evidence:   EvidenceFrom(e),
	}
	var edges []model.Edge
	if affects, ok := attrs["affects"].([]any); ok {
		for _, a := range affects {
			pkg, _ := a.(string)
			if pkg == "" {
				continue
			}
			edges = append(edges, model.Edge{
				Relation:   model.RelHasVuln,
				SrcKey:     pkg, // package purl
				DstKey:     ent.Key,
				Confidence: 1.0,
				Evidence:   EvidenceFrom(e),
			})
		}
	}
	return ent, edges
}

// registerRefs records both the bom-ref and the resource id for a component so
// dependency references resolve to the entity key.
func registerRefs(m map[string]string, e rawevent.RawEvent, key string) {
	m[e.ResourceID] = key
	if attrs := payloadMap(e); attrs != nil {
		if ref := stringAttr(attrs, "bom_ref"); ref != "" {
			m[ref] = key
		}
		if purl := stringAttr(attrs, "purl"); purl != "" {
			m[purl] = key
		}
	}
}

// resolveRef maps a dependency ref to the registered entity key, falling back
// to the ref itself.
func resolveRef(m map[string]string, ref string) string {
	if k, ok := m[ref]; ok {
		return k
	}
	return ref
}

func payloadMap(e rawevent.RawEvent) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(e.Payload, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func stringAttr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
