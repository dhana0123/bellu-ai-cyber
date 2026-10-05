package resolve

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
)

func ev(id string) []model.Evidence {
	return []model.Evidence{{RawEventID: id, Source: "cyclonedx"}}
}

func TestCanonicalID(t *testing.T) {
	cases := []struct {
		kind     model.Kind
		provider string
		key      string
		want     string
	}{
		{model.KindContainerImage, "image", "sha256:abc", "image:sha256:abc"},
		{model.KindPackage, "repo", "pkg:pypi/x@1", "package:pkg:pypi/x@1"},
		{model.KindVulnerability, "repo", "cve:CVE-1", "vuln:CVE-1"},
		{model.KindIdentity, "aws", "arn:aws:iam::1:role/a", "identity:aws:arn:aws:iam::1:role/a"},
		{model.KindNetworkEndpoint, "network", "10.0.0.1", "ip:10.0.0.1"},
	}
	for _, c := range cases {
		if got := CanonicalID(c.kind, c.provider, c.key); got != c.want {
			t.Errorf("CanonicalID(%s,%s,%s) = %q, want %q", c.kind, c.provider, c.key, got, c.want)
		}
	}
}

func TestResolveAssignsIDsAndRewritesEdges(t *testing.T) {
	in := normalize.Result{
		Entities: []model.Entity{
			{Kind: model.KindPackage, Key: "pkg:pypi/a@1", Provider: "repo", Evidence: ev("r1")},
			{Kind: model.KindVulnerability, Key: "cve:CVE-1", Provider: "repo", Evidence: ev("r2")},
		},
		Edges: []model.Edge{
			{Relation: model.RelHasVuln, SrcKey: "pkg:pypi/a@1", DstKey: "cve:CVE-1", Confidence: 1, Evidence: ev("r2")},
		},
	}
	out, err := PassThrough{}.Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entities) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(out.Entities))
	}
	if len(out.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(out.Edges))
	}
	e := out.Edges[0]
	if e.SrcCanonicalID != "package:pkg:pypi/a@1" || e.DstCanonicalID != "vuln:CVE-1" {
		t.Fatalf("edge endpoints not rewritten: %+v", e)
	}
}

func TestResolveMergesSameCanonicalID(t *testing.T) {
	// Same image digest from two sources -> one node, two evidence rows.
	in := normalize.Result{
		Entities: []model.Entity{
			{Kind: model.KindContainerImage, Key: "sha256:x", Provider: "image", Name: "img", Evidence: ev("syft1")},
			{Kind: model.KindContainerImage, Key: "sha256:x", Provider: "image", Evidence: ev("trivy1")},
		},
	}
	out, err := PassThrough{}.Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entities) != 1 {
		t.Fatalf("expected 1 merged entity, got %d", len(out.Entities))
	}
	if len(out.Entities[0].Evidence) != 2 {
		t.Fatalf("expected 2 evidence rows after merge, got %d", len(out.Entities[0].Evidence))
	}
	if out.Entities[0].Name != "img" {
		t.Fatalf("merged name lost: %q", out.Entities[0].Name)
	}
}

func TestResolveDropsUnresolvedEdges(t *testing.T) {
	in := normalize.Result{
		Entities: []model.Entity{
			{Kind: model.KindPackage, Key: "pkg:pypi/a@1", Provider: "repo", Evidence: ev("r1")},
		},
		Edges: []model.Edge{
			{Relation: model.RelDependsOn, SrcKey: "pkg:pypi/a@1", DstKey: "pkg:pypi/missing@9", Evidence: ev("r1")},
		},
	}
	out, err := PassThrough{}.Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Edges) != 0 {
		t.Fatalf("expected unresolved edge dropped, got %d", len(out.Edges))
	}
}
