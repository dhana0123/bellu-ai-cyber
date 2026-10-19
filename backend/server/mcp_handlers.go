package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/mcp"
)

// msgTemporarilyUnavailable is the single generic message every MCP tool
// returns to the client when a backend store is unavailable or a projection
// read fails. The internal error detail is logged, never returned, so a client
// cannot learn backend topology or failure specifics from the response
// (Req 10.8).
const msgTemporarilyUnavailable = "temporarily unavailable"

// msgEntityNotFound is the single message get_entity returns for a lookup miss.
// A missing entity and an entity that belongs to another tenant resolve to the
// exact same string, so a client can never distinguish "does not exist for you"
// from "exists for someone else" and thus cannot probe cross-tenant existence
// (Req 10.7). It is a constant so both callers share one message by
// construction rather than by two independent string literals.
const msgEntityNotFound = "entity not found"

// mcpBackendError handles any error a tool method returns on the backend path:
// it logs the internal detail (which may name the projection, a store, or a
// query) under the tool name and responds with the generic
// "temporarily unavailable" message and a 503. The projection-unavailable
// sentinel and any other backend/store error collapse to the same client-facing
// message so cross-tenant or infrastructure detail never leaks (Req 10.8).
func mcpBackendError(c *gin.Context, tool string, err error) {
	if errors.Is(err, mcp.ErrProjectionUnavailable) {
		log.Printf("mcp: %s: projection unavailable: %v", tool, err)
	} else {
		log.Printf("mcp: %s: backend error: %v", tool, err)
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": msgTemporarilyUnavailable})
}

// registerMCPRoutes mounts the six MCP read tools under /api/mcp, inside the
// authenticated + graph:read group (see Handler). Each tool resolves the tenant
// from the authenticated principal and ignores any organization a client puts
// in the request body (Req 10.2, Req 10.3). When the MCP server is not wired
// (nil), the routes are not registered at all.
//
// The tenant resolution is the security-critical seam: mcpOrg reads
// p.User.OrgID off the Principal the auth middleware loaded, and that org — not
// a client-supplied value — is the one every tool method receives. No tool
// input type carries an org field, so a client has no channel to override it.
func (s *Server) registerMCPRoutes(rg *gin.RouterGroup) {
	if s.mcp == nil {
		return
	}
	g := rg.Group("/mcp")
	g.POST("/find_entities", s.handleMCPFindEntities)
	g.POST("/get_entity", s.handleMCPGetEntity)
	g.POST("/get_neighbors", s.handleMCPGetNeighbors)
	g.POST("/entity_history", s.handleMCPEntityHistory)
	g.POST("/recent_changes", s.handleMCPRecentChanges)
	g.POST("/describe_schema", s.handleMCPDescribeSchema)
}

// mcpOrg resolves the authenticated tenant's org id from the Gin context. It is
// the only source of org for every MCP tool: a client-supplied organization is
// never consulted. Returns (0, false) when there is no principal, in which case
// the handler responds 401 (the Authenticate middleware should already have
// rejected the request, so this is defense in depth).
func mcpOrg(c *gin.Context) (int64, bool) {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return 0, false
	}
	return p.User.OrgID, true
}

func (s *Server) handleMCPFindEntities(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var in mcp.FindEntitiesInput
	if err := bindMCPInput(c, &in); err != nil {
		return
	}
	ctx, cancel := mcpContext(c)
	defer cancel()
	out, err := s.mcp.FindEntities(ctx, org, in)
	if err != nil {
		mcpBackendError(c, "find_entities", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"entities": out})
}

func (s *Server) handleMCPGetEntity(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var in mcp.GetEntityInput
	if err := bindMCPInput(c, &in); err != nil {
		return
	}
	ctx, cancel := mcpContext(c)
	defer cancel()
	out, err := s.mcp.GetEntity(ctx, org, in)
	if err != nil {
		mcpBackendError(c, "get_entity", err)
		return
	}
	// A nil result is a lookup miss: the entity does not exist for the
	// authenticated tenant, OR it belongs to another tenant. Both collapse to
	// the same plain "entity not found" so a client can never distinguish a
	// cross-tenant entity from one that does not exist (Req 10.7).
	if out == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": msgEntityNotFound})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleMCPGetNeighbors(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var in mcp.GetNeighborsInput
	if err := bindMCPInput(c, &in); err != nil {
		return
	}
	ctx, cancel := mcpContext(c)
	defer cancel()
	out, err := s.mcp.GetNeighbors(ctx, org, in)
	if err != nil {
		mcpBackendError(c, "get_neighbors", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleMCPEntityHistory(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var in mcp.EntityHistoryInput
	if err := bindMCPInput(c, &in); err != nil {
		return
	}
	ctx, cancel := mcpContext(c)
	defer cancel()
	out, err := s.mcp.EntityHistory(ctx, org, in)
	if err != nil {
		mcpBackendError(c, "entity_history", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"changes": out})
}

func (s *Server) handleMCPRecentChanges(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var in mcp.RecentChangesInput
	if err := bindMCPInput(c, &in); err != nil {
		return
	}
	ctx, cancel := mcpContext(c)
	defer cancel()
	out, err := s.mcp.RecentChanges(ctx, org, in)
	if err != nil {
		mcpBackendError(c, "recent_changes", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"changes": out})
}

func (s *Server) handleMCPDescribeSchema(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	ctx, cancel := mcpContext(c)
	defer cancel()
	out, err := s.mcp.DescribeSchema(ctx, org)
	if err != nil {
		mcpBackendError(c, "describe_schema", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// bindMCPInput decodes an optional JSON body into the tool input. An empty body
// is allowed (all tool inputs have usable zero values), so only a malformed
// body is an error; on error it writes a 400 and returns the error so the
// handler can stop.
func bindMCPInput(c *gin.Context, dst any) error {
	if c.Request.Body == nil || c.Request.ContentLength == 0 {
		return nil
	}
	if err := c.ShouldBindJSON(dst); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return err
	}
	return nil
}

// mcpContext derives a bounded context for a single MCP tool call from the
// request context so a slow projection or log query cannot hang the handler.
func mcpContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 15*time.Second)
}
