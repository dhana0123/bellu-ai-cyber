// Package classify is the derived-lane classification engine. It labels each
// DataStore by its likely content (its data class) and derives the identity
// reader/writer sets that can touch it — WITHOUT ever reading the datastore's
// stored contents. Classification is produced from metadata only: naming
// conventions, infrastructure-as-code definitions carried in attributes, and
// IAM policy (CAN_READ/CAN_WRITE/CAN_ADMIN edges) derived during normalization.
//
// Two stages share one output shape. Stage A (this file) is rule-based with
// Origin()=="rule". Stage B is a model-refined classifier that produces the
// SAME DataClass shape with Origin()=="model", so the derived lane, the
// projection, and the Memgraph node properties are stage-agnostic: adopting the
// model stage needs no schema or projection change.
//
// Classifications are written to the derived_events lane (kind="classification",
// change_type="classified") by the engine/projection wiring in later tasks;
// this package produces the DataClass values that write path consumes.
package classify

import (
	"context"
	"sort"
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// Data-class labels. The vocabulary is intentionally small and provider-neutral;
// a store resolves to exactly one class, falling back to ClassInternal when no
// rule matches so every DataStore is always classified.
const (
	ClassPII      = "pii"
	ClassSecrets  = "secrets"
	ClassLogs     = "logs"
	ClassBackups  = "backups"
	ClassPublic   = "public"
	ClassInternal = "internal"
)

// originRule is the Origin() value for the Stage A rule-based classifier. Stage
// B reuses the DataClass shape with origin "model".
const originRule = "rule"

// Classifier labels a DataStore by likely content and reader/writer sets
// WITHOUT reading stored data. Stage A (rules) and Stage B (model) produce the
// SAME DataClass shape, so the derived lane and Memgraph props are stage-agnostic.
type Classifier interface {
	// Origin reports the producing stage: "rule" for Stage A, "model" for Stage B.
	Origin() string
	// Classify labels every DataStore in the input from metadata only and
	// returns one DataClass per store. It never reads datastore contents.
	Classify(ctx context.Context, in ClassifyInput) ([]DataClass, error)
}

// ClassifyInput is the metadata a classifier may read. It carries only the
// normalized run data (entities and the IAM/access edges between them); it does
// NOT carry datastore contents, and the classifier must never read any such
// contents. This mirrors the inference engine's contract: derive from observed
// or normalized data only, never from derived output.
type ClassifyInput struct {
	// Entities are the normalized nodes for the run. The classifier inspects
	// DataStore entities (name, provider, Attributes) and resolves reader/writer
	// identities by following access edges to Identity entities.
	Entities []model.Entity
	// Edges are the normalized relationships for the run. Access edges
	// (CAN_READ/CAN_WRITE/CAN_ADMIN) between an Identity and a DataStore derive
	// the reader and writer sets.
	Edges []model.Edge
}

// DataClass is one classification result for a DataStore. Stage A and Stage B
// both produce this shape, so the write path and the projection are the same
// regardless of which stage produced it.
type DataClass struct {
	// CanonicalID is the classified DataStore.
	CanonicalID string
	// Class is the likely-content label (see the Class* constants).
	Class string
	// Readers are the identity canonical_ids granted read access.
	Readers []string
	// Writers are the identity canonical_ids granted write access.
	Writers []string
	// Confidence is the classifier's confidence in [0,1].
	Confidence float64
	// Evidence are the raw-event ids that justify this classification.
	Evidence []string
	// ClassifiedBy is the producer id, surfaced as the Memgraph classified_by
	// node property (e.g. "rule:classify-stage-a").
	ClassifiedBy string
}

// RuleClassifier is the Stage A rule-based classifier. It derives a DataStore's
// class from naming conventions and IaC/attribute hints, and its reader/writer
// sets from IAM access edges — all from metadata, never from stored contents.
type RuleClassifier struct {
	// producer is reported as DataClass.ClassifiedBy.
	producer string
}

// NewRuleClassifier constructs the Stage A rule-based classifier.
func NewRuleClassifier() *RuleClassifier {
	return &RuleClassifier{producer: "rule:classify-stage-a"}
}

// Origin reports the Stage A origin "rule".
func (c *RuleClassifier) Origin() string { return originRule }

// Classify labels every DataStore entity in the input. For each store it:
//   - derives read/write identities from CAN_READ/CAN_WRITE/CAN_ADMIN edges
//     targeting the store (IAM policy, metadata only);
//   - picks a class from naming conventions and IaC/attribute hints;
//   - falls back to ClassInternal with low confidence when no rule matches.
//
// It never reads datastore contents — only entity/edge metadata.
func (c *RuleClassifier) Classify(ctx context.Context, in ClassifyInput) ([]DataClass, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	readers, writers := c.deriveAccess(in)

	out := make([]DataClass, 0)
	for _, e := range in.Entities {
		if e.Kind != model.KindDataStore {
			continue
		}
		id := e.CanonicalID
		if id == "" {
			continue
		}

		class, confidence := classifyStore(e)

		dc := DataClass{
			CanonicalID:  id,
			Class:        class,
			Readers:      sortedUnique(readers[id]),
			Writers:      sortedUnique(writers[id]),
			Confidence:   confidence,
			Evidence:     evidenceIDs(e.Evidence),
			ClassifiedBy: c.producer,
		}
		out = append(out, dc)
	}

	// Deterministic output order so replay and tests are stable.
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalID < out[j].CanonicalID })
	return out, nil
}

// deriveAccess walks the access edges and returns, per DataStore canonical_id,
// the identity canonical_ids with read and with write access. CAN_READ grants
// read; CAN_WRITE and CAN_ADMIN grant write (admin implies write); CAN_ADMIN
// also implies read. This is IAM-policy-derived metadata, not stored content.
func (c *RuleClassifier) deriveAccess(in ClassifyInput) (readers, writers map[string][]string) {
	// Only edges whose destination is a DataStore are relevant.
	stores := make(map[string]struct{}, len(in.Entities))
	for _, e := range in.Entities {
		if e.Kind == model.KindDataStore && e.CanonicalID != "" {
			stores[e.CanonicalID] = struct{}{}
		}
	}

	readers = make(map[string][]string)
	writers = make(map[string][]string)
	for _, ed := range in.Edges {
		dst := ed.DstCanonicalID
		src := ed.SrcCanonicalID
		if src == "" || dst == "" {
			continue
		}
		if _, ok := stores[dst]; !ok {
			continue
		}
		switch ed.Relation {
		case model.RelCanRead:
			readers[dst] = append(readers[dst], src)
		case model.RelCanWrite:
			writers[dst] = append(writers[dst], src)
		case model.RelCanAdmin:
			// Admin implies both read and write.
			readers[dst] = append(readers[dst], src)
			writers[dst] = append(writers[dst], src)
		}
	}
	return readers, writers
}

// classifyStore picks a class for one DataStore from naming conventions and
// IaC/attribute hints only. It returns the class and a confidence. When no rule
// matches it returns ClassInternal with a low confidence (the Unknown fallback),
// so every store is always classified without reading its contents.
func classifyStore(e model.Entity) (string, float64) {
	// Explicit IaC/attribute hints win over naming heuristics: a tag or an IaC
	// definition that declares the data class is a stronger signal than a name.
	if class, ok := classFromAttributes(e.Attributes); ok {
		return class, 0.9
	}

	// Public exposure is a lifecycle/exposure fact carried in attributes.
	if isPublic(e.Attributes) {
		return ClassPublic, 0.7
	}

	// Naming conventions: match the store name (and provider-neutral path hints)
	// against the class vocabulary.
	name := strings.ToLower(e.Name)
	if class, ok := classFromName(name); ok {
		return class, 0.6
	}

	// Unknown fallback: no rule matched. Default to internal with low confidence
	// so a later model stage (same shape) can refine it.
	return ClassInternal, 0.3
}

// classFromAttributes reads an explicit data-class hint from IaC-derived
// attributes (tags, classification labels). Returns ok=false when absent or
// when the declared value is not in the known vocabulary.
func classFromAttributes(attrs map[string]any) (string, bool) {
	if len(attrs) == 0 {
		return "", false
	}
	// Common attribute keys an IaC definition or tag set may carry.
	for _, key := range []string{"data_class", "data_classification", "classification", "sensitivity"} {
		if v, ok := attrs[key]; ok {
			if s, ok := v.(string); ok {
				if class, ok := normalizeClass(s); ok {
					return class, true
				}
			}
		}
	}
	return "", false
}

// classFromName derives a class from naming conventions. The checks are ordered
// most-specific first so, e.g., "pii" is not shadowed by a broader token.
func classFromName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	type rule struct {
		class  string
		tokens []string
	}
	// Ordered: the first matching rule wins (exactly-one classification).
	rules := []rule{
		{ClassSecrets, []string{"secret", "credential", "vault", "keystore", "key-store", "token", "password"}},
		{ClassPII, []string{"pii", "personal", "customer", "user-data", "userdata", "profile", "patient", "ssn"}},
		{ClassBackups, []string{"backup", "snapshot", "archive", "dump"}},
		{ClassLogs, []string{"log", "audit", "trace", "event", "telemetry", "metric"}},
		{ClassPublic, []string{"public", "static-assets", "cdn", "www", "assets"}},
	}
	for _, r := range rules {
		for _, tok := range r.tokens {
			if strings.Contains(name, tok) {
				return r.class, true
			}
		}
	}
	return "", false
}

// isPublic reports whether the store's exposure attributes mark it public. These
// are the same provider-neutral exposure keys the change engine treats as state.
func isPublic(attrs map[string]any) bool {
	if len(attrs) == 0 {
		return false
	}
	for _, key := range []string{"public", "public_access", "publicly_accessible", "exposed"} {
		if v, ok := attrs[key]; ok && truthy(v) {
			return true
		}
	}
	if v, ok := attrs["exposure"]; ok {
		if s, ok := v.(string); ok && strings.EqualFold(s, "public") {
			return true
		}
	}
	return false
}

// normalizeClass maps a declared/tag string to a known class label.
func normalizeClass(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ClassPII, "personal", "personal-data", "personally-identifiable":
		return ClassPII, true
	case ClassSecrets, "secret", "credentials", "credential":
		return ClassSecrets, true
	case ClassLogs, "log", "audit":
		return ClassLogs, true
	case ClassBackups, "backup", "snapshot":
		return ClassBackups, true
	case ClassPublic:
		return ClassPublic, true
	case ClassInternal, "private", "confidential":
		return ClassInternal, true
	default:
		return "", false
	}
}

// truthy interprets common boolean-ish attribute values.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "yes", "public", "enabled", "1":
			return true
		}
	}
	return false
}

// evidenceIDs extracts the raw-event ids from an entity's evidence slice.
func evidenceIDs(evs []model.Evidence) []string {
	if len(evs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(evs))
	for _, ev := range evs {
		if ev.RawEventID != "" {
			ids = append(ids, ev.RawEventID)
		}
	}
	return sortedUnique(ids)
}

// sortedUnique returns the input with duplicates removed, in sorted order, for
// deterministic reader/writer/evidence sets.
func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Ensure *RuleClassifier satisfies the Classifier interface at compile time.
var _ Classifier = (*RuleClassifier)(nil)
