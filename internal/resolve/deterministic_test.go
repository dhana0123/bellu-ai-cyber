package resolve

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
)

func TestNormalizeImageKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sha256:abc", "sha256:abc"},
		{"123.dkr.ecr.us-east-1.amazonaws.com/x@sha256:abc", "sha256:abc"},
		{"pkg:oci/x@sha256:abc?repository_url=foo", "sha256:abc"},
		{"no-digest-here", "no-digest-here"},
	}
	for _, c := range cases {
		if got := normalizeImageKey(c.in); got != c.want {
			t.Errorf("normalizeImageKey(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/bellu/agent-api.git", "https://github.com/bellu/agent-api"},
		{"git@github.com:bellu/agent-api.git", "https://github.com/bellu/agent-api"},
		{"ssh://git@github.com/bellu/agent-api", "https://github.com/bellu/agent-api"},
		{"https://github.com/bellu/agent-api/", "https://github.com/bellu/agent-api"},
	}
	for _, c := range cases {
		if got := NormalizeRepoURL(c.in); got != c.want {
			t.Errorf("NormalizeRepoURL(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func imgEnt(key, src string) model.Entity {
	return model.Entity{
		Kind: model.KindContainerImage, Key: key, Provider: "image", Name: "img",
		Attributes: map[string]any{},
		Evidence:   []model.Evidence{{RawEventID: src + "-1", Source: src}},
	}
}

func TestImageUnifiesAcrossThreeSources(t *testing.T) {
	// ECR digest, K8s imageID form, and SBOM purl all carry the same sha256.
	in := normalize.Result{
		Entities: []model.Entity{
			imgEnt("sha256:deadbeef", "ecr"),
			imgEnt("123.dkr.ecr.us-east-1.amazonaws.com/x@sha256:deadbeef", "k8s"),
			imgEnt("pkg:oci/x@sha256:deadbeef?repository_url=foo", "syft"),
		},
	}
	out, err := NewDeterministic(nil).Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entities) != 1 {
		t.Fatalf("expected 1 unified image node, got %d", len(out.Entities))
	}
	if got := out.Entities[0].CanonicalID; got != "image:sha256:deadbeef" {
		t.Fatalf("unexpected canonical id %q", got)
	}
	if n := len(out.Entities[0].Evidence); n != 3 {
		t.Fatalf("expected 3 evidence rows from 3 sources, got %d", n)
	}
}

func TestAzureIDLowercased(t *testing.T) {
	in := normalize.Result{
		Entities: []model.Entity{
			{Kind: model.KindDataStore, Key: "/Subscriptions/SUB/Store", Provider: "azure", Evidence: ev("e1")},
			{Kind: model.KindDataStore, Key: "/subscriptions/sub/store", Provider: "azure", Evidence: ev("e2")},
		},
	}
	out, _ := NewDeterministic(nil).Resolve(in)
	if len(out.Entities) != 1 {
		t.Fatalf("expected azure ids to unify by lowercase, got %d nodes", len(out.Entities))
	}
}

func TestBuiltFromOCILabel(t *testing.T) {
	img := imgEnt("sha256:cafe", "syft")
	img.Attributes["repo_url"] = "https://github.com/bellu/agent-api.git"
	in := normalize.Result{
		Entities: []model.Entity{
			img,
			{Kind: model.KindRepository, Key: "https://github.com/bellu/agent-api", Provider: "repo", Evidence: ev("r1")},
		},
	}
	out, _ := NewDeterministic(nil).Resolve(in)
	var found *model.Edge
	for i := range out.Edges {
		if out.Edges[i].Relation == model.RelBuiltFrom {
			found = &out.Edges[i]
		}
	}
	if found == nil {
		t.Fatalf("expected BUILT_FROM edge; edges=%v", out.Edges)
	}
	if found.Confidence != 0.7 {
		t.Fatalf("expected confidence 0.7, got %v", found.Confidence)
	}
	if found.Attributes["method"] != "oci_label" {
		t.Fatalf("expected oci_label method, got %v", found.Attributes["method"])
	}
}

func TestBuiltFromMappingFallback(t *testing.T) {
	img := imgEnt("sha256:beef", "syft") // no repo_url label
	in := normalize.Result{
		Entities: []model.Entity{
			img,
			{Kind: model.KindRepository, Key: "https://github.com/bellu/agent-api", Provider: "repo", Evidence: ev("r1")},
		},
	}
	mappings := map[string]string{"https://github.com/bellu/agent-api": "sha256:beef"}
	out, _ := NewDeterministic(mappings).Resolve(in)
	var found *model.Edge
	for i := range out.Edges {
		if out.Edges[i].Relation == model.RelBuiltFrom {
			found = &out.Edges[i]
		}
	}
	if found == nil {
		t.Fatalf("expected BUILT_FROM from mapping fallback; edges=%v", out.Edges)
	}
	if found.Attributes["method"] != "mappings_yaml" {
		t.Fatalf("expected mappings_yaml method, got %v", found.Attributes["method"])
	}
}

func TestConflictOnNameMismatch(t *testing.T) {
	in := normalize.Result{
		Entities: []model.Entity{
			{Kind: model.KindDataStore, Key: "/subscriptions/s/store", Provider: "azure", Name: "alpha", Evidence: ev("e1")},
			{Kind: model.KindDataStore, Key: "/subscriptions/s/store", Provider: "azure", Name: "beta", Evidence: ev("e2")},
		},
	}
	r := NewDeterministic(nil)
	_, _ = r.Resolve(in)
	if len(r.Conflicts) == 0 {
		t.Fatal("expected a name_mismatch conflict to be logged")
	}
}

func TestIPToWorkloadResolution(t *testing.T) {
	in := normalize.Result{
		Entities: []model.Entity{
			{Kind: model.KindWorkload, Key: "k8s:pod-1", Provider: "k8s", Name: "pod",
				Attributes: map[string]any{"pod_ip": "10.0.1.5"}, Evidence: ev("e1")},
			{Kind: model.KindExternalService, Key: "api.openai.com", Provider: "network",
				Attributes: map[string]any{}, Evidence: ev("e2")},
		},
		Edges: []model.Edge{
			// Zeek flow keyed by source IP.
			{Relation: model.RelConnectsTo, SrcKey: "10.0.1.5", DstKey: "api.openai.com", Confidence: 0.9, Evidence: ev("e3")},
		},
	}
	out, _ := NewDeterministic(nil).Resolve(in)
	var found *model.Edge
	for i := range out.Edges {
		if out.Edges[i].Relation == model.RelConnectsTo {
			found = &out.Edges[i]
		}
	}
	if found == nil {
		t.Fatalf("expected CONNECTS_TO edge; edges=%v", out.Edges)
	}
	if found.SrcCanonicalID != "workload:k8s:k8s:pod-1" {
		t.Fatalf("expected source IP to resolve to workload, got %q", found.SrcCanonicalID)
	}
}

func TestIPReuseAcrossTime(t *testing.T) {
	// The same IP owned by two workloads records two aliases (time-bounding is
	// applied at persistence); the resolver keeps the last-indexed owner for
	// edge resolution within a run.
	in := normalize.Result{
		Entities: []model.Entity{
			{Kind: model.KindWorkload, Key: "k8s:pod-a", Provider: "k8s",
				Attributes: map[string]any{"pod_ip": "10.0.1.5"}, Evidence: ev("a")},
			{Kind: model.KindWorkload, Key: "k8s:pod-b", Provider: "k8s",
				Attributes: map[string]any{"pod_ip": "10.0.1.5"}, Evidence: ev("b")},
		},
	}
	r := NewDeterministic(nil)
	_, _ = r.Resolve(in)
	ipAliases := 0
	for _, a := range r.Aliases {
		if a.AliasKey == "ip:10.0.1.5" {
			ipAliases++
		}
	}
	if ipAliases != 2 {
		t.Fatalf("expected 2 IP aliases for reused address, got %d", ipAliases)
	}
}

func TestAliasRecorded(t *testing.T) {
	in := normalize.Result{
		Entities: []model.Entity{
			imgEnt("123.dkr.ecr.us-east-1.amazonaws.com/x@sha256:feed", "ecr"),
		},
	}
	r := NewDeterministic(nil)
	_, _ = r.Resolve(in)
	if len(r.Aliases) == 0 {
		t.Fatal("expected an alias recorded for the non-canonical image key")
	}
	if r.Aliases[0].CanonicalID != "image:sha256:feed" {
		t.Fatalf("alias canonical id = %q", r.Aliases[0].CanonicalID)
	}
}
