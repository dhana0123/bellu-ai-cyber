package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/monitor"
	"github.com/bellu/bellu-ai-cyber/internal/store"
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

	// Wire the vulnerability delta tap: an agent-ingested finding that is a
	// nothing->open transition (UpsertResult.Created == true) emits exactly one
	// monitor event; a re-ingest of a still-open finding emits nothing
	// (Requirements 4.5, 4.6). The shared *store.Store is the MonitorEmitter, so
	// this path and the sweep get the delta through the same upsert helper with
	// no duplicated logic. The emit is best-effort inside UpsertFinding.
	store := finding.NewStore(s.store).WithMonitorEmitter(s.store)

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

// This block implements seam 3 of the agents plane: the finding-report
// write-back endpoint POST /api/agent/findings/reports (Phase 3, Req 14/15).
// Where findings:ingest carries thin finding fields, this seam carries the
// agent's ACTUAL generated report artifact — structured columns, the long-form
// JSONB sections, and the plane's rendered markdown — and stores it per
// (finding_id, team) via store.UpsertFindingReport (Req 14.1, 14.2, 15.1).
//
// Two safety invariants hold here, identical to every other new seam:
//   - Tenant isolation (P1, Req 19.1/19.2): the org is resolved from the
//     authenticated principal via mcpOrg; a finding that does not exist for the
//     caller is a 404 with no existence disclosure.
//   - PoC redaction (P6, Req 16.2/16.3): the store defensively strips any
//     poc_script_code key from Sections at any nesting level, so the weaponized
//     body is never persisted even if a caller supplies one. Only the
//     poc_available flag is stored.

// ReportIngestRequest is the report write-back body. The finding the report
// attaches to is identified EITHER by a numeric finding_id directly, OR by the
// (asset_id, vuln_id) dedup key the agent just ingested, which the handler
// resolves to the finding_id. Org is NEVER read from the body — it is resolved
// from the authenticated principal (Req 19.1).
type ReportIngestRequest struct {
	// FindingID targets the finding directly by its surrogate id. When zero, the
	// handler resolves the finding from AssetID+VulnID instead.
	FindingID int64 `json:"finding_id,omitempty"`
	// AssetID + VulnID resolve the finding by its (asset_id, vuln_id) dedup key
	// when FindingID is not supplied (Req 15 — report against the just-ingested
	// finding without a round-trip to learn its numeric id).
	AssetID string `json:"asset_id,omitempty"`
	VulnID  string `json:"vuln_id,omitempty"`
	// RunID links the report to the auto-triggered run that produced it; empty
	// stores SQL NULL (run_id is nullable).
	RunID string `json:"run_id,omitempty"`
	// Team is the explicit report classifier 'red' or 'blue' (Req 15.2). When
	// empty, the team is derived from the reporting agent's stage (Req 15.3).
	Team string `json:"team,omitempty"`
	// Stage / AgentName drive team derivation when Team is empty: Stage is a
	// stage string taken as-is, else AgentName is mapped to a stage via
	// monitor.MapStage (Req 15.3).
	Stage     string `json:"stage,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	// Title / Severity back the list/badges.
	Title    string `json:"title,omitempty"`
	Severity string `json:"severity,omitempty"`
	// PoCAvailable is the flag only; the weaponized body is NEVER transported
	// (Req 16.1). Sections carries the long-form structured content (the store
	// strips any poc_script_code key defensively, Req 16.2).
	PoCAvailable     bool            `json:"poc_available,omitempty"`
	Sections         json.RawMessage `json:"sections,omitempty"`
	RenderedMarkdown string          `json:"rendered_markdown,omitempty"`
}

// handleAgentFindingReportIngest accepts one report artifact and upserts it per
// (finding_id, team) (Req 14.1, 14.2, 15.1). Org comes from the authenticated
// principal only (Req 19.1); a missing principal is a 401. The finding is
// resolved by finding_id or by (asset_id, vuln_id); a finding that does not
// exist for the caller is a 404 with no existence disclosure (Req 15.4, 19.2).
// The team is the caller's explicit 'red'/'blue' when valid, else derived from
// the reporting agent's stage (Req 15.2, 15.3). A store failure collapses to a
// generic temporarily-unavailable so no backend detail leaks (Req 21.2).
func (s *Server) handleAgentFindingReportIngest(c *gin.Context) {
	org, ok := mcpOrg(c) // tenant from principal; body org (if any) ignored (Req 19.1)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req ReportIngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Resolve the target finding id. A direct finding_id wins; otherwise resolve
	// it from the (asset_id, vuln_id) dedup key. A report with neither is a 400.
	findingID := req.FindingID
	if findingID == 0 {
		if req.AssetID == "" || req.VulnID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "need finding_id or asset_id and vuln_id"})
			return
		}
		id, err := finding.NewStore(s.store).GetIDByKey(ctx, req.AssetID, req.VulnID)
		if errors.Is(err, finding.ErrNotFound) {
			// No existence disclosure: a finding in another org (or none at all)
			// is indistinguishable from a miss (Req 15.4, 19.2).
			c.JSON(http.StatusNotFound, gin.H{"error": "finding not found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
			return
		}
		findingID = id
	}

	team := reportTeam(req)

	r := store.FindingReport{
		FindingID:        findingID,
		RunID:            req.RunID,
		Team:             team,
		Title:            req.Title,
		Severity:         req.Severity,
		PoCAvailable:     req.PoCAvailable,
		Sections:         req.Sections, // poc_script_code stripped at any depth by the store (P6)
		RenderedMarkdown: req.RenderedMarkdown,
	}

	// UpsertFindingReport is gated through the findings table: a finding_id that
	// does not exist produces no row (INSERT ... SELECT matches nothing), so a
	// write-back against a finding in another org is a silent no-op with no
	// existence disclosure. A direct-finding_id caller therefore cannot learn a
	// cross-org finding exists; the 200 below reports the stored team only.
	if err := s.store.UpsertFindingReport(ctx, org, r); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"finding_id": findingID,
		"team":       team,
	})
}

// reportTeam classifies a report into 'red' or 'blue' (Req 15.2, 15.3).
//
// An explicit Team of 'red' or 'blue' is honored verbatim (Req 15.2). Otherwise
// the team is derived from the reporting agent's stage (Req 15.3): a supplied
// Stage string is mapped if present, else AgentName is mapped via
// monitor.MapStage. EXPLOIT maps to 'red'; VERIFY, STABILITY, and REPORT map to
// 'blue'. Any other stage (or no stage signal at all) defaults to 'blue', the
// safe remediation-facing side, so a report is always classifiable.
func reportTeam(req ReportIngestRequest) string {
	switch req.Team {
	case "red", "blue":
		return req.Team
	}

	stage := monitor.Stage(req.Stage)
	if req.Stage == "" {
		// No explicit stage: derive it from the reporting agent name (Req 15.3).
		stage = monitor.MapStage(req.AgentName)
	}
	return teamForStage(stage)
}

// teamForStage maps a pipeline stage to its report team (Req 15.3): EXPLOIT is
// the active/offensive side ('red'); VERIFY, STABILITY, and REPORT are the
// remediation/defensive side ('blue'). Every other stage defaults to 'blue'.
func teamForStage(stage monitor.Stage) string {
	switch stage {
	case monitor.StageExploit:
		return "red"
	case monitor.StageVerify, monitor.StageStability, monitor.StageReport:
		return "blue"
	default:
		return "blue"
	}
}
