package atlas

import (
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

// TestParseYAMLFlatAndMatrix confirms both the flat "techniques" layout and the
// matrix-nested layout decode, and that output is id-sorted for determinism.
func TestParseYAMLFlatAndMatrix(t *testing.T) {
	payload := []byte(`
matrices:
  - id: ATLAS
    name: ATLAS Matrix
    techniques:
      - id: AML.T0051
        name: LLM Prompt Injection
        affected_models: ["meta-llama/Llama-3-8B"]
techniques:
  - id: AML.T0043
    name: Craft Adversarial Data
    references: ["CVE-2024-1234"]
`)
	recs, err := Parser{}.Parse(raw.RawRef{}, payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 techniques, got %d", len(recs))
	}
	// Sorted by id: AML.T0043 before AML.T0051.
	if recs[0].SourceKey() != "AML.T0043" || recs[1].SourceKey() != "AML.T0051" {
		t.Errorf("expected id-sorted output, got %q, %q", recs[0].SourceKey(), recs[1].SourceKey())
	}
	// References are carried as aliases alongside the id.
	wantAliases := []string{"AML.T0043", "CVE-2024-1234"}
	if !reflect.DeepEqual(recs[0].Aliases(), wantAliases) {
		t.Errorf("aliases = %v, want %v", recs[0].Aliases(), wantAliases)
	}
}

// TestParseJSONAccepted confirms a JSON mirror (JSON is valid YAML) decodes.
func TestParseJSONAccepted(t *testing.T) {
	payload := []byte(`{"techniques":[{"id":"AML.T0051","name":"Prompt Injection"}]}`)
	recs, err := Parser{}.Parse(raw.RawRef{}, payload)
	if err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if len(recs) != 1 || recs[0].SourceKey() != "AML.T0051" {
		t.Fatalf("unexpected json decode: %+v", recs)
	}
}

// TestParseDeterministic confirms identical bytes yield identical records.
func TestParseDeterministic(t *testing.T) {
	payload := []byte(`{"techniques":[{"id":"AML.T0051"},{"id":"AML.T0043"}]}`)
	a, err := Parser{}.Parse(raw.RawRef{}, payload)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parser{}.Parse(raw.RawRef{}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("parse not deterministic:\n%+v\n%+v", a, b)
	}
}

// TestParseRejectsMissingID confirms a technique without an id is rejected.
func TestParseRejectsMissingID(t *testing.T) {
	if _, err := (Parser{}).Parse(raw.RawRef{}, []byte(`{"techniques":[{"name":"x"}]}`)); err == nil {
		t.Fatal("expected rejection for technique missing id")
	}
}
