package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
)

// handleLogin authenticates email/password and returns a JWT + the principal.
func (s *Server) handleLogin(c *gin.Context) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	u, err := s.auth.Store.Authenticate(c.Request.Context(), body.Email, body.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	token, exp, err := s.auth.Tokens.Issue(u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	principal, err := s.auth.Store.LoadPrincipal(c.Request.Context(), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":       token,
		"expires_at":  exp,
		"user":        principal.User,
		"memberships": principal.Memberships,
		"permissions": permissionList(principal),
	})
}

// handleMe returns the current principal.
func (s *Server) handleMe(c *gin.Context) {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user":        p.User,
		"memberships": p.Memberships,
		"permissions": permissionList(p),
	})
}

func permissionList(p *auth.Principal) []string {
	out := make([]string, 0, len(p.Permissions))
	for k, v := range p.Permissions {
		if v {
			out = append(out, k)
		}
	}
	return out
}
