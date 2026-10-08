package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
)

// fakeOS is an osClient that routes requests by METHOD+PATH to canned handlers
// and records the last request body per route, so tests can both drive the
// indexer offline and assert on what it sent.
type fakeOS struct {
	t        *testing.T
	handlers map[string]func(req *http.Request) *http.Response
	lastBody map[string]string
}

func newFakeOS(t *testing.T) *fakeOS {
	return &fakeOS{t: t, handlers: map[string]func(*http.Request) *http.Response{}, lastBody: map[string]string{}}
}

func (f *fakeOS) on(method, pathContains string, h func(*http.Request) *http.Response) {
	f.handlers[method+" "+pathContains] = h
}

func (f *fakeOS) Perform(req *http.Request) (*http.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	for key, h := range f.handlers {
		parts := strings.SplitN(key, " ", 2)
		if req.Method == parts[0] && strings.Contains(req.URL.Path, parts[1]) {
			f.lastBody[key] = body
			return h(req), nil
		}
	}
	f.t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
	return nil, nil
}

func resp(status int, jsonBody string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(jsonBody)),
	}
}

// hitsJSON builds a canned OpenSearch _search response body from documents.
func hitsJSON(t *testing.T, docs []Document) string {
	t.Helper()
	type hit struct {
		ID     string   `json:"_id"`
		Score  float64  `json:"_score"`
		Source Document `json:"_source"`
	}
	hits := make([]hit, 0, len(docs))
	for i, d := range docs {
		hits = append(hits, hit{ID: d.ID, Score: float64(len(docs) - i), Source: d})
	}
	payload := map[string]any{"hits": map[string]any{"hits": hits}}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal hits: %v", err)
	}
	return string(b)
}

func sampleDocs() []Document {
	return FromCanonicalMany([]canonical.CanonicalVuln{
		{
			ID:             "CVE-2024-0001",
			Aliases:        []string{"CVE-2024-0001", "GHSA-AAAA-BBBB-CCCC"},
			Summary:        "requests TLS verification bypass",
			Severity:       canonical.SevCritical,
			KnownExploited: true,
			EPSS:           0.7,
			CVSS:           canonical.CVSS{Score: 9.8},
			Affected: []canonical.AffectedPackage{{
				Purl: "pkg:pypi/requests", Ecosystem: "PyPI",
				Ranges: []canonical.SemverRange{{Introduced: "2.0.0", Fixed: "2.31.0"}},
			}},
			Sources: []string{"osv", "kev"},
		},
		{
			ID:       "CVE-2024-0002",
			Aliases:  []string{"CVE-2024-0002"},
			Summary:  "flask denial of service",
			Severity: canonical.SevHigh,
			Affected: []canonical.AffectedPackage{{
				Purl: "pkg:pypi/flask", Ecosystem: "PyPI",
				Ranges: []canonical.SemverRange{{Introduced: "1.0.0", Fixed: "1.1.4"}},
			}},
			Sources: []string{"osv"},
		},
	})
}

func TestFromCanonicalProjection(t *testing.T) {
	docs := sampleDocs()
	d := docs[0]
	if d.ID != "CVE-2024-0001" {
		t.Fatalf("id = %q", d.ID)
	}
	if len(d.Purls) != 1 || d.Purls[0] != "pkg:pypi/requests" {
		t.Fatalf("purls = %v", d.Purls)
	}
	if !d.KnownExploited || d.Severity != "critical" || d.CVSS != 9.8 {
		t.Fatalf("facets wrong: %+v", d)
	}
	if len(d.Affected) != 1 || d.Affected[0].Ranges[0].Fixed != "2.31.0" {
		t.Fatalf("affected ranges wrong: %+v", d.Affected)
	}
}

func TestEnsureIndexCreatesWhenAbsent(t *testing.T) {
	f := newFakeOS(t)
	// HEAD -> 404 (absent), then PUT create -> 200.
	f.on(http.MethodHead, DefaultIndex, func(*http.Request) *http.Response { return resp(404, "") })
	created := false
	f.on(http.MethodPut, DefaultIndex, func(r *http.Request) *http.Response {
		created = true
		return resp(200, `{"acknowledged":true}`)
	})
	idx := newWithClient(f)
	if err := idx.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("EnsureIndex: %v", err)
	}
	if !created {
		t.Fatal("expected index to be created when absent")
	}
	// The create body carries the mapping.
	if !strings.Contains(f.lastBody[http.MethodPut+" "+DefaultIndex], `"known_exploited"`) {
		t.Fatalf("create body missing mapping: %s", f.lastBody[http.MethodPut+" "+DefaultIndex])
	}
}

func TestEnsureIndexNoopWhenPresent(t *testing.T) {
	f := newFakeOS(t)
	f.on(http.MethodHead, DefaultIndex, func(*http.Request) *http.Response { return resp(200, "") })
	f.on(http.MethodPut, DefaultIndex, func(*http.Request) *http.Response {
		t.Fatal("must not create when index already exists")
		return resp(200, "")
	})
	if err := newWithClient(f).EnsureIndex(context.Background()); err != nil {
		t.Fatalf("EnsureIndex: %v", err)
	}
}

func TestBulkIndexSendsNdjsonKeyedById(t *testing.T) {
	f := newFakeOS(t)
	f.on(http.MethodPost, "_bulk", func(*http.Request) *http.Response {
		return resp(200, `{"errors":false,"items":[]}`)
	})
	if err := newWithClient(f).Index(context.Background(), sampleDocs()); err != nil {
		t.Fatalf("Index: %v", err)
	}
	sent := f.lastBody[http.MethodPost+" _bulk"]
	// NDJSON: action/meta line keyed by _id, then the source line.
	if !strings.Contains(sent, `"_id":"CVE-2024-0001"`) || !strings.Contains(sent, `"_id":"CVE-2024-0002"`) {
		t.Fatalf("bulk body not keyed by id: %s", sent)
	}
	if !strings.Contains(sent, `"pkg:pypi/requests"`) {
		t.Fatalf("bulk body missing projected purl: %s", sent)
	}
}

func TestBulkIndexSurfacesItemErrors(t *testing.T) {
	f := newFakeOS(t)
	f.on(http.MethodPost, "_bulk", func(*http.Request) *http.Response {
		return resp(200, `{"errors":true,"items":[{"index":{"status":400,"error":{"type":"mapper_parsing_exception"}}}]}`)
	})
	err := newWithClient(f).Index(context.Background(), sampleDocs())
	if err == nil || !strings.Contains(err.Error(), "bulk item failed") {
		t.Fatalf("expected bulk item error, got %v", err)
	}
}

func TestSearchVulnerabilityByAlias(t *testing.T) {
	f := newFakeOS(t)
	all := sampleDocs()
	f.on(http.MethodPost, "_search", func(r *http.Request) *http.Response {
		// Only CVE-2024-0001 carries the GHSA alias.
		return resp(200, hitsJSON(t, all[:1]))
	})
	idx := newWithClient(f)
	got, err := idx.Search(context.Background(), Query{Alias: "ghsa-aaaa-bbbb-cccc"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "CVE-2024-0001" {
		t.Fatalf("alias search = %+v", got)
	}
	// The alias filter must be uppercased in the query body.
	body := f.lastBody[http.MethodPost+" _search"]
	if !strings.Contains(body, "GHSA-AAAA-BBBB-CCCC") {
		t.Fatalf("alias not normalized in query: %s", body)
	}
}

func TestSearchPackageByPurl(t *testing.T) {
	f := newFakeOS(t)
	all := sampleDocs()
	f.on(http.MethodPost, "_search", func(*http.Request) *http.Response {
		return resp(200, hitsJSON(t, all[:1])) // requests only
	})
	got, err := newWithClient(f).Search(context.Background(), Query{Purl: "pkg:pypi/requests"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "CVE-2024-0001" {
		t.Fatalf("purl search = %+v", got)
	}
}

func TestSearchVersionPostFilter(t *testing.T) {
	f := newFakeOS(t)
	all := sampleDocs() // requests affected [2.0.0, 2.31.0)
	// The cluster returns the purl candidates; the post-filter decides.
	f.on(http.MethodPost, "_search", func(*http.Request) *http.Response {
		return resp(200, hitsJSON(t, all[:1]))
	})
	idx := newWithClient(f)

	// 2.30.0 is inside the affected range -> match.
	in, err := idx.Search(context.Background(), Query{Purl: "pkg:pypi/requests", Version: "2.30.0"})
	if err != nil {
		t.Fatalf("Search in-range: %v", err)
	}
	if len(in) != 1 || in[0].ID != "CVE-2024-0001" {
		t.Fatalf("expected in-range match, got %+v", in)
	}

	// 2.31.0 is the exclusive fixed bound -> no match (patched).
	out, err := idx.Search(context.Background(), Query{Purl: "pkg:pypi/requests", Version: "2.31.0"})
	if err != nil {
		t.Fatalf("Search fixed-edge: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no match at fixed edge, got %+v", out)
	}
}

func TestSearchErrorStatus(t *testing.T) {
	f := newFakeOS(t)
	f.on(http.MethodPost, "_search", func(*http.Request) *http.Response {
		return resp(500, `{"error":"boom"}`)
	})
	if _, err := newWithClient(f).Search(context.Background(), Query{Text: "anything"}); err == nil {
		t.Fatal("expected error on 500 search")
	}
}

func TestNewOpenSearchRejectsEmptyEndpoint(t *testing.T) {
	if _, err := NewOpenSearch("   "); err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

// fakeSource is a DocumentSource for Rebuild tests.
type fakeSource struct{ docs []Document }

func (s fakeSource) Documents(context.Context) ([]Document, error) { return s.docs, nil }

func TestRebuildDeletesEnsuresAndIndexes(t *testing.T) {
	f := newFakeOS(t)
	var deleted, created, bulked bool
	f.on(http.MethodDelete, DefaultIndex, func(*http.Request) *http.Response { deleted = true; return resp(200, `{"acknowledged":true}`) })
	f.on(http.MethodHead, DefaultIndex, func(*http.Request) *http.Response { return resp(404, "") })
	f.on(http.MethodPut, DefaultIndex, func(*http.Request) *http.Response { created = true; return resp(200, `{"acknowledged":true}`) })
	f.on(http.MethodPost, "_bulk", func(*http.Request) *http.Response { bulked = true; return resp(200, `{"errors":false,"items":[]}`) })

	if err := newWithClient(f).Rebuild(context.Background(), fakeSource{docs: sampleDocs()}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if !deleted || !created || !bulked {
		t.Fatalf("rebuild steps: deleted=%v created=%v bulked=%v", deleted, created, bulked)
	}
}
