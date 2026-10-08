package mcp

import (
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
)

// TestParseAdvisories confirms advisories decode, are id-sorted for
// determinism, and carry id + references as aliases.
func TestParseAdvisories(t *testing.T) {
	payload := []byte(`{"advisories":[
		{"id":"MCP-2025-0002","summary":"b","affected_servers":["github"],"references":["CVE-2025-2"]},
		{"id":"MCP-2025-0001","summary":"a","affected_servers":["filesystem"]}
	]}`)
	recs, err := Parser{}.Parse(raw.RawRef{}, payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 advisories, got %d", len(recs))
	}
	if recs[0].SourceKey() != "MCP-2025-0001" || recs[1].SourceKey() != "MCP-2025-0002" {
		t.Errorf("expected id-sorted output, got %q, %q", recs[0].SourceKey(), recs[1].SourceKey())
	}
	wantAliases := []string{"MCP-2025-0002", "CVE-2025-2"}
	if !reflect.DeepEqual(recs[1].Aliases(), wantAliases) {
		t.Errorf("aliases = %v, want %v", recs[1].Aliases(), wantAliases)
	}
}

// TestParseDeterministic confirms identical bytes yield identical records.
func TestParseDeterministic(t *testing.T) {
	payload := []byte(`{"advisories":[{"id":"MCP-2025-0001"},{"id":"MCP-2025-0002"}]}`)
	a, err := Parser{}.Parse(raw.RawRef{}, payload)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parser{}.Parse(raw.RawRef{}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("parse not deterministic")
	}
}

// TestParseRejectsMissingID confirms an advisory without an id is rejected.
func TestParseRejectsMissingID(t *testing.T) {
	if _, err := (Parser{}).Parse(raw.RawRef{}, []byte(`{"advisories":[{"summary":"x"}]}`)); err == nil {
		t.Fatal("expected rejection for advisory missing id")
	}
}
