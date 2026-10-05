package cyclonedx

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// collect runs the source over a single file and returns all events plus any
// errors.
func collect(t *testing.T, path string) ([]rawevent.RawEvent, []error) {
	t.Helper()
	s := New(path)
	evc, errc := s.Collect(context.Background(), source.Run{RunID: "run-test"})
	var events []rawevent.RawEvent
	var errs []error
	for evc != nil || errc != nil {
		select {
		case ev, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			if err := ev.Validate(); err != nil {
				t.Errorf("invalid event %s: %v", ev.ResourceID, err)
			}
			events = append(events, ev)
		case err, ok := <-errc:
			if !ok {
				errc = nil
				continue
			}
			errs = append(errs, err)
		}
	}
	return events, errs
}

func byType(events []rawevent.RawEvent, rt string) []rawevent.RawEvent {
	var out []rawevent.RawEvent
	for _, e := range events {
		if e.ResourceType == rt {
			out = append(out, e)
		}
	}
	return out
}

func find(events []rawevent.RawEvent, rt, rid string) *rawevent.RawEvent {
	for i := range events {
		if events[i].ResourceType == rt && events[i].ResourceID == rid {
			return &events[i]
		}
	}
	return nil
}

const imageDigestID = "sha256:aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa7777bbbb8888"

func TestSyftImageBOM(t *testing.T) {
	events, errs := collect(t, "../../../testdata/sboms/syft-image.cdx.json")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Subject keyed by digest.
	img := find(events, RTImage, imageDigestID)
	if img == nil {
		t.Fatalf("expected image subject keyed by digest %s; got %+v", imageDigestID, ids(events))
	}
	if img.Collector != "cyclonedx:syft" {
		t.Fatalf("expected syft collector, got %q", img.Collector)
	}
	if img.Provider != "image" {
		t.Fatalf("expected image provider, got %q", img.Provider)
	}
	var p map[string]any
	_ = json.Unmarshal(img.Payload, &p)
	if p["repo_url"] != "https://github.com/bellu/agent-api" {
		t.Fatalf("repo_url from OCI source label not captured: %v", p["repo_url"])
	}
	// Components and dependencies.
	if n := len(byType(events, RTComponent)); n != 2 {
		t.Fatalf("expected 2 components, got %d", n)
	}
	if n := len(byType(events, RTDependency)); n != 2 {
		t.Fatalf("expected 2 dependency edges, got %d", n)
	}
}

func TestTrivyDigestFromProperty(t *testing.T) {
	events, errs := collect(t, "../../../testdata/sboms/trivy-image.cdx.json")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	img := find(events, RTImage, imageDigestID)
	if img == nil {
		t.Fatalf("trivy digest not resolved to %s; got %v", imageDigestID, ids(events))
	}
	if img.Collector != "cyclonedx:trivy" {
		t.Fatalf("expected trivy collector, got %q", img.Collector)
	}
	// Vulnerability emitted and affects langchain.
	vulns := byType(events, RTVuln)
	if len(vulns) != 1 {
		t.Fatalf("expected 1 vulnerability, got %d", len(vulns))
	}
	var vp map[string]any
	_ = json.Unmarshal(vulns[0].Payload, &vp)
	if vp["id"] != "CVE-2024-21513" {
		t.Fatalf("unexpected vuln id: %v", vp["id"])
	}
	aff, _ := vp["affects"].([]any)
	if len(aff) != 1 || aff[0] != "pkg:pypi/langchain@0.2.1" {
		t.Fatalf("vuln affects not captured: %v", vp["affects"])
	}
}

func TestAibomSignals(t *testing.T) {
	events, errs := collect(t, "../../../testdata/sboms/cdxgen-aibom.cdx.json")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Repo subject (not an image) with VCS url.
	repo := find(events, RTRepository, "https://github.com/bellu/agent-api")
	if repo == nil {
		t.Fatalf("expected repo subject; got %v", ids(events))
	}
	if repo.Collector != "cyclonedx:cdxgen" {
		t.Fatalf("expected cdxgen collector, got %q", repo.Collector)
	}

	// Model component present with model card.
	model := find(events, RTComponent, "pkg:huggingface/meta-llama/Llama-3-8B@main")
	if model == nil {
		t.Fatalf("expected model component; got %v", ids(events))
	}
	var mp map[string]any
	_ = json.Unmarshal(model.Payload, &mp)
	if mp["type"] != "machine-learning-model" {
		t.Fatalf("expected ML model type, got %v", mp["type"])
	}
	if _, ok := mp["model_card"]; !ok {
		t.Fatalf("model card not captured")
	}

	// MCP server surfaces as a service.
	mcp := find(events, RTService, "service:mcp-filesystem")
	if mcp == nil {
		t.Fatalf("expected MCP service; got %v", ids(events))
	}
	var sp map[string]any
	_ = json.Unmarshal(mcp.Payload, &sp)
	props, _ := sp["properties"].(map[string]any)
	if props["ai:mcp:transport"] != "stdio" {
		t.Fatalf("MCP transport property not captured: %v", sp["properties"])
	}
}

func TestUnknownToolDoesNotError(t *testing.T) {
	// A minimal BOM with no tools metadata should still parse.
	events, errs := collect(t, "../../../testdata/sboms/cdxgen-aibom.cdx.json")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(events) == 0 {
		t.Fatal("expected events")
	}
}

func ids(events []rawevent.RawEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.ResourceType+"="+e.ResourceID)
	}
	return out
}
