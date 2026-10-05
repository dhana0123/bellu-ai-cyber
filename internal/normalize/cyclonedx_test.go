package normalize

import (
	"context"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
	cdxsrc "github.com/bellu/bellu-ai-cyber/internal/source/cyclonedx"
)

// loadBOM reads a fixture BOM through the cyclonedx source and returns its raw
// events, so normalizer tests run on real source output.
func loadBOM(t *testing.T, path string) []rawevent.RawEvent {
	t.Helper()
	s := cdxsrc.New(path)
	evc, errc := s.Collect(context.Background(), source.Run{RunID: "run-test"})
	var events []rawevent.RawEvent
	for evc != nil || errc != nil {
		select {
		case ev, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			events = append(events, ev)
		case err, ok := <-errc:
			if !ok {
				errc = nil
				continue
			}
			t.Fatalf("source error: %v", err)
		}
	}
	return events
}

func normalizeBOM(t *testing.T, path string) Result {
	t.Helper()
	res, err := CycloneDX{}.Normalize(loadBOM(t, path))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return res
}

func kindOf(res Result, key string) (model.Kind, bool) {
	for _, e := range res.Entities {
		if e.Key == key {
			return e.Kind, true
		}
	}
	return "", false
}

func hasEdge(res Result, rel model.Relation, src, dst string) bool {
	for _, e := range res.Edges {
		if e.Relation == rel && e.SrcKey == src && e.DstKey == dst {
			return true
		}
	}
	return false
}

func TestNormalizeKindMapping(t *testing.T) {
	cases := []struct {
		path string
		key  string
		want model.Kind
	}{
		{"../../testdata/sboms/syft-image.cdx.json", "sha256:aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa7777bbbb8888", model.KindContainerImage},
		{"../../testdata/sboms/syft-image.cdx.json", "pkg:pypi/langchain@0.2.1", model.KindPackage},
		{"../../testdata/sboms/cdxgen-aibom.cdx.json", "https://github.com/bellu/agent-api", model.KindRepository},
		{"../../testdata/sboms/cdxgen-aibom.cdx.json", "pkg:huggingface/meta-llama/Llama-3-8B@main", model.KindAIModel},
		{"../../testdata/sboms/cdxgen-aibom.cdx.json", "prompt:system-agent", model.KindPrompt},
		{"../../testdata/sboms/cdxgen-aibom.cdx.json", "service:mcp-filesystem", model.KindMCPServer},
		{"../../testdata/sboms/cdxgen-aibom.cdx.json", "service:openai", model.KindAIService},
	}
	for _, c := range cases {
		res := normalizeBOM(t, c.path)
		got, ok := kindOf(res, c.key)
		if !ok {
			t.Errorf("%s: entity %q not found", c.path, c.key)
			continue
		}
		if got != c.want {
			t.Errorf("%s: entity %q kind = %q, want %q", c.path, c.key, got, c.want)
		}
	}
}

func TestNormalizeDependsOnEdge(t *testing.T) {
	res := normalizeBOM(t, "../../testdata/sboms/syft-image.cdx.json")
	if !hasEdge(res, model.RelDependsOn, "pkg:pypi/langchain@0.2.1", "pkg:pypi/requests@2.31.0") {
		t.Fatal("expected DEPENDS_ON langchain -> requests")
	}
}

func TestNormalizeUsesModelEdge(t *testing.T) {
	res := normalizeBOM(t, "../../testdata/sboms/cdxgen-aibom.cdx.json")
	// repo depends on the model -> typed as USES_MODEL.
	if !hasEdge(res, model.RelUsesModel, "https://github.com/bellu/agent-api", "pkg:huggingface/meta-llama/Llama-3-8B@main") {
		t.Fatalf("expected USES_MODEL repo -> model; edges=%v", res.Edges)
	}
}

func TestNormalizeVulnEdge(t *testing.T) {
	res := normalizeBOM(t, "../../testdata/sboms/trivy-image.cdx.json")
	if !hasEdge(res, model.RelHasVuln, "pkg:pypi/langchain@0.2.1", "cve:CVE-2024-21513") {
		t.Fatalf("expected HAS_VULN langchain -> CVE; edges=%v", res.Edges)
	}
}

// Every emitted edge must reference at least one evidence id.
func TestEveryEdgeHasEvidence(t *testing.T) {
	paths := []string{
		"../../testdata/sboms/syft-image.cdx.json",
		"../../testdata/sboms/trivy-image.cdx.json",
		"../../testdata/sboms/cdxgen-aibom.cdx.json",
	}
	for _, p := range paths {
		res := normalizeBOM(t, p)
		for _, e := range res.Edges {
			if len(e.Evidence) == 0 {
				t.Errorf("%s: edge %s %s->%s has no evidence", p, e.Relation, e.SrcKey, e.DstKey)
			}
			for _, ev := range e.Evidence {
				if ev.RawEventID == "" {
					t.Errorf("%s: edge %s %s->%s has empty evidence id", p, e.Relation, e.SrcKey, e.DstKey)
				}
			}
		}
		for _, ent := range res.Entities {
			if len(ent.Evidence) == 0 {
				t.Errorf("%s: entity %s has no evidence", p, ent.Key)
			}
		}
	}
}
