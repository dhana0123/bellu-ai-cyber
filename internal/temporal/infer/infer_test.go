package infer

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
)

// This file tests the inference engine's five rule families and the hard
// facts/derived separation (design Property 2, Requirements 8.1, 8.2, 4.3,
// 4.4). The rule tests are table-driven: each case constructs an InferInput of
// normalized observed facts (entities/edges with the attribute shapes the rule
// reads) and asserts the expected InferredEdge(s). The separation tests assert
// — both structurally and behaviorally — that the engine reads ONLY observed
// facts (InferInput carries no derived-lane field) and emits only InferredEdge
// values destined for the derived lane, never touching observed_events.

// ---- builders for observed/normalized facts ----

// ent builds a normalized entity (an observed fact). Rules key off Kind,
// identifiers and Attributes; nothing here is derived output.
func ent(kind model.Kind, canonical, name string, attrs map[string]any) model.Entity {
	return model.Entity{
		Kind:        kind,
		CanonicalID: canonical,
		Name:        name,
		Provider:    "aws",
		Attributes:  attrs,
	}
}

// withProvider overrides an entity's provider (used for the Datadog/Sentry
// monitor cases that are keyed by provider rather than attribute marker).
func withProvider(e model.Entity, provider string) model.Entity {
	e.Provider = provider
	return e
}

// edgeKey is a comparable projection of an InferredEdge used to compare an
// unordered set of produced edges against the expected set, ignoring evidence
// ordering (which merges two entities' provenance).
type edgeKey struct {
	Src, Dst string
	Rel      model.Relation
	Conf     float64
	Origin   string
	Producer string
}

func keyOf(e InferredEdge) edgeKey {
	return edgeKey{
		Src:      e.SrcID,
		Dst:      e.DstID,
		Rel:      e.Relation,
		Conf:     e.Confidence,
		Origin:   e.Origin,
		Producer: e.Producer,
	}
}

func keysOf(edges []InferredEdge) []edgeKey {
	out := make([]edgeKey, 0, len(edges))
	for _, e := range edges {
		out = append(out, keyOf(e))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		return out[i].Rel < out[j].Rel
	})
	return out
}

// TestRules_InputToDerivedEdges is the table-driven core: a single rule under
// test, an observed InferInput, and the exact derived edges it must emit. Each
// expected edge carries Origin="rule" and Producer=<rule name> because the
// engine stamps them; the rule tests run through the engine to exercise that.
func TestRules_InputToDerivedEdges(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		in   InferInput
		want []edgeKey
		// wantEvidence maps an edge's (Src,Dst) to the raw-event ids the edge
		// must carry, asserting rules attach provenance from the observed facts.
		wantEvidence map[[2]string][]string
	}{
		{
			name: "env-var-dependency: container env value and secret ref resolve to datastores",
			rule: EnvVarDependencyRule{},
			in: InferInput{
				OrgID: 7,
				RunID: "run-1",
				Entities: []model.Entity{
					ent(model.KindWorkload, "wl:svc", "svc", map[string]any{
						"container_definitions": []map[string]any{
							{
								"environment": []map[string]any{
									{"name": "BUCKET", "value": "ds:bucket"},
								},
								"secrets": []map[string]any{
									{"name": "DB", "valueFrom": "ds:db"},
								},
							},
						},
					}),
					ent(model.KindDataStore, "ds:bucket", "bucket", nil),
					ent(model.KindDataStore, "ds:db", "db", nil),
				},
				Evidence: map[string][]string{
					"wl:svc":    {"ev-wl"},
					"ds:bucket": {"ev-bucket"},
					"ds:db":     {"ev-db"},
				},
			},
			want: []edgeKey{
				{Src: "wl:svc", Dst: "ds:bucket", Rel: model.RelDependsOn, Conf: 0.6, Origin: OriginRule, Producer: "env-var-dependency"},
				{Src: "wl:svc", Dst: "ds:db", Rel: model.RelDependsOn, Conf: 0.75, Origin: OriginRule, Producer: "env-var-dependency"},
			},
			wantEvidence: map[[2]string][]string{
				{"wl:svc", "ds:bucket"}: {"ev-wl", "ev-bucket"},
				{"wl:svc", "ds:db"}:     {"ev-wl", "ev-db"},
			},
		},
		{
			name: "env-var-dependency: value that resolves to a workload is not a datastore dependency",
			rule: EnvVarDependencyRule{},
			in: InferInput{
				Entities: []model.Entity{
					ent(model.KindWorkload, "wl:a", "a", map[string]any{
						"container_definitions": []map[string]any{
							{"environment": []map[string]any{{"name": "PEER", "value": "wl:b"}}},
						},
					}),
					ent(model.KindWorkload, "wl:b", "b", nil),
				},
			},
			want: nil,
		},
		{
			name: "event-source-mapping: consumer CALLS the resolved source",
			rule: EventSourceCallRule{},
			in: InferInput{
				Entities: []model.Entity{
					ent(model.KindWorkload, "wl:consumer", "consumer", map[string]any{
						"event_source_mappings": []map[string]any{
							{"event_source_arn": "ds:queue"},
						},
					}),
					ent(model.KindDataStore, "ds:queue", "queue", nil),
				},
				Evidence: map[string][]string{
					"wl:consumer": {"ev-consumer"},
					"ds:queue":    {"ev-queue"},
				},
			},
			want: []edgeKey{
				{Src: "wl:consumer", Dst: "ds:queue", Rel: model.RelCalls, Conf: 0.7, Origin: OriginRule, Producer: "event-source-mapping-call"},
			},
			wantEvidence: map[[2]string][]string{
				{"wl:consumer", "ds:queue"}: {"ev-consumer", "ev-queue"},
			},
		},
		{
			name: "repo-build-provenance: repo BUILT_FROM workload, RUNS_IMAGE container image",
			rule: RepoBuildRule{},
			in: InferInput{
				Entities: []model.Entity{
					ent(model.KindWorkload, "wl:svc", "svc", map[string]any{
						"source_repository": "repo:app",
					}),
					ent(model.KindContainerImage, "img:svc", "svc-image", map[string]any{
						"repository_url": "repo:app",
					}),
					ent(model.KindRepository, "repo:app", "app", nil),
				},
				Evidence: map[string][]string{
					"wl:svc":   {"ev-wl"},
					"img:svc":  {"ev-img"},
					"repo:app": {"ev-repo"},
				},
			},
			want: []edgeKey{
				{Src: "repo:app", Dst: "img:svc", Rel: model.RelRunsImage, Conf: 0.65, Origin: OriginRule, Producer: "repo-build-provenance"},
				{Src: "repo:app", Dst: "wl:svc", Rel: model.RelBuiltFrom, Conf: 0.65, Origin: OriginRule, Producer: "repo-build-provenance"},
			},
			wantEvidence: map[[2]string][]string{
				{"repo:app", "wl:svc"}:  {"ev-repo", "ev-wl"},
				{"repo:app", "img:svc"}: {"ev-repo", "ev-img"},
			},
		},
		{
			name: "execution-role-identity: workload AUTHENTICATES_AS identity from execution_role_arn",
			rule: ExecutionRoleRule{},
			in: InferInput{
				Entities: []model.Entity{
					ent(model.KindWorkload, "wl:svc", "svc", map[string]any{
						"execution_role_arn": "id:task-role",
					}),
					ent(model.KindIdentity, "id:task-role", "task-role", nil),
				},
				Evidence: map[string][]string{
					"wl:svc":       {"ev-wl"},
					"id:task-role": {"ev-role"},
				},
			},
			want: []edgeKey{
				{Src: "wl:svc", Dst: "id:task-role", Rel: model.RelAuthenticatesAs, Conf: 0.8, Origin: OriginRule, Producer: "execution-role-identity"},
			},
			wantEvidence: map[[2]string][]string{
				{"wl:svc", "id:task-role"}: {"ev-wl", "ev-role"},
			},
		},
		{
			name: "monitor-config-uses: monitor USES monitored resources (list + single ref)",
			rule: MonitorConfigRule{},
			in: InferInput{
				Entities: []model.Entity{
					withProvider(ent(model.KindExternalService, "mon:dd", "dd-monitor", map[string]any{
						"monitored_resources": []map[string]any{
							{"arn": "ds:table"},
						},
						"target": "wl:svc",
					}), "datadog"),
					ent(model.KindDataStore, "ds:table", "table", nil),
					ent(model.KindWorkload, "wl:svc", "svc", nil),
				},
				Evidence: map[string][]string{
					"mon:dd":   {"ev-mon"},
					"ds:table": {"ev-table"},
					"wl:svc":   {"ev-wl"},
				},
			},
			want: []edgeKey{
				{Src: "mon:dd", Dst: "ds:table", Rel: model.RelUses, Conf: 0.7, Origin: OriginRule, Producer: "monitor-config-uses"},
				{Src: "mon:dd", Dst: "wl:svc", Rel: model.RelUses, Conf: 0.7, Origin: OriginRule, Producer: "monitor-config-uses"},
			},
			wantEvidence: map[[2]string][]string{
				{"mon:dd", "ds:table"}: {"ev-mon", "ev-table"},
				{"mon:dd", "wl:svc"}:   {"ev-mon", "ev-wl"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Run the single rule through the engine so Origin/Producer stamping
			// is exercised exactly as production does.
			eng := NewEngine(tc.rule)
			got, err := eng.Infer(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("Infer returned error: %v", err)
			}

			gotKeys := keysOf(got)
			wantKeys := append([]edgeKey(nil), tc.want...)
			sort.Slice(wantKeys, func(i, j int) bool {
				if wantKeys[i].Src != wantKeys[j].Src {
					return wantKeys[i].Src < wantKeys[j].Src
				}
				if wantKeys[i].Dst != wantKeys[j].Dst {
					return wantKeys[i].Dst < wantKeys[j].Dst
				}
				return wantKeys[i].Rel < wantKeys[j].Rel
			})
			if len(gotKeys) != len(wantKeys) || (len(wantKeys) > 0 && !reflect.DeepEqual(gotKeys, wantKeys)) {
				t.Fatalf("edges mismatch\n got: %+v\nwant: %+v", gotKeys, wantKeys)
			}

			// Every emitted edge must carry the rule's name as Producer and the
			// rule origin — the derived-lane provenance stamped by the engine.
			for _, e := range got {
				if e.Origin != OriginRule {
					t.Errorf("edge %s->%s Origin=%q, want %q", e.SrcID, e.DstID, e.Origin, OriginRule)
				}
				if e.Producer != tc.rule.Name() {
					t.Errorf("edge %s->%s Producer=%q, want %q", e.SrcID, e.DstID, e.Producer, tc.rule.Name())
				}
			}

			// Evidence provenance: each edge carries the raw-event ids from both
			// endpoints (merged, de-duplicated, first-seen order).
			for _, e := range got {
				want, ok := tc.wantEvidence[[2]string{e.SrcID, e.DstID}]
				if !ok {
					continue
				}
				if !reflect.DeepEqual(e.Evidence, want) {
					t.Errorf("edge %s->%s evidence=%v, want %v", e.SrcID, e.DstID, e.Evidence, want)
				}
			}
		})
	}
}

// TestDefaultRules_OrderAndNames pins the five rule families and their stable
// order, which the design requires for a deterministic candidate list.
func TestDefaultRules_OrderAndNames(t *testing.T) {
	got := DefaultRules()
	wantNames := []string{
		"env-var-dependency",
		"event-source-mapping-call",
		"repo-build-provenance",
		"execution-role-identity",
		"monitor-config-uses",
	}
	if len(got) != len(wantNames) {
		t.Fatalf("DefaultRules returned %d rules, want %d", len(got), len(wantNames))
	}
	for i, r := range got {
		if r.Name() != wantNames[i] {
			t.Errorf("rule[%d].Name()=%q, want %q", i, r.Name(), wantNames[i])
		}
	}
}

// ---- Property 2: facts/derived separation ----

// TestProperty2_InputCarriesNoDerivedLaneField asserts structurally that the
// engine's only input, InferInput, exposes nothing from the derived lane. Its
// fields are exactly the observed/normalized facts (org/run scope, normalized
// entities and edges, and raw-event evidence). There is no field typed as
// InferredEdge or any derived classification, so a rule cannot read derived
// output even in principle (Requirement 4.3).
func TestProperty2_InputCarriesNoDerivedLaneField(t *testing.T) {
	allowed := map[string]reflect.Type{
		"OrgID":    reflect.TypeOf(int64(0)),
		"RunID":    reflect.TypeOf(""),
		"Entities": reflect.TypeOf([]model.Entity(nil)),
		"Edges":    reflect.TypeOf([]model.Edge(nil)),
		"Evidence": reflect.TypeOf(map[string][]string(nil)),
	}

	ty := reflect.TypeOf(InferInput{})
	if ty.NumField() != len(allowed) {
		t.Fatalf("InferInput has %d fields, want %d — a new field may leak the derived lane into rule input", ty.NumField(), len(allowed))
	}

	inferredEdgeType := reflect.TypeOf(InferredEdge{})
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		wantType, ok := allowed[f.Name]
		if !ok {
			t.Fatalf("InferInput has unexpected field %q (%s): rule input must be observed facts only", f.Name, f.Type)
		}
		if f.Type != wantType {
			t.Errorf("InferInput.%s is %s, want %s", f.Name, f.Type, wantType)
		}
		// No field may carry derived output (InferredEdge) in any form.
		if fieldReferencesType(f.Type, inferredEdgeType) {
			t.Errorf("InferInput.%s references InferredEdge: rules must not read derived output", f.Name)
		}
	}
}

// fieldReferencesType reports whether t is or structurally contains target
// (through slice/array/map/pointer element types), used to prove InferInput
// never carries derived output.
func fieldReferencesType(t, target reflect.Type) bool {
	if t == target {
		return true
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Ptr:
		return fieldReferencesType(t.Elem(), target)
	case reflect.Map:
		return fieldReferencesType(t.Key(), target) || fieldReferencesType(t.Elem(), target)
	default:
		return false
	}
}

// TestProperty2_OutputIsDerivedLaneOnly asserts behaviorally that the engine's
// output is destined exclusively for the derived lane: it emits InferredEdge
// values (the derived-lane shape) and produces nothing of the observed-lane
// shape. An InferredEdge is intentionally NOT a model.Edge (the observed fact
// type), so engine output cannot be appended to observed facts by type, and
// every emitted edge is stamped with the derived origin (Requirements 4.4,
// 8.1).
func TestProperty2_OutputIsDerivedLaneOnly(t *testing.T) {
	// A rich observed input that triggers every rule family at once.
	in := InferInput{
		OrgID: 42,
		RunID: "run-sep",
		Entities: []model.Entity{
			ent(model.KindWorkload, "wl:svc", "svc", map[string]any{
				"container_definitions": []map[string]any{
					{"environment": []map[string]any{{"name": "BUCKET", "value": "ds:bucket"}}},
				},
				"event_source_mappings": []map[string]any{{"event_source_arn": "ds:queue"}},
				"source_repository":     "repo:app",
				"execution_role_arn":    "id:role",
			}),
			ent(model.KindDataStore, "ds:bucket", "bucket", nil),
			ent(model.KindDataStore, "ds:queue", "queue", nil),
			ent(model.KindRepository, "repo:app", "app", nil),
			ent(model.KindIdentity, "id:role", "role", nil),
			withProvider(ent(model.KindExternalService, "mon:dd", "dd", map[string]any{
				"service": "monitor",
				"target":  "wl:svc",
			}), "datadog"),
		},
	}

	eng := NewEngine(DefaultRules()...)
	got, err := eng.Infer(context.Background(), in)
	if err != nil {
		t.Fatalf("Infer returned error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected inferred edges from the combined input, got none")
	}

	// Structural: engine output is []InferredEdge, which is a distinct type from
	// model.Edge (the observed_events fact shape). This guarantees the engine
	// cannot write to the observed lane by returning observed facts.
	if reflect.TypeOf(got) == reflect.TypeOf([]model.Edge(nil)) {
		t.Fatal("engine output is []model.Edge: derived output must not be an observed-lane fact type")
	}
	if reflect.TypeOf(InferredEdge{}) == reflect.TypeOf(model.Edge{}) {
		t.Fatal("InferredEdge is identical to model.Edge: the derived lane must be a distinct shape")
	}

	// Behavioral: every edge is a well-formed derived candidate — stamped with
	// the derived origin and a producer, carrying confidence and reusing an
	// existing model relation (never inventing a relation kind).
	for _, e := range got {
		if e.Origin != OriginRule {
			t.Errorf("edge %s->%s Origin=%q, want derived-lane origin %q", e.SrcID, e.DstID, e.Origin, OriginRule)
		}
		if e.Producer == "" {
			t.Errorf("edge %s->%s has empty Producer; derived output must name its producer", e.SrcID, e.DstID)
		}
		if e.Confidence <= 0 || e.Confidence > 1 {
			t.Errorf("edge %s->%s Confidence=%v, want in (0,1]", e.SrcID, e.DstID, e.Confidence)
		}
		if !model.ValidRelation(e.Relation) {
			t.Errorf("edge %s->%s Relation=%q is not an existing model relation", e.SrcID, e.DstID, e.Relation)
		}
	}
}

// TestProperty2_RulesIgnoreObservedEdgesAndDerivedShapedInput proves rules read
// only observed facts and never derived output. We feed observed EDGES whose
// relations coincide with what rules emit (DEPENDS_ON, CALLS, ...) and whose
// attributes mimic a derived classification; the engine must ignore all of it,
// because rules key off entity attributes/evidence (observed facts), not off
// pre-existing edges or any derived signal. With no triggering attributes
// present, the engine yields no edges.
func TestProperty2_RulesIgnoreObservedEdgesAndDerivedShapedInput(t *testing.T) {
	in := InferInput{
		OrgID: 1,
		RunID: "run-ignore",
		Entities: []model.Entity{
			// Plain entities with no rule-triggering attributes.
			ent(model.KindWorkload, "wl:svc", "svc", map[string]any{
				// Attributes shaped like derived output must not be treated as a
				// signal: rules never read a "derived"/"inferred" lane.
				"inferred":   true,
				"data_class": "pii",
			}),
			ent(model.KindDataStore, "ds:db", "db", nil),
			ent(model.KindIdentity, "id:role", "role", nil),
		},
		// Observed edges that already assert relations rules can emit. A rule
		// must not consume these as input; it only derives from entity
		// attributes/evidence. (Also demonstrates InferInput can carry edges yet
		// rules remain attribute-driven.)
		Edges: []model.Edge{
			{Relation: model.RelDependsOn, SrcCanonicalID: "wl:svc", DstCanonicalID: "ds:db", Confidence: 1.0},
			{Relation: model.RelAuthenticatesAs, SrcCanonicalID: "wl:svc", DstCanonicalID: "id:role", Confidence: 1.0},
		},
	}

	eng := NewEngine(DefaultRules()...)
	got, err := eng.Infer(context.Background(), in)
	if err != nil {
		t.Fatalf("Infer returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("engine emitted %d edges from input with no triggering facts; rules must not read observed edges or derived-shaped attributes: %+v", len(got), keysOf(got))
	}
}

// TestInputFromResult_BridgesObservedFactsOnly asserts the single bridge from
// the normalized stage into the engine carries exactly the observed Result and
// evidence — the only path into InferInput, reinforcing that rules start from
// facts (Requirement 4.3).
func TestInputFromResult_BridgesObservedFactsOnly(t *testing.T) {
	res := normalize.Result{
		Entities: []model.Entity{ent(model.KindWorkload, "wl:svc", "svc", nil)},
		Edges:    []model.Edge{{Relation: model.RelContains, SrcCanonicalID: "a", DstCanonicalID: "b"}},
	}
	evidence := map[string][]string{"wl:svc": {"ev-1"}}

	in := InputFromResult(99, "run-bridge", res, evidence)

	if in.OrgID != 99 || in.RunID != "run-bridge" {
		t.Errorf("scope mismatch: got org=%d run=%q", in.OrgID, in.RunID)
	}
	if !reflect.DeepEqual(in.Entities, res.Entities) {
		t.Errorf("entities not carried through from the observed Result")
	}
	if !reflect.DeepEqual(in.Edges, res.Edges) {
		t.Errorf("edges not carried through from the observed Result")
	}
	if !reflect.DeepEqual(in.Evidence, evidence) {
		t.Errorf("evidence not carried through")
	}
}
