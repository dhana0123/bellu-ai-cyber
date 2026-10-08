// OpenSearch-backed implementation of Indexer.
//
// It connects to BELLU_SEARCH_ENDPOINT, maintains a single index of canonical
// vulnerability documents, bulk-upserts them keyed by canonical id, and serves
// the three lookup shapes (vulnerability / package / package-version). The
// version lookup is a two-step query: a purl term query narrows candidates in
// the cluster, then matchesVersion post-filters them against the canonical
// half-open ranges using the authoritative comparator — so range semantics stay
// in one place (the canonical package) and the index mapping stays simple.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	opensearch "github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// DefaultIndex is the index name holding canonical intelligence documents.
const DefaultIndex = "intel-vulnerabilities"

// defaultSearchLimit caps results when a Query sets no positive Limit.
const defaultSearchLimit = 50

// osClient is the subset of the OpenSearch API the indexer uses. The concrete
// *opensearch.Client satisfies it; tests inject a fake.
type osClient interface {
	Perform(*http.Request) (*http.Response, error)
}

// OpenSearchIndexer is the OpenSearch-backed Indexer.
type OpenSearchIndexer struct {
	client osClient
	index  string
}

// Option customizes an OpenSearchIndexer.
type Option func(*OpenSearchIndexer)

// WithIndex overrides the index name (default DefaultIndex).
func WithIndex(name string) Option {
	return func(o *OpenSearchIndexer) {
		if strings.TrimSpace(name) != "" {
			o.index = name
		}
	}
}

// WithHTTPClient overrides the HTTP client used to reach the endpoint (e.g. to
// inject a custom transport or a fake in tests). It builds an opensearch client
// around the given transport and the endpoint address.
func withClient(c osClient) Option {
	return func(o *OpenSearchIndexer) { o.client = c }
}

// NewOpenSearch builds an indexer against the given endpoint (e.g. the value of
// BELLU_SEARCH_ENDPOINT). Credentials/signing are the caller's concern via the
// transport; on AWS the domain access policy authorizes the task role and the
// default transport suffices for VPC-internal HTTPS. An empty endpoint is an
// error.
func NewOpenSearch(endpoint string, opts ...Option) (*OpenSearchIndexer, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("search: OpenSearch endpoint must not be empty")
	}
	cfg := opensearch.Config{Addresses: []string{endpoint}}
	cl, err := opensearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("search: new opensearch client: %w", err)
	}
	o := &OpenSearchIndexer{client: cl, index: DefaultIndex}
	for _, opt := range opts {
		opt(o)
	}
	return o, nil
}

// newWithClient builds an indexer around an injected low-level client, for
// tests. The endpoint is irrelevant since the client is provided.
func newWithClient(c osClient, opts ...Option) *OpenSearchIndexer {
	o := &OpenSearchIndexer{client: c, index: DefaultIndex}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// indexMapping is the index settings + field mappings. Aliases/purls are
// keyword (exact term lookups), the summary is text (full-text), and the
// facets are keyword/boolean/float for filtering and sorting.
var indexMapping = `{
  "settings": { "index": { "number_of_shards": 1 } },
  "mappings": {
    "properties": {
      "id":              { "type": "keyword" },
      "aliases":         { "type": "keyword" },
      "purls":           { "type": "keyword" },
      "text":            { "type": "text" },
      "severity":        { "type": "keyword" },
      "known_exploited": { "type": "boolean" },
      "epss":            { "type": "float" },
      "cvss":            { "type": "float" },
      "sources":         { "type": "keyword" },
      "affected": {
        "type": "nested",
        "properties": {
          "purl":      { "type": "keyword" },
          "ecosystem": { "type": "keyword" },
          "ranges": {
            "type": "nested",
            "properties": {
              "introduced": { "type": "keyword" },
              "fixed":      { "type": "keyword" }
            }
          }
        }
      }
    }
  }
}`

// EnsureIndex creates the index with the canonical mapping if it does not exist.
func (o *OpenSearchIndexer) EnsureIndex(ctx context.Context) error {
	exists, err := o.indexExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	req := opensearchapi.IndicesCreateRequest{
		Index: o.index,
		Body:  strings.NewReader(indexMapping),
	}
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("search: create index: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("search: create index: %s", body(res))
	}
	return nil
}

func (o *OpenSearchIndexer) indexExists(ctx context.Context) (bool, error) {
	req := opensearchapi.IndicesExistsRequest{Index: []string{o.index}}
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return false, fmt.Errorf("search: index exists: %w", err)
	}
	defer res.Body.Close()
	// 200 = exists, 404 = absent; anything else is an error.
	switch res.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("search: index exists: %s", body(res))
	}
}

// Index upserts documents via the _bulk API, keyed on Document.ID so re-indexing
// is idempotent. Large corpora are chunked so a single request stays bounded.
func (o *OpenSearchIndexer) Index(ctx context.Context, docs []Document) error {
	const chunk = 500
	for start := 0; start < len(docs); start += chunk {
		end := start + chunk
		if end > len(docs) {
			end = len(docs)
		}
		if err := o.bulkIndex(ctx, docs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (o *OpenSearchIndexer) bulkIndex(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, d := range docs {
		if strings.TrimSpace(d.ID) == "" {
			return fmt.Errorf("search: document with empty id")
		}
		meta, _ := json.Marshal(map[string]map[string]string{
			"index": {"_index": o.index, "_id": d.ID},
		})
		src, err := json.Marshal(d)
		if err != nil {
			return fmt.Errorf("search: marshal doc %q: %w", d.ID, err)
		}
		buf.Write(meta)
		buf.WriteByte('\n')
		buf.Write(src)
		buf.WriteByte('\n')
	}

	req := opensearchapi.BulkRequest{
		Index: o.index,
		Body:  bytes.NewReader(buf.Bytes()),
	}
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("search: bulk index: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("search: bulk index: %s", body(res))
	}
	// The bulk API returns 200 even when individual items fail, so inspect the
	// per-item errors flag.
	var br struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&br); err != nil {
		return fmt.Errorf("search: decode bulk response: %w", err)
	}
	if br.Errors {
		for _, item := range br.Items {
			for _, v := range item {
				if v.Status >= 300 {
					return fmt.Errorf("search: bulk item failed (status %d): %s", v.Status, string(v.Error))
				}
			}
		}
	}
	return nil
}

// Search builds a bool query from the Query constraints, runs it, and returns
// ids + scores. A version lookup narrows by purl in the cluster, then
// post-filters candidates against the canonical ranges.
func (o *OpenSearchIndexer) Search(ctx context.Context, q Query) ([]Result, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	// A version lookup must see all candidate docs for the purl before the
	// post-filter trims them, so fetch a generous window then cap after.
	fetch := limit
	if q.Version != "" {
		fetch = limit * 10
		if fetch < 100 {
			fetch = 100
		}
	}

	body, err := json.Marshal(o.buildQuery(q, fetch))
	if err != nil {
		return nil, fmt.Errorf("search: marshal query: %w", err)
	}
	req := opensearchapi.SearchRequest{
		Index: []string{o.index},
		Body:  bytes.NewReader(body),
	}
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return nil, fmt.Errorf("search: query: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("search: query: %s", bodyStr(res))
	}

	var sr struct {
		Hits struct {
			Hits []struct {
				ID     string   `json:"_id"`
				Score  float64  `json:"_score"`
				Source Document `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("search: decode response: %w", err)
	}

	out := make([]Result, 0, len(sr.Hits.Hits))
	for _, h := range sr.Hits.Hits {
		if q.Version != "" && !matchesVersion(h.Source, q.Purl, q.Version) {
			continue
		}
		out = append(out, Result{ID: h.ID, Score: h.Score})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// buildQuery assembles the OpenSearch bool query for a Query.
func (o *OpenSearchIndexer) buildQuery(q Query, size int) map[string]any {
	var must []any
	var filter []any

	if strings.TrimSpace(q.Text) != "" {
		must = append(must, map[string]any{
			"match": map[string]any{"text": q.Text},
		})
	}
	if a := strings.TrimSpace(q.Alias); a != "" {
		filter = append(filter, map[string]any{
			"term": map[string]any{"aliases": strings.ToUpper(a)},
		})
	}
	if p := strings.TrimSpace(q.Purl); p != "" {
		filter = append(filter, map[string]any{
			"term": map[string]any{"purls": p},
		})
		if eco := strings.TrimSpace(q.Ecosystem); eco != "" {
			filter = append(filter, map[string]any{
				"nested": map[string]any{
					"path": "affected",
					"query": map[string]any{
						"bool": map[string]any{"must": []any{
							map[string]any{"term": map[string]any{"affected.purl": p}},
							map[string]any{"term": map[string]any{"affected.ecosystem": eco}},
						}},
					},
				},
			})
		}
	}
	if s := strings.TrimSpace(q.Severity); s != "" {
		filter = append(filter, map[string]any{
			"term": map[string]any{"severity": strings.ToLower(s)},
		})
	}
	if q.KnownExploitedOnly {
		filter = append(filter, map[string]any{
			"term": map[string]any{"known_exploited": true},
		})
	}

	boolq := map[string]any{}
	if len(must) > 0 {
		boolq["must"] = must
	}
	if len(filter) > 0 {
		boolq["filter"] = filter
	}
	// With no constraints, match everything (bounded by size).
	var query map[string]any
	if len(boolq) == 0 {
		query = map[string]any{"match_all": map[string]any{}}
	} else {
		query = map[string]any{"bool": boolq}
	}
	return map[string]any{"size": size, "query": query}
}

// Rebuild drops the index and reindexes the full corpus from src. The index is
// a disposable view of the authoritative Postgres store, so a full rebuild is
// always safe.
func (o *OpenSearchIndexer) Rebuild(ctx context.Context, src DocumentSource) error {
	if src == nil {
		return fmt.Errorf("search: rebuild needs a document source")
	}
	if err := o.deleteIndex(ctx); err != nil {
		return err
	}
	if err := o.EnsureIndex(ctx); err != nil {
		return err
	}
	docs, err := src.Documents(ctx)
	if err != nil {
		return fmt.Errorf("search: rebuild load corpus: %w", err)
	}
	return o.Index(ctx, docs)
}

func (o *OpenSearchIndexer) deleteIndex(ctx context.Context) error {
	req := opensearchapi.IndicesDeleteRequest{
		Index:             []string{o.index},
		IgnoreUnavailable: boolPtr(true),
	}
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("search: delete index: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("search: delete index: %s", bodyStr(res))
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

// body/bodyStr read an error response body for diagnostics.
func body(res *opensearchapi.Response) string { return bodyStr(res) }
func bodyStr(res *opensearchapi.Response) string {
	if res == nil || res.Body == nil {
		return res.String()
	}
	b, _ := io.ReadAll(res.Body)
	if len(b) == 0 {
		return res.String()
	}
	return string(b)
}

// Compile-time assertion that OpenSearchIndexer satisfies Indexer.
var _ Indexer = (*OpenSearchIndexer)(nil)
