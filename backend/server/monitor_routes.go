package server

import (
	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
)

// registerMonitorRoutes mounts the continuous-monitoring seams inside the
// authenticated group (see Handler). It mirrors registerAgentRoutes'
// group-by-permission pattern: each sub-group applies a single
// RequirePermission middleware and every handler resolves the tenant from the
// authenticated principal (via mcpOrg), never from client input.
//
// Three permission groups:
//   - monitor:ingest gates the durable-first ingest seam the external taps use.
//   - findings:read gates the Mission Control events read.
//   - agent:run gates the agents-plane claim seam and the status write-back.
func (s *Server) registerMonitorRoutes(authed *gin.RouterGroup) {
	mi := authed.Group("")
	mi.Use(s.auth.RequirePermission(auth.PermMonitorIngest)) // "monitor:ingest"
	{
		mi.POST("/monitor/events", s.handleMonitorIngest)
	}

	mr := authed.Group("")
	mr.Use(s.auth.RequirePermission(auth.PermFindingsRead)) // "findings:read"
	{
		mr.GET("/monitor/events", s.handleMonitorEventsList)
		// Run detail + timeline reads are operator-facing observability, gated by
		// findings:read (Req 13.1-13.5, 20.3).
		mr.GET("/agent/runs/:id/detail", s.handleAgentRunDetail)
		mr.GET("/agent/runs/:id/timeline", s.handleAgentRunTimeline)
	}

	ac := authed.Group("")
	ac.Use(s.auth.RequirePermission(auth.PermAgentRun)) // "agent:run"
	{
		ac.POST("/agent/monitor/claim", s.handleMonitorClaim)
		ac.PUT("/agent/runs/:id/status", s.handleAgentRunStatusWrite)
		// Trace write-back is an agents-plane seam gated by agent:run (Req 12.1,
		// 20.2).
		ac.POST("/agent/runs/:id/trace", s.handleAgentRunTrace)
	}
}
