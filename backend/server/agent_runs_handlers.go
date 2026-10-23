package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file implements Seam 3 of the agents plane: the Runs seam
// (POST /api/agent/runs, GET /api/agent/runs/:id). Route registration lives in
// server.go (task 5); this file owns only the request/response types and the
// two handlers. Every org is resolved from the authenticated principal via the
// mcpOrg pattern and NEVER read from the request body, so a caller cannot act
// as, or read, another tenant (tenant isolation).

// RunTargetRef is the Runs-seam scan target: either a graph entity (addressed
// by canonical id) or an operator-supplied raw target (URL/IP).
//
// NOTE: a broader `TargetRef` is introduced elsewhere in this package by the
// graph-context seam (task 2.1). To stay independent of task-landing order and
// avoid a duplicate-type compile error, this file deliberately declares its own
// distinct `RunTargetRef` rather than referencing or redeclaring `TargetRef`.
type RunTargetRef struct {
	Kind        string `json:"kind"`                   // "entity" | "raw"
	CanonicalID string `json:"canonical_id,omitempty"` // kind:provider:native-id
	Raw         string `json:"raw,omitempty"`          // URL/IP for raw targets
}

// LaunchRequest is the body of POST /api/agent/runs. It carries no org field by
// design: the tenant is resolved from the authenticated principal.
type LaunchRequest struct {
	Target     RunTargetRef `json:"target"`
	AuthzScope *AuthzScope  `json:"authz_scope,omitempty"` // omitted => read-only reasoning only
}

// AuthzScope authorizes active testing for a run. An absent scope, or one with
// ActiveTesting=false, means active testing is OFF and the run is read-only
// graph reasoning. When ActiveTesting is true, AllowTargets must be non-empty.
type AuthzScope struct {
	ActiveTesting bool     `json:"active_testing"`
	AllowTargets  []string `json:"allow_targets"` // exact hosts/IPs/CIDRs the sandbox may hit
}

// RunStatus is the response of GET /api/agent/runs/:id.
type RunStatus struct {
	RunID         string `json:"run_id"`
	Status        string `json:"status"`         // queued|running|completed|failed
	FindingsCount int    `json:"findings_count"`
	ActiveTesting bool   `json:"active_testing"` // whether this run was authorized for it
}

// handleAgentLaunch accepts a scan launch request, persists an org-scoped run
// row, and returns 202 with the new run id. Active testing is opt-in: a scope
// that enables it MUST carry a non-empty allow list, otherwise the launch is
// rejected with 400 before anything is persisted.
func (s *Server) handleAgentLaunch(c *gin.Context) {
	org, ok := mcpOrg(c) // org from principal, NEVER from body
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var req LaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	// Opt-in active testing: active_testing=true with an empty allow list is a
	// bad request. An absent scope => read-only graph reasoning only.
	if req.AuthzScope != nil && req.AuthzScope.ActiveTesting && len(req.AuthzScope.AllowTargets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active testing requires allow_targets"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	runID, err := s.store.CreateAgentRun(ctx, org, launchToInput(req)) // persists org-scoped run row
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}
	// Hand off to the agents plane out-of-band (queue/notify); do not block.
	c.JSON(http.StatusAccepted, gin.H{"run_id": runID, "status": "queued"})
}

// handleAgentRunStatus returns a run's status scoped to the caller's org. A run
// belonging to another tenant, or a nonexistent run, both return 404 with the
// same message so the endpoint never discloses a run's existence across orgs.
func (s *Server) handleAgentRunStatus(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// GetAgentRun scopes by org: a run from another tenant is a not-found, so a
	// caller cannot read or probe another org's run (tenant isolation).
	st, err := s.store.GetAgentRun(ctx, org, c.Param("id"))
	if err != nil {
		if errors.Is(err, store.ErrAgentRunNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}
	c.JSON(http.StatusOK, RunStatus{
		RunID:         st.RunID,
		Status:        st.Status,
		FindingsCount: st.FindingsCount,
		ActiveTesting: st.ActiveTesting,
	})
}

// launchToInput maps the validated HTTP launch request onto the store-local
// input shape. Keeping the mapping here lets the store stay free of any
// dependency on the server package's request types.
func launchToInput(req LaunchRequest) store.AgentRunInput {
	in := store.AgentRunInput{
		Target: store.AgentRunTarget{
			Kind:        req.Target.Kind,
			CanonicalID: req.Target.CanonicalID,
			Raw:         req.Target.Raw,
		},
	}
	if req.AuthzScope != nil {
		in.Scope = &store.AgentRunScope{
			ActiveTesting: req.AuthzScope.ActiveTesting,
			AllowTargets:  req.AuthzScope.AllowTargets,
		}
	}
	return in
}
