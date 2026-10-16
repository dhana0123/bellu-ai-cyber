package graphquery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// APIClient queries the Bellu control-panel HTTP API (backend/server). It uses
// the authenticated read endpoints: /api/health, /api/entities, /api/edges/find.
type APIClient struct {
	// BaseURL is the API root, e.g. http://localhost:8080.
	BaseURL string
	// Token is a bearer token from POST /api/auth/login (graph:read scope).
	Token string
	// HTTP is the client used for requests; nil means a default with a timeout.
	HTTP *http.Client
}

// NewAPIClient builds an API client with a bounded default HTTP timeout.
func NewAPIClient(baseURL, token string) *APIClient {
	return &APIClient{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *APIClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do issues an authenticated GET and decodes the JSON body into out.
func (c *APIClient) do(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graphquery: GET %s: status %s", path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Healthy probes GET /api/health.
func (c *APIClient) Healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.do(ctx, "/api/health", nil) == nil
}

// entityRow mirrors store.EntityRow's JSON (snake_case).
type entityRow struct {
	CanonicalID string `json:"canonical_id"`
	Kind        string `json:"kind"`
	Provider    string `json:"provider"`
	Name        string `json:"name"`
}

// Nodes queries GET /api/entities?kind=&limit=.
func (c *APIClient) Nodes(ctx context.Context, kind string) ([]Node, error) {
	q := url.Values{}
	q.Set("limit", "1000")
	if kind != "" {
		q.Set("kind", kind)
	}
	var body struct {
		Entities []entityRow `json:"entities"`
	}
	if err := c.do(ctx, "/api/entities?"+q.Encode(), &body); err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(body.Entities))
	for _, e := range body.Entities {
		out = append(out, Node{CanonicalID: e.CanonicalID, Kind: e.Kind, Name: e.Name})
	}
	return out, nil
}

// edgeDetail mirrors store.EdgeDetail's JSON. EdgeDetail has NO json tags, so
// Go serializes its exported fields with their Go names (PascalCase).
type edgeDetail struct {
	ID         int64   `json:"ID"`
	SrcID      string  `json:"SrcID"`
	Relation   string  `json:"Relation"`
	DstID      string  `json:"DstID"`
	Confidence float64 `json:"Confidence"`
}

// Edges queries GET /api/edges/find?relation=&limit=.
func (c *APIClient) Edges(ctx context.Context, relation string) ([]Edge, error) {
	q := url.Values{}
	q.Set("limit", "1000")
	if relation != "" {
		q.Set("relation", relation)
	}
	var body struct {
		Edges []edgeDetail `json:"edges"`
	}
	if err := c.do(ctx, "/api/edges/find?"+q.Encode(), &body); err != nil {
		return nil, err
	}
	out := make([]Edge, 0, len(body.Edges))
	for _, e := range body.Edges {
		out = append(out, Edge{SrcID: e.SrcID, Relation: e.Relation, DstID: e.DstID, Confidence: e.Confidence})
	}
	return out, nil
}
