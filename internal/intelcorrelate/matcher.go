// Package intelcorrelate is the bridge between Bellu's canonical intelligence
// corpus and a customer's AI Security Graph. Its Matcher reads graph assets
// (Package / ContainerImage / AI nodes) together with canonical intel and emits
// Candidate (asset, vulnerability) pairings, each citing the intel record id it
// came from.
//
// This file implements only matching + Candidate emission (task 14.2). It does
// NOT enrich the graph (Vulnerability node + HAS_VULN edge) — that is task 14.3
// — and it never writes to the Postgres evidence table. The matcher is
// read-only against the graph: it issues vetted, parameterized read-only Cypher
// (MATCH ... RETURN) through graph.Writer.QueryRows and performs no MERGE / SET
// / CREATE / DELETE.
package intelcorrelate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/correlation"
	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// Candidate is a (asset, vulnerability) pairing produced by the Matcher. Each
// candidate records which intel record justified it (IntelRef) and on what
// basis the asset and vuln were correlated (MatchBasis).
type Candidate struct {
	// AssetID is the graph canonical_id of the matched asset
	// (Package / ContainerImage / AIAgent / AIModel / MCPServer).
	AssetID string
	// AssetKind is the model kind of the matched asset.
	AssetKind model.Kind
	// VulnID is the canonical intel vulnerability id.
	VulnID string
	// IntelRef cites the intel record id the match came from (the canonical
	// vuln id / alias). It is NEVER a raw_event id (Requirement 8.4).
	IntelRef string
	// MatchBasis records how the match was made:
	// "purl+range" | "image-digest" | "image-package" | "ai-model" | "mcp".
	MatchBasis string
}

// Match basis constants describe how a Candidate was correlated.
const (
	BasisPurlRange    = "purl+range"
	BasisImageDigest  = "image-digest"
	BasisImagePackage = "image-package"
	BasisAIModel      = "ai-model"
	BasisMCP          = "mcp"
)

// Matcher correlates canonical intel against the customer graph and emits
// candidate findings. It never fabricates evidence: every candidate cites the
// intel record id it came from.
type Matcher interface {
	Correlate(ctx context.Context) ([]Candidate, error)
}

// IntelSource is the matcher's read-only view of the canonical intelligence
// corpus. It is intentionally small so the matcher does not hard-depend on the
// intel Postgres store and can be unit-tested with an in-memory fake.
//
// Design: package correlation is purl-keyed, so VulnsByPurl lets the matcher
// fetch only the handful of vulns that mention a given purl name (matching the
// intel_affected_packages.purl index) rather than scanning the whole corpus for
// every Package node. Image-digest and AI-asset correlation are not purl-keyed,
// so AllVulns supplies the full set the matcher scans once for those assets.
// A real implementation (backed by the intel_* tables, task 16.1) satisfies
// both; the matcher only ever reads through this interface.
type IntelSource interface {
	// VulnsByPurl returns every canonical vuln whose affected set names the
	// given purl (the purl name, without a version qualifier). The purlName is
	// matched case-sensitively against AffectedPackage.Purl.
	VulnsByPurl(ctx context.Context, purlName string) ([]canonical.CanonicalVuln, error)
	// AllVulns returns the full canonical corpus for non-purl-keyed matching
	// (container-image digests and AI-asset identifiers).
	AllVulns(ctx context.Context) ([]canonical.CanonicalVuln, error)
}

// GraphReader is the read-only slice of graph.Writer the matcher needs. The
// concrete *graph.Writer satisfies it via its QueryRows method, and tests can
// supply a fake without a live Memgraph.
type GraphReader interface {
	QueryRows(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// matcher is the concrete Matcher. It reads assets from the graph (read-only)
// and intel from an IntelSource, then emits Candidates.
type matcher struct {
	graph GraphReader
	intel IntelSource
}

// New builds a Matcher from a read-only graph handle and an intel source.
// Passing a *graph.Writer uses only its read-only QueryRows method.
func New(g GraphReader, intel IntelSource) Matcher {
	return &matcher{graph: g, intel: intel}
}

// Read-only Cypher that enumerates the asset classes the matcher correlates.
// Each query is MATCH ... RETURN only (no writes) and references only vetted
// model labels. Values are returned, never interpolated. Package and
// ContainerImage carry the canonical_id as the asset key; the versioned purl
// and the image digest are read from the stored node name (set by the
// cyclonedx normalizer: Package.name is the purl, ContainerImage.name is the
// digest). embedded image packages, when present, are read from an optional
// list property so image-package correlation can run without a live SBOM join.
const (
	cypherPackages = `
		MATCH (p:Package)
		RETURN p.canonical_id AS id, p.name AS purl`

	cypherImages = `
		MATCH (i:ContainerImage)
		RETURN i.canonical_id AS id, i.name AS digest,
		       i.packages AS packages`

	cypherAIModels = `
		MATCH (m:AIModel)
		RETURN m.canonical_id AS id, m.name AS model_id`

	cypherAIAgents = `
		MATCH (a:AIAgent)
		RETURN a.canonical_id AS id, a.name AS model_id`

	cypherMCPServers = `
		MATCH (s:MCPServer)
		RETURN s.canonical_id AS id, s.name AS mcp_id`
)

// Correlate enumerates graph assets, looks up intel, and returns the candidate
// (asset, vuln) pairings. It is deterministic: candidates are sorted by
// (AssetID, VulnID, MatchBasis) so repeated runs over the same inputs produce
// an identical slice.
func (m *matcher) Correlate(ctx context.Context) ([]Candidate, error) {
	var out []Candidate

	pkgCands, err := m.correlatePackages(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, pkgCands...)

	imgCands, err := m.correlateImages(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, imgCands...)

	aiCands, err := m.correlateAIAssets(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, aiCands...)

	sortCandidates(out)
	return out, nil
}

// correlatePackages matches Package nodes against intel by purl + version range.
func (m *matcher) correlatePackages(ctx context.Context) ([]Candidate, error) {
	rows, err := m.graph.QueryRows(ctx, cypherPackages, nil)
	if err != nil {
		return nil, fmt.Errorf("intelcorrelate: query packages: %w", err)
	}
	var out []Candidate
	for _, row := range rows {
		assetID := asString(row["id"])
		purl := asString(row["purl"])
		if assetID == "" || purl == "" {
			continue
		}
		name, version := correlation.SplitPurl(purl)
		if name == "" {
			continue
		}
		vulns, err := m.intel.VulnsByPurl(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("intelcorrelate: intel by purl %q: %w", name, err)
		}
		out = append(out, matchPackageVulns(assetID, name, version, vulns)...)
	}
	return out, nil
}

// matchPackageVulns is the pure core of package correlation: given an asset and
// the candidate vulns for its purl name, it emits a Candidate for each vuln
// whose affected range contains the package version (introduced <= v < fixed).
// A version outside every declared range is NOT emitted (Requirement 8.5).
// Extracted as a pure function so it is unit-testable without a graph or DB.
func matchPackageVulns(assetID, purlName, version string, vulns []canonical.CanonicalVuln) []Candidate {
	var out []Candidate
	for _, v := range vulns {
		if correlation.MatchesPackage(v.Affected, purlName, version) {
			out = append(out, Candidate{
				AssetID:    assetID,
				AssetKind:  model.KindPackage,
				VulnID:     v.ID,
				IntelRef:   v.ID,
				MatchBasis: BasisPurlRange,
			})
		}
	}
	return out
}

// correlateImages matches ContainerImage nodes against intel by digest and by
// embedded package purls. This is intentionally minimal but present (Req 8.2).
func (m *matcher) correlateImages(ctx context.Context) ([]Candidate, error) {
	rows, err := m.graph.QueryRows(ctx, cypherImages, nil)
	if err != nil {
		return nil, fmt.Errorf("intelcorrelate: query images: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	vulns, err := m.intel.AllVulns(ctx)
	if err != nil {
		return nil, fmt.Errorf("intelcorrelate: all vulns: %w", err)
	}
	var out []Candidate
	for _, row := range rows {
		assetID := asString(row["id"])
		if assetID == "" {
			continue
		}
		digest := asString(row["digest"])
		pkgs := asStringSlice(row["packages"])
		out = append(out, matchImageVulns(assetID, digest, pkgs, vulns)...)
	}
	return out, nil
}

// matchImageVulns is the pure core of image correlation. An image matches a
// vuln on either of two bases:
//
//   - "image-digest": an affected entry in an OCI/container ecosystem carries
//     the image digest in its Purl field and equals the image's digest; or
//   - "image-package": an embedded image package falls inside an affected
//     range, reusing the vetted purl+range logic.
//
// Both bases are deterministic and purl/digest-driven, so the matcher never
// fabricates a container match from anything but a declared intel record.
// Extracted as a pure function so it is unit-testable without a graph or DB.
func matchImageVulns(assetID, digest string, imagePkgs []string, vulns []canonical.CanonicalVuln) []Candidate {
	var out []Candidate
	for _, v := range vulns {
		// (a) digest-level correlation: an affected entry in an OCI/container
		//     ecosystem carries the image digest in its Purl field.
		if digest != "" {
			for _, ap := range v.Affected {
				if isContainerEcosystem(ap.Ecosystem) &&
					correlation.MatchContainerImageDigest(ap.Purl, digest) {
					out = append(out, Candidate{
						AssetID:    assetID,
						AssetKind:  model.KindContainerImage,
						VulnID:     v.ID,
						IntelRef:   v.ID,
						MatchBasis: BasisImageDigest,
					})
					break
				}
			}
		}
		// (b) embedded-package correlation reuses the vetted purl+range logic.
		if len(imagePkgs) > 0 {
			if ok, _, _ := correlation.MatchContainerImagePackages(v.Affected, imagePkgs); ok {
				out = append(out, Candidate{
					AssetID:    assetID,
					AssetKind:  model.KindContainerImage,
					VulnID:     v.ID,
					IntelRef:   v.ID,
					MatchBasis: BasisImagePackage,
				})
			}
		}
	}
	return out
}

// correlateAIAssets matches AIModel / AIAgent nodes (by model identifier) and
// MCPServer nodes (by MCP identifier) against intel. Minimal but present
// (Requirement 8.3).
func (m *matcher) correlateAIAssets(ctx context.Context) ([]Candidate, error) {
	var out []Candidate

	modelRows := make([]map[string]any, 0)
	for _, q := range []string{cypherAIModels, cypherAIAgents} {
		rows, err := m.graph.QueryRows(ctx, q, nil)
		if err != nil {
			return nil, fmt.Errorf("intelcorrelate: query ai assets: %w", err)
		}
		modelRows = append(modelRows, rows...)
	}
	mcpRows, err := m.graph.QueryRows(ctx, cypherMCPServers, nil)
	if err != nil {
		return nil, fmt.Errorf("intelcorrelate: query mcp servers: %w", err)
	}
	if len(modelRows) == 0 && len(mcpRows) == 0 {
		return nil, nil
	}

	vulns, err := m.intel.AllVulns(ctx)
	if err != nil {
		return nil, fmt.Errorf("intelcorrelate: all vulns: %w", err)
	}

	for _, row := range modelRows {
		assetID := asString(row["id"])
		modelID := asString(row["model_id"])
		if assetID == "" || modelID == "" {
			continue
		}
		out = append(out, matchAIModelVulns(assetID, modelID, vulns)...)
	}
	for _, row := range mcpRows {
		assetID := asString(row["id"])
		mcpID := asString(row["mcp_id"])
		if assetID == "" || mcpID == "" {
			continue
		}
		out = append(out, matchMCPVulns(assetID, mcpID, vulns)...)
	}
	return out, nil
}

// matchAIModelVulns emits a Candidate for each vuln whose AI model identifier
// matches the asset's model identifier. The intel model identifier is carried
// in an affected entry whose Ecosystem marks it AI (so AI intel reuses the same
// Affected list the schema already stores). Pure for unit testing.
func matchAIModelVulns(assetID, modelID string, vulns []canonical.CanonicalVuln) []Candidate {
	var out []Candidate
	for _, v := range vulns {
		for _, ap := range v.Affected {
			if isAIModelEcosystem(ap.Ecosystem) && correlation.MatchAIModel(ap.Purl, modelID) {
				out = append(out, Candidate{
					AssetID:    assetID,
					AssetKind:  model.KindAIModel,
					VulnID:     v.ID,
					IntelRef:   v.ID,
					MatchBasis: BasisAIModel,
				})
				break
			}
		}
	}
	return out
}

// matchMCPVulns emits a Candidate for each vuln whose MCP-server identifier
// matches the asset's MCP identifier. Pure for unit testing.
func matchMCPVulns(assetID, mcpID string, vulns []canonical.CanonicalVuln) []Candidate {
	var out []Candidate
	for _, v := range vulns {
		for _, ap := range v.Affected {
			if isMCPEcosystem(ap.Ecosystem) && correlation.MatchMCPServer(ap.Purl, mcpID) {
				out = append(out, Candidate{
					AssetID:    assetID,
					AssetKind:  model.KindMCPServer,
					VulnID:     v.ID,
					IntelRef:   v.ID,
					MatchBasis: BasisMCP,
				})
				break
			}
		}
	}
	return out
}

func isContainerEcosystem(eco string) bool {
	switch strings.ToLower(strings.TrimSpace(eco)) {
	case "oci", "container", "docker":
		return true
	}
	return false
}

func isAIModelEcosystem(eco string) bool {
	switch strings.ToLower(strings.TrimSpace(eco)) {
	case "ai", "aimodel", "ai-model", "huggingface", "model":
		return true
	}
	return false
}

func isMCPEcosystem(eco string) bool {
	switch strings.ToLower(strings.TrimSpace(eco)) {
	case "mcp", "mcpserver", "mcp-server":
		return true
	}
	return false
}

// sortCandidates orders candidates deterministically by
// (AssetID, VulnID, MatchBasis).
func sortCandidates(c []Candidate) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].AssetID != c[j].AssetID {
			return c[i].AssetID < c[j].AssetID
		}
		if c[i].VulnID != c[j].VulnID {
			return c[i].VulnID < c[j].VulnID
		}
		return c[i].MatchBasis < c[j].MatchBasis
	})
}

// asString coerces a Cypher-returned value to a trimmed string. Nil and
// non-string values yield "".
func asString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// asStringSlice coerces a Cypher-returned list property to a []string. The
// neo4j driver returns lists as []any; non-string elements are skipped.
func asStringSlice(v any) []string {
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}
