package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadPhase1Scenarios loads the real Phase 1 scenario contracts and asserts
// they validate against the live Bellu model allow-lists.
func TestLoadPhase1Scenarios(t *testing.T) {
	// repo root is four levels up from this package dir.
	root := filepath.Join("..", "..", "aws", "scenarios")
	for _, name := range []string{"public-ec2", "iam-to-s3"} {
		path := filepath.Join(root, name, "scenario.yaml")
		sc, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%s): %v", path, err)
		}
		if sc.Name != "aws/"+name {
			t.Errorf("%s: name = %q, want aws/%s", path, sc.Name, name)
		}
		if len(sc.ExpectedNodes) == 0 || len(sc.ExpectedEdges) == 0 {
			t.Errorf("%s: expected non-empty nodes and edges", path)
		}
	}
}

// TestValidateRejectsUnknownKind confirms an unknown Kind fails validation and
// the error names the bad token (Requirement 3.8).
func TestValidateRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.yaml")
	body := `name: aws/bad
expected_nodes:
  - { kind: NotAKind, name: x }
expected_edges: []
expected_attack_paths: []
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for unknown Kind, got nil")
	}
	if !strings.Contains(err.Error(), "NotAKind") {
		t.Errorf("error should name the unknown token, got: %v", err)
	}
}

// TestValidateReportsMissingSection confirms a missing section is reported by
// name (Requirement 3.9).
func TestValidateReportsMissingSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.yaml")
	body := `name: aws/bad
expected_nodes: []
expected_attack_paths: []
` // expected_edges omitted
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected missing-section error, got nil")
	}
	if !strings.Contains(err.Error(), "expected_edges") {
		t.Errorf("error should name the missing section, got: %v", err)
	}
}
