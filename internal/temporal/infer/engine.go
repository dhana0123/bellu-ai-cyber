// Package infer implements the inference engine of the derived lane. It reads
// only observed/normalized facts (entity and edge attributes, evidence
// linkage) and produces candidate inferred edges destined EXCLUSIVELY for the
// derived_events log. It never reads derived output and never writes the
// observed_events facts log, preserving the hard facts/derived separation.
//
// Rules are the only unit of extensibility: a provider adds a Rule, it does
// not add a branch to the engine. The engine therefore never switches on
// provider type — it simply runs every registered rule over the same observed
// input and collects their candidate edges.
package infer

import (
	"context"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
)

// OriginRule is the Origin value every rule-produced edge carries. A later
// model stage would use a different origin while keeping the same shape.
const OriginRule = "rule"

// Rule maps observed facts to candidate inferred edges. Each rule reads only
// observed_events / normalized attributes — never derived output — keeping the
// facts/inference separation. Providers add rules; the engine never branches on
// provider type.
type Rule interface {
	// Name identifies the rule, e.g. "env-var-dependency". It becomes the
	// Producer of every edge the rule emits.
	Name() string
	// Infer reads the observed input and returns candidate inferred edges. It
	// must not read or depend on derived output. Returning an error aborts only
	// this rule's contribution; the engine isolates rule failures.
	Infer(ctx context.Context, in InferInput) ([]InferredEdge, error)
}

// InferInput is the observed/normalized view a rule reads from. It carries the
// run's normalized entities and edges (the same Result the change engine
// classified) plus the per-element evidence linkage, and nothing from the
// derived lane. Rules key off entity/edge Attributes and evidence; they never
// receive inferred edges or classifications.
type InferInput struct {
	// OrgID is the tenant scope of the run.
	OrgID int64
	// RunID is the collection run that produced these observed facts.
	RunID string
	// Entities are the run's normalized entities (observed facts only).
	Entities []model.Entity
	// Edges are the run's normalized edges (observed facts only).
	Edges []model.Edge
	// Evidence maps an element identity (entity canonical id, or an edge
	// identity) to the raw-event ids that justify it, so a rule can attach
	// provenance to the edges it emits.
	Evidence map[string][]string
}

// InferredEdge is a candidate edge produced by a rule for the derived lane. It
// is destined exclusively for derived_events; it never enters observed_events.
type InferredEdge struct {
	// SrcID is the source entity canonical id.
	SrcID string
	// Relation reuses an existing model relation (BUILT_FROM, RUNS_IMAGE,
	// AUTHENTICATES_AS, CALLS, DEPENDS_ON, USES, ...). Rules never invent new
	// relation kinds.
	Relation model.Relation
	// DstID is the destination entity canonical id.
	DstID string
	// Confidence in [0,1] expresses how strongly the signal implies the edge.
	Confidence float64
	// Evidence is the raw-event ids backing the inference.
	Evidence []string
	// Origin is the derivation stage, e.g. "rule" (OriginRule).
	Origin string
	// Producer is the specific rule (or model id) that emitted the edge, set to
	// the rule's Name by the engine.
	Producer string
}

// Engine runs a set of registered rules over observed input and collects their
// candidate inferred edges. It holds no provider-specific logic: adding a
// provider means registering more rules, never editing the engine.
//
// The engine deliberately returns the candidate edges rather than writing them
// itself; persistence into derived_events (and projection) is the caller's
// step, keeping this type a pure fact→candidate transform that cannot touch the
// observed lane.
type Engine struct {
	rules []Rule
}

// NewEngine builds an engine with the given rules, in order. The order is
// preserved so a deterministic run produces a deterministic candidate list.
func NewEngine(rules ...Rule) *Engine {
	e := &Engine{}
	e.Register(rules...)
	return e
}

// Register appends rules to the engine. Nil rules are ignored. Rules are the
// only extension point; there is intentionally no provider parameter.
func (e *Engine) Register(rules ...Rule) {
	for _, r := range rules {
		if r != nil {
			e.rules = append(e.rules, r)
		}
	}
}

// Rules returns the registered rules in registration order.
func (e *Engine) Rules() []Rule {
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

// Infer runs every registered rule over the observed input and returns the
// union of their candidate inferred edges, stamping each edge's Origin and
// Producer. It reads only observed/normalized data (via in) and produces edges
// destined solely for the derived lane; it never branches on provider type and
// never consults derived output.
//
// A rule returning an error aborts the whole run with a wrapped error naming
// the rule, so a misbehaving rule is visible rather than silently dropped.
func (e *Engine) Infer(ctx context.Context, in InferInput) ([]InferredEdge, error) {
	var out []InferredEdge
	for _, r := range e.rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		edges, err := r.Infer(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("infer: rule %q: %w", r.Name(), err)
		}
		for _, edge := range edges {
			if edge.Origin == "" {
				edge.Origin = OriginRule
			}
			if edge.Producer == "" {
				edge.Producer = r.Name()
			}
			out = append(out, edge)
		}
	}
	return out, nil
}

// InputFromResult builds an InferInput from a run's normalized Result and
// evidence linkage. It is the single bridge from the observed/normalized stage
// into the inference engine, making explicit that rules read only facts (the
// Result) and never the derived lane.
func InputFromResult(org int64, runID string, res normalize.Result, evidence map[string][]string) InferInput {
	return InferInput{
		OrgID:    org,
		RunID:    runID,
		Entities: res.Entities,
		Edges:    res.Edges,
		Evidence: evidence,
	}
}
