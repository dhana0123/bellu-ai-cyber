package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/global-intel/database"
	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file implements the Findings and Intelligence read API (Requirement 13).
// The four handlers follow the existing handleEntities/handleStats shape: a
// per-request context timeout, a single store call, and a JSON response, with
// 500 on store errors, 400 on a bad path id, and 404 when nothing matches.
// Findings and intel reads go through the shared *store.Store the Server already
// holds, so no new Server dependency is introduced. Routing is wired separately
// (task 18.2); these methods exist and compile but need not yet be referenced.

// handleFindings serves GET /api/findings, returning findings filterable by
// severity, status, asset_kind, and limit (Requirement 13.1). Empty filter
// query params are omitted from the query; limit reuses the shared queryInt
// helper and defaults to 200.
func (s *Server) handleFindings(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	filter := finding.Filter{
		Severity:  canonical.Severity(c.Query("severity")),
		Status:    finding.Status(c.Query("status")),
		AssetKind: model.Kind(c.Query("asset_kind")),
		Limit:     queryInt(c, "limit", 200),
	}
	findings, err := finding.NewStore(s.store).List(ctx, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"findings": findings})
}

// handleFinding serves GET /api/findings/:id, returning the finding together
// with the canonical intelligence it cites and the evidence trail backing its
// asset (Requirement 13.2). A non-numeric :id is a 400; a missing finding is a
// 404. The cited intel is loaded by the finding's vuln_id, and the evidence
// trail is the read-only set of raw_events justifying the asset's graph
// presence.
func (s *Server) handleFinding(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid finding id"})
		return
	}

	f, err := finding.NewStore(s.store).Get(ctx, id)
	if errors.Is(err, finding.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "finding not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Cited intel: resolve the canonical vuln the finding references. A missing
	// intel row is not fatal for the detail view, so a lookup error yields a
	// nil vuln rather than failing the whole response.
	var vuln any
	if v, verr := database.New(s.store).VulnByID(ctx, f.VulnID); verr == nil {
		vuln = v
	}

	// Evidence trail: the raw_events backing the asset's presence in the graph.
	evidence, err := s.store.EvidenceForAsset(ctx, f.AssetID, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if evidence == nil {
		evidence = []store.EvidenceDetail{}
	}

	c.JSON(http.StatusOK, gin.H{
		"finding":  f,
		"intel":    vuln,
		"evidence": evidence,
	})
}

// handleIntelVulns serves GET /api/intel/vulnerabilities, returning canonical
// vulnerabilities filterable by severity, kev, and purl (Requirement 13.3).
// When purl is given the purl-keyed read is used; otherwise the full corpus is
// scanned. Severity and kev are applied in Go over the hydrated set.
func (s *Server) handleIntelVulns(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	intel := database.New(s.store)

	var (
		vulns []canonical.CanonicalVuln
		err   error
	)
	if purl := c.Query("purl"); purl != "" {
		vulns, err = intel.VulnsByPurl(ctx, purl)
	} else {
		vulns, err = intel.AllVulns(ctx)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	severity := canonical.Severity(c.Query("severity"))
	kevOnly, hasKEV := queryBool(c, "kev")

	out := make([]canonical.CanonicalVuln, 0, len(vulns))
	for _, v := range vulns {
		if severity != "" && v.Severity != severity {
			continue
		}
		if hasKEV && v.KnownExploited != kevOnly {
			continue
		}
		out = append(out, v)
	}

	c.JSON(http.StatusOK, gin.H{"vulnerabilities": out})
}

// handleIntelVuln serves GET /api/intel/vulnerabilities/:id, returning the
// canonical vulnerability with its aliases and affected ranges (Requirement
// 13.4). The hydrated CanonicalVuln already carries both, so the store read is
// returned directly. A missing vuln is a 404.
func (s *Server) handleIntelVuln(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid vulnerability id"})
		return
	}

	v, err := database.New(s.store).VulnByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "vulnerability not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vulnerability": v})
}

// queryBool parses an optional boolean query param. The second return reports
// whether the param was present and well-formed, so an absent or unparseable
// value leaves the corresponding filter unapplied.
func queryBool(c *gin.Context, key string) (val, ok bool) {
	v := c.Query(key)
	if v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}
