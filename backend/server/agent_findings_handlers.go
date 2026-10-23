package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// This file implements seam 2 of the agents plane: the findings write-back
// endpoint POST /api/agent/findings:ingest (Requirement 6). Agent-produced
// findings flow back into Bellu's existing finding model here — linked to a
// graph entity when one applies, or attached to an org-scoped synthetic asset
// ref when the operator-supplied raw target has no graph entity.
//
// The handler mirrors the MCP seam's tenant discipline: the org is resolved
// from the authenticated principal via mcpOrg and any org in the request body
// is ignored (Requirement 6.1), so the agents plane can only write within its
// own tenant. Route registration is wired separately (task 5); this method
// exists and compiles but need not yet be referenced.

// IngestRequest is a batch of agent-produced findings. Org is NEVER read from
// the body — it is resolved from the authenticated service-account principal
// (Requirement 6.1), and the request type carries no org field by construction
// (Requirement 9.6).
type IngestRequest struct {
	RunID    string          `json:"run_id"`
	Findings []IngestFinding `json:"findings"`
}

// IngestFinding is one finding in the agent's vocabulary. The handler maps it
// onto finding.Finding and upserts per (AssetID, VulnID).
type IngestFinding struct {
	// AssetID is a graph canonical_id for a linked asset, OR empty for an
	// operator-supplied raw target with no graph entity (synthetic asset).
	AssetID string `json:"asset_id,omitempty"`
	// AssetKind must pass model.ValidKind when AssetID is set (Requirement 6.3).
	AssetKind string `json:"asset_kind,omitempty"`
	// RawTarget is set when AssetID is empty (Requirement 6.5).
	RawTarget string  `json:"raw_target,omitempty"`
	VulnID    string  `json:"vuln_id"`
	IntelRef  string  `json:"intel_ref,omitempty"`
	RiskScore float64 `json:"risk_score"`
	Severity  string  `json:"severity"`
}

// handleAgentFindingsIngest accepts a batch of agent findings, maps and upserts
// each one, and reports the created/updated/rejected counts (Requirement 6.10).
//
// Org comes from the authenticated principal only; any org in the body is
// ignored (Requirement 6.1). A missing principal is a 401 (Requirement 6.2) and
// an unparseable body is a 400. Per-finding validation failures (bad kind,
// missing identifiers) increment the rejected count and continue the batch
// rather than aborting it (Requirements 6.4, 6.6, 6.7); only a store failure
// stops the batch, collapsing to the generic temporarily-unavailable message so
// no backend detail leaks.
func (s *Server) handleAgentFindingsIngest(c *gin.Context) {
	org, ok := mcpOrg(c) // tenant from principal; body org (if any) ignored
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req IngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	// An empty batch has nothing to ingest and is almost certainly a caller
	// error, so it is a 400 rather than a trivially-successful 0/0/0 response.
	if len(req.Findings) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no findings in request"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	store := finding.NewStore(s.store)

	var created, updated, rejected int
	// rejections carries a per-finding reason for each skipped finding so the
	// caller can tell *why* a finding was dropped without the counts alone.
	rejections := make([]gin.H, 0)

	for i, in := range req.Findings {
		f, err := mapIngestFinding(org, req.RunID, in)
		if err != nil {
			rejected++ // validation / allow-list failure: skip, keep the batch going
			rejections = append(rejections, gin.H{"index": i, "reason": err.Error()})
			continue
		}
		// Upsert keyed on (asset_id, vuln_id): the findings_uniq constraint makes
		// repeated ingests idempotent except for last_seen (Property 2).
		res, err := store.UpsertFinding(ctx, org, f)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
			return
		}
		if res.Created {
			created++
		} else {
			updated++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"created":    created,
		"updated":    updated,
		"rejected":   rejected,
		"rejections": rejections,
	})
}

// mapIngestFinding translates an agent finding into a finding.Finding,
// enforcing the allow-list on asset kind and resolving linked vs synthetic
// assets. It returns an error (never a partial Finding) for any finding that
// must be rejected, so the handler can increment the rejected count and move on.
func mapIngestFinding(org int64, runID string, in IngestFinding) (finding.Finding, error) {
	if in.VulnID == "" {
		return finding.Finding{}, errors.New("missing vuln_id") // Requirement 6.7
	}

	f := finding.Finding{
		VulnID:    in.VulnID,
		IntelRef:  in.IntelRef,
		RiskScore: clamp(in.RiskScore, 0, 100), // Requirement 6.9
		Severity:  canonical.Severity(in.Severity),
		Status:    finding.StatusOpen,
	}

	if in.AssetID != "" {
		// Linked asset: kind MUST be in the model allow-list, else reject
		// (Requirements 6.3, 6.4 / correctness Property 3).
		k := model.Kind(in.AssetKind)
		if !model.ValidKind(k) {
			return finding.Finding{}, fmt.Errorf("disallowed asset_kind %q", in.AssetKind)
		}
		f.AssetID = in.AssetID
		f.AssetKind = k
		return f, nil
	}

	// Unlinked: operator-supplied raw target with no graph entity. Attach a
	// synthetic, org-scoped asset ref so the finding is still unique and never
	// collides across orgs or with real canonical ids (Requirement 6.5).
	if in.RawTarget == "" {
		return finding.Finding{}, errors.New("need asset_id or raw_target") // Requirement 6.6
	}
	f.AssetID = syntheticAssetID(org, runID, in.RawTarget)
	f.AssetKind = model.KindExternalService
	return f, nil
}

// syntheticAssetID builds an org-scoped, collision-resistant asset id for a raw
// target that has no graph entity (Requirement 6.5). The org is embedded in the
// id so the same raw target scanned by two tenants yields two distinct ids, and
// the "synthetic:" prefix keeps it from ever colliding with a real canonical id
// of the form kind:provider:native-id. runID and rawTarget are hashed together
// so the same (org, run, target) maps stably to one id — repeated ingests dedup
// on it — while differing only in runID still separates runs.
func syntheticAssetID(org int64, runID, rawTarget string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", org, runID, rawTarget)))
	return fmt.Sprintf("synthetic:%d:%s", org, hex.EncodeToString(sum[:]))
}

// clamp constrains v to the inclusive range [lo, hi]. It is used to keep an
// ingested risk_score within [0,100] (Requirement 6.9) regardless of what the
// agent reported.
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
