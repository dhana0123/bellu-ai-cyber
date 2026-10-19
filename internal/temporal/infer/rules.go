package infer

import (
	"context"
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// This file defines the five rule families of the derived lane's inference
// engine. Each rule is a struct implementing Rule (engine.go): it reads ONLY
// observed/normalized entity and edge attributes plus the per-element evidence
// linkage from InferInput, and emits candidate InferredEdges reusing existing
// model.Relation constants. No rule reads derived output, and no rule invents a
// relation kind. The signal→edge mapping mirrors the design's "Inference
// engine" table:
//
//	env vars / secret references          -> workload DEPENDS_ON datastore/service
//	event-source mappings                 -> service CALLS service
//	IaC + repo/CI metadata                -> repo BUILT_FROM / RUNS_IMAGE workload
//	workload execution role               -> workload AUTHENTICATES_AS identity
//	Datadog/Sentry monitor configuration  -> monitor USES resource
//
// Rules emit SrcID/DstID as entity canonical ids (model.Entity.CanonicalID),
// attach raw-event evidence ids, and set a per-signal confidence. The engine
// stamps Origin (OriginRule) and Producer (the rule Name) for them.

// DefaultRules returns the five rule families in a deterministic order, so an
// engine built from them produces a deterministic candidate list.
func DefaultRules() []Rule {
	return []Rule{
		EnvVarDependencyRule{},
		EventSourceCallRule{},
		RepoBuildRule{},
		ExecutionRoleRule{},
		MonitorConfigRule{},
	}
}

// ---- entity index + shared helpers ----

// entityIndex is a lookup of a run's entities keyed by both canonical id and
// native key, so a rule can resolve a reference found in one entity's
// attributes (an ARN, name, or key) to another entity in the same run. It is
// built once per Infer call from in.Entities only (observed facts).
type entityIndex struct {
	byCanonical map[string]model.Entity
	byKey       map[string]model.Entity
	byName      map[string]model.Entity
}

func newEntityIndex(ents []model.Entity) entityIndex {
	ix := entityIndex{
		byCanonical: make(map[string]model.Entity, len(ents)),
		byKey:       make(map[string]model.Entity, len(ents)),
		byName:      make(map[string]model.Entity, len(ents)),
	}
	for _, e := range ents {
		if e.CanonicalID != "" {
			ix.byCanonical[e.CanonicalID] = e
		}
		if e.Key != "" {
			ix.byKey[e.Key] = e
		}
		if e.Name != "" {
			ix.byName[e.Name] = e
		}
	}
	return ix
}

// resolve finds an entity by any of its identifiers (canonical id, native key,
// or name). It returns the entity and whether a match was found.
func (ix entityIndex) resolve(ref string) (model.Entity, bool) {
	if ref == "" {
		return model.Entity{}, false
	}
	if e, ok := ix.byCanonical[ref]; ok {
		return e, true
	}
	if e, ok := ix.byKey[ref]; ok {
		return e, true
	}
	if e, ok := ix.byName[ref]; ok {
		return e, true
	}
	return model.Entity{}, false
}

// id returns the preferred stable identifier of an entity for an InferredEdge
// endpoint: its resolved CanonicalID when present, else its native Key.
func entityID(e model.Entity) string {
	if e.CanonicalID != "" {
		return e.CanonicalID
	}
	return e.Key
}

// evidenceOf returns the raw-event ids backing an entity, preferring the
// explicit linkage in in.Evidence (keyed by canonical id) and falling back to
// the ids carried on the entity's own Evidence slice.
func evidenceOf(in InferInput, e model.Entity) []string {
	if ids, ok := in.Evidence[e.CanonicalID]; ok && len(ids) > 0 {
		return append([]string(nil), ids...)
	}
	var ids []string
	for _, ev := range e.Evidence {
		if ev.RawEventID != "" {
			ids = append(ids, ev.RawEventID)
		}
	}
	return ids
}

// mergeEvidence concatenates two evidence id slices, de-duplicating while
// preserving first-seen order, so an edge inferred from two entities carries
// both their provenance without repeats.
func mergeEvidence(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string(nil), a...), b...) {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// --- attribute readers (tolerant of the any-typed normalized bags) ---

func strAttr(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	s, _ := attrs[key].(string)
	return s
}

// mapsIn returns the []map[string]any view of an attribute that normalizers
// store as a JSON array of objects (e.g. container_definitions, environment).
// It tolerates both []any (post-JSON decode) and []map[string]any.
func mapsIn(attrs map[string]any, key string) []map[string]any {
	if attrs == nil {
		return nil
	}
	switch v := attrs[key].(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, it := range v {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

// kindOf reports whether an entity is one of the given kinds.
func kindOf(e model.Entity, kinds ...model.Kind) bool {
	for _, k := range kinds {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// ---- 1. env-var / secret-ref -> workload DEPENDS_ON datastore/service ----

// EnvVarDependencyRule infers that a workload depends on a datastore or service
// it references through an environment variable value or a secret reference. A
// container definition's environment variables and secret ARNs name resources
// (a bucket, a table, an endpoint) the workload reads at runtime but that no
// asserted edge captures. When the referenced value resolves to another entity
// in the run, the rule emits workload DEPENDS_ON that entity.
type EnvVarDependencyRule struct{}

// Name implements Rule.
func (EnvVarDependencyRule) Name() string { return "env-var-dependency" }

// Infer implements Rule. It scans each workload/compute entity's container
// definitions for environment-variable values and secret references, resolves
// each to a datastore or service entity in the run, and emits a DEPENDS_ON
// candidate edge for every resolved reference.
func (r EnvVarDependencyRule) Infer(ctx context.Context, in InferInput) ([]InferredEdge, error) {
	ix := newEntityIndex(in.Entities)
	var out []InferredEdge

	for _, e := range in.Entities {
		if !kindOf(e, model.KindWorkload, model.KindCompute) {
			continue
		}
		srcEv := evidenceOf(in, e)
		seen := map[string]struct{}{}

		emit := func(ref string, conf float64) {
			dst, ok := ix.resolve(ref)
			if !ok {
				return
			}
			if !kindOf(dst, model.KindDataStore, model.KindAIService, model.KindExternalService, model.KindNetworkEndpoint) {
				return
			}
			dstID := entityID(dst)
			if dstID == "" || dstID == entityID(e) {
				return
			}
			if _, dup := seen[dstID]; dup {
				return
			}
			seen[dstID] = struct{}{}
			out = append(out, InferredEdge{
				SrcID:      entityID(e),
				Relation:   model.RelDependsOn,
				DstID:      dstID,
				Confidence: conf,
				Evidence:   mergeEvidence(srcEv, evidenceOf(in, dst)),
			})
		}

		for _, cd := range mapsIn(e.Attributes, "container_definitions") {
			// environment: [{name, value}] — a value may be an ARN/name of a store.
			for _, kv := range mapsIn(cd, "environment") {
				emit(strAttr(kv, "value"), 0.6)
			}
			// secrets: [{name, valueFrom}] — valueFrom is a secret/parameter ARN.
			for _, sref := range mapsIn(cd, "secrets") {
				emit(strAttr(sref, "valueFrom"), 0.75)
			}
		}
		// Flat environment on the entity itself (e.g. a Lambda's env map).
		if env, ok := e.Attributes["environment"].(map[string]any); ok {
			for _, v := range env {
				if s, ok := v.(string); ok {
					emit(s, 0.6)
				}
			}
		}
	}
	return out, nil
}

// ---- 2. event-source mapping -> service CALLS service ----

// EventSourceCallRule infers a service-to-service CALLS edge from an
// event-source mapping: a workload/compute (the consumer) wired to an event
// source (a queue/stream/topic) names a producer the consumer effectively
// calls. The mapping's event_source_arn (or source_arn) resolves to the source
// entity in the run.
type EventSourceCallRule struct{}

// Name implements Rule.
func (EventSourceCallRule) Name() string { return "event-source-mapping-call" }

// Infer implements Rule. It reads event_source_mappings off each
// service-like entity, resolves each mapping's source ARN to another entity,
// and emits a CALLS candidate edge from the consumer to the resolved source.
func (r EventSourceCallRule) Infer(ctx context.Context, in InferInput) ([]InferredEdge, error) {
	ix := newEntityIndex(in.Entities)
	var out []InferredEdge

	for _, e := range in.Entities {
		if !kindOf(e, model.KindWorkload, model.KindCompute, model.KindAIService, model.KindExternalService) {
			continue
		}
		srcEv := evidenceOf(in, e)
		seen := map[string]struct{}{}

		emit := func(ref string) {
			dst, ok := ix.resolve(ref)
			if !ok {
				return
			}
			dstID := entityID(dst)
			if dstID == "" || dstID == entityID(e) {
				return
			}
			if _, dup := seen[dstID]; dup {
				return
			}
			seen[dstID] = struct{}{}
			out = append(out, InferredEdge{
				SrcID:      entityID(e),
				Relation:   model.RelCalls,
				DstID:      dstID,
				Confidence: 0.7,
				Evidence:   mergeEvidence(srcEv, evidenceOf(in, dst)),
			})
		}

		for _, m := range mapsIn(e.Attributes, "event_source_mappings") {
			emit(strAttr(m, "event_source_arn"))
			emit(strAttr(m, "source_arn"))
		}
		// A single event source attribute (e.g. a trigger arn) also counts.
		emit(strAttr(e.Attributes, "event_source_arn"))
	}
	return out, nil
}

// ---- 3. IaC + repo/CI -> repo BUILT_FROM / RUNS_IMAGE workload ----

// RepoBuildRule infers build-provenance edges from IaC and repo/CI metadata. A
// workload or container image that carries a source-repository reference
// (repository URL, source repo, build repo) is built from that repository: the
// rule emits repository BUILT_FROM workload, and repository RUNS_IMAGE for a
// container image, reusing both existing relations.
type RepoBuildRule struct{}

// Name implements Rule.
func (RepoBuildRule) Name() string { return "repo-build-provenance" }

// Infer implements Rule. For each workload/image entity it reads the
// repository reference from IaC/CI metadata, resolves it to a Repository
// entity, and emits BUILT_FROM (to a workload) or RUNS_IMAGE (to a container
// image) from that repository.
func (r RepoBuildRule) Infer(ctx context.Context, in InferInput) ([]InferredEdge, error) {
	ix := newEntityIndex(in.Entities)
	var out []InferredEdge

	repoRefKeys := []string{
		"source_repository", "repository", "repository_url",
		"repo", "repo_url", "git_repository", "build_source_repository",
	}

	for _, e := range in.Entities {
		if !kindOf(e, model.KindWorkload, model.KindContainerImage) {
			continue
		}
		var repo model.Entity
		found := false
		for _, k := range repoRefKeys {
			if cand, ok := ix.resolve(strAttr(e.Attributes, k)); ok && cand.Kind == model.KindRepository {
				repo, found = cand, true
				break
			}
		}
		if !found {
			continue
		}
		repoID := entityID(repo)
		if repoID == "" || repoID == entityID(e) {
			continue
		}
		rel := model.RelBuiltFrom
		if e.Kind == model.KindContainerImage {
			rel = model.RelRunsImage
		}
		out = append(out, InferredEdge{
			SrcID:      repoID,
			Relation:   rel,
			DstID:      entityID(e),
			Confidence: 0.65,
			Evidence:   mergeEvidence(evidenceOf(in, repo), evidenceOf(in, e)),
		})
	}
	return out, nil
}

// ---- 4. workload execution role -> workload AUTHENTICATES_AS identity ----

// ExecutionRoleRule infers that a workload authenticates as the identity named
// by its execution/task role. A workload that carries an execution_role_arn (or
// task_role_arn/role) assumes that identity at runtime; the rule emits workload
// AUTHENTICATES_AS identity when the role resolves to an identity entity.
type ExecutionRoleRule struct{}

// Name implements Rule.
func (ExecutionRoleRule) Name() string { return "execution-role-identity" }

// Infer implements Rule. It reads the execution/task role reference off each
// workload/compute entity, resolves it to an Identity entity, and emits an
// AUTHENTICATES_AS candidate edge from the workload to that identity.
func (r ExecutionRoleRule) Infer(ctx context.Context, in InferInput) ([]InferredEdge, error) {
	ix := newEntityIndex(in.Entities)
	var out []InferredEdge

	roleKeys := []string{"execution_role_arn", "task_role_arn", "role_arn", "role"}

	for _, e := range in.Entities {
		if !kindOf(e, model.KindWorkload, model.KindCompute) {
			continue
		}
		srcEv := evidenceOf(in, e)
		seen := map[string]struct{}{}

		for _, k := range roleKeys {
			id, ok := ix.resolve(strAttr(e.Attributes, k))
			if !ok || id.Kind != model.KindIdentity {
				continue
			}
			dstID := entityID(id)
			if dstID == "" || dstID == entityID(e) {
				continue
			}
			if _, dup := seen[dstID]; dup {
				continue
			}
			seen[dstID] = struct{}{}
			out = append(out, InferredEdge{
				SrcID:      entityID(e),
				Relation:   model.RelAuthenticatesAs,
				DstID:      dstID,
				Confidence: 0.8,
				Evidence:   mergeEvidence(srcEv, evidenceOf(in, id)),
			})
		}
	}
	return out, nil
}

// ---- 5. monitor config -> monitor USES resource ----

// MonitorConfigRule infers that a monitor (a Datadog/Sentry monitor collected
// as configuration, not as a log stream) uses the resource it watches. A
// monitor entity names its monitored resources through a monitored_resources
// list or a single monitored_resource/target reference; each resolved target
// yields monitor USES resource.
type MonitorConfigRule struct{}

// Name implements Rule.
func (MonitorConfigRule) Name() string { return "monitor-config-uses" }

// Infer implements Rule. It reads the monitored-resource references off each
// monitor entity (identified by a monitor marker attribute), resolves each to
// an entity in the run, and emits a USES candidate edge from the monitor to the
// resolved resource.
func (r MonitorConfigRule) Infer(ctx context.Context, in InferInput) ([]InferredEdge, error) {
	ix := newEntityIndex(in.Entities)
	var out []InferredEdge

	for _, e := range in.Entities {
		if !isMonitor(e) {
			continue
		}
		srcEv := evidenceOf(in, e)
		seen := map[string]struct{}{}

		emit := func(ref string) {
			dst, ok := ix.resolve(ref)
			if !ok {
				return
			}
			dstID := entityID(dst)
			if dstID == "" || dstID == entityID(e) {
				return
			}
			if _, dup := seen[dstID]; dup {
				return
			}
			seen[dstID] = struct{}{}
			out = append(out, InferredEdge{
				SrcID:      entityID(e),
				Relation:   model.RelUses,
				DstID:      dstID,
				Confidence: 0.7,
				Evidence:   mergeEvidence(srcEv, evidenceOf(in, dst)),
			})
		}

		for _, m := range mapsIn(e.Attributes, "monitored_resources") {
			emit(strAttr(m, "arn"))
			emit(strAttr(m, "id"))
			emit(strAttr(m, "resource"))
		}
		// A list of plain string refs, or a single reference.
		if refs, ok := e.Attributes["monitored_resources"].([]any); ok {
			for _, v := range refs {
				if s, ok := v.(string); ok {
					emit(s)
				}
			}
		}
		emit(strAttr(e.Attributes, "monitored_resource"))
		emit(strAttr(e.Attributes, "target"))
	}
	return out, nil
}

// isMonitor reports whether an entity represents monitor configuration. A
// monitor is collected as an external service carrying a monitor marker
// attribute (resource_subtype/service "monitor", or a provider of a monitoring
// vendor), so the rule does not depend on a dedicated kind that the model does
// not define.
func isMonitor(e model.Entity) bool {
	if !kindOf(e, model.KindExternalService, model.KindAIService) {
		return false
	}
	for _, k := range []string{"resource_subtype", "subtype", "service", "type", "kind"} {
		if strings.Contains(strings.ToLower(strAttr(e.Attributes, k)), "monitor") {
			return true
		}
	}
	switch strings.ToLower(e.Provider) {
	case "datadog", "sentry":
		return true
	}
	return false
}
