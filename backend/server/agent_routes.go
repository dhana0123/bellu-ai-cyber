package server

import (
	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
)

// registerAgentRoutes mounts the agents-plane seams under /api/agent, inside the
// authenticated group (see Handler). It mirrors the existing group-by-permission
// pattern used for the graph:read and findings:read groups: each sub-group
// applies a single RequirePermission middleware and every handler resolves the
// tenant from the authenticated principal (via mcpOrg), never from client input.
//
// Two permission groups:
//   - agent:run gates the launch/status endpoints and the bounded graph-context
//     read the runner fetches at scan start.
//   - findings:ingest gates the write-back seam the plane uses to report findings.
//
// Note on the "/agent/findings:ingest" path: gin (httprouter) only treats ":" as
// the start of a named path parameter when it is the first character of a path
// segment. Here the colon sits mid-segment ("findings:ingest"), so gin registers
// it as a literal path, not a ":param". This matches the design's intended route
// and registers without adjustment.
func (s *Server) registerAgentRoutes(authed *gin.RouterGroup) {
	ar := authed.Group("")
	ar.Use(s.auth.RequirePermission(auth.PermAgentRun)) // "agent:run"
	{
		ar.POST("/agent/runs", s.handleAgentLaunch)
		ar.GET("/agent/runs/:id", s.handleAgentRunStatus)
		ar.GET("/agent/graph-context", s.handleAgentGraphContext)
	}

	fi := authed.Group("")
	fi.Use(s.auth.RequirePermission(auth.PermFindingsIngest)) // "findings:ingest"
	{
		fi.POST("/agent/findings:ingest", s.handleAgentFindingsIngest)
	}
}
