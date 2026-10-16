// Package contract defines the scenario.yaml schema and a validating loader for
// the Bellu Cloud Lab. A scenario contract declares the Bellu graph a scenario
// is expected to produce (expected_nodes, expected_edges, expected_attack_paths)
// and is validated against Bellu's own allow-lists (internal/model) BEFORE any
// cloud resources are provisioned.
//
// The loader is a pure, read-only consumer of internal/model: it never modifies
// Bellu's model, normalizers, or collectors.
package contract

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// Scenario is the decoded scenario.yaml contract.
type Scenario struct {
	// Name is the scenario identifier, "<provider>/<dir>" (e.g. aws/public-ec2).
	Name string `yaml:"name"`
	// ExpectedNodes are the graph nodes the scenario must produce.
	ExpectedNodes []NodeExpect `yaml:"expected_nodes"`
	// ExpectedEdges are the graph edges the scenario must produce.
	ExpectedEdges []EdgeExpect `yaml:"expected_edges"`
	// ExpectedAttackPaths are the attack-path chains plus their risk outcome.
	ExpectedAttackPaths []AttackPath `yaml:"expected_attack_paths"`

	// present tracks which top-level sections appeared in the YAML, so Validate
	// can report a *missing* section distinctly from an empty one (Req 3.9).
	present sectionPresence `yaml:"-"`
}

// NodeExpect is one entry in expected_nodes.
type NodeExpect struct {
	Kind       string         `yaml:"kind"`
	Name       string         `yaml:"name"`
	Attributes map[string]any `yaml:"attributes,omitempty"`
}

// EdgeExpect is one entry in expected_edges (and in an attack path's edges).
// From/To match graph nodes by Kind or Name; the comparator decides which.
type EdgeExpect struct {
	Relation      string  `yaml:"relation"`
	From          string  `yaml:"from"`
	To            string  `yaml:"to"`
	MinConfidence float64 `yaml:"min_confidence,omitempty"`
}

// AttackPath is a chain of node references with the edges connecting them and
// an optional reachability-driven risk outcome.
type AttackPath struct {
	Nodes       []string     `yaml:"nodes"`
	Edges       []EdgeExpect  `yaml:"edges"`
	RiskOutcome *RiskExpect   `yaml:"risk_outcome,omitempty"`
}

// RiskExpect declares the expected reachability-driven risk relationship: the
// reachable score should be ReachableMultiplier times the unreachable score,
// after clamping to [Clamp[0], Clamp[1]].
type RiskExpect struct {
	ReachableMultiplier float64    `yaml:"reachable_multiplier"`
	Clamp               [2]float64 `yaml:"clamp"`
}

// sectionPresence records which optional-looking-but-required top-level
// sections were actually present in the YAML document.
type sectionPresence struct {
	nodes bool
	edges bool
	paths bool
}

// Load reads and decodes a scenario.yaml at path, then validates it against the
// Bellu model allow-lists. A contract that references an unknown Kind/Relation,
// or omits a required section, fails here — before any provisioning.
func Load(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("contract: read %s: %w", path, err)
	}

	// First decode into a presence probe so we can distinguish a missing
	// section from a present-but-empty one.
	var probe map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return Scenario{}, fmt.Errorf("contract: parse %s: %w", path, err)
	}

	var s Scenario
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return Scenario{}, fmt.Errorf("contract: decode %s: %w", path, err)
	}
	_, s.present.nodes = probe["expected_nodes"]
	_, s.present.edges = probe["expected_edges"]
	_, s.present.paths = probe["expected_attack_paths"]

	if err := s.Validate(); err != nil {
		return Scenario{}, err
	}
	return s, nil
}

// Validate enforces the contract invariants (Requirement 3):
//   - all three sections present (report the missing one),
//   - every expected_nodes Kind is in model.AllKinds,
//   - every expected_edges Relation is in model.AllRelations,
//   - every attack-path node Kind and edge Relation is in the allow-lists.
//
// On any unknown token, Validate reports EVERY unknown token together with the
// entry that referenced it and marks the whole contract invalid (no partial
// acceptance, Requirement 3.8).
func (s Scenario) Validate() error {
	var missing []string
	if !s.present.nodes {
		missing = append(missing, "expected_nodes")
	}
	if !s.present.edges {
		missing = append(missing, "expected_edges")
	}
	if !s.present.paths {
		missing = append(missing, "expected_attack_paths")
	}
	if len(missing) > 0 {
		return fmt.Errorf("contract %q: missing required section(s): %s",
			s.Name, strings.Join(missing, ", "))
	}

	var problems []string

	checkKind := func(kind, where string) {
		if !model.ValidKind(model.Kind(kind)) {
			problems = append(problems, fmt.Sprintf("unknown Kind %q (in %s)", kind, where))
		}
	}
	checkRelation := func(rel, where string) {
		if !model.ValidRelation(model.Relation(rel)) {
			problems = append(problems, fmt.Sprintf("unknown Relation %q (in %s)", rel, where))
		}
	}

	for i, n := range s.ExpectedNodes {
		checkKind(n.Kind, fmt.Sprintf("expected_nodes[%d] name=%q", i, n.Name))
	}
	for i, e := range s.ExpectedEdges {
		checkRelation(e.Relation, fmt.Sprintf("expected_edges[%d] %s->%s", i, e.From, e.To))
	}
	for i, p := range s.ExpectedAttackPaths {
		for j, e := range p.Edges {
			checkRelation(e.Relation, fmt.Sprintf("expected_attack_paths[%d].edges[%d] %s->%s", i, j, e.From, e.To))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("contract %q is invalid:\n  - %s",
			s.Name, strings.Join(problems, "\n  - "))
	}
	return nil
}
