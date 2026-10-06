package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const principalKey = "bellu_principal"

// Service bundles the auth store and token manager for use by HTTP handlers.
type Service struct {
	Store  *Store
	Tokens *TokenManager
}

// NewService builds an auth service.
func NewService(store *Store, tokens *TokenManager) *Service {
	return &Service{Store: store, Tokens: tokens}
}

// PrincipalFrom returns the authenticated principal from the Gin context.
func PrincipalFrom(c *gin.Context) (*Principal, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return nil, false
	}
	p, ok := v.(*Principal)
	return p, ok
}

// Authenticate is Gin middleware that requires a valid bearer token and loads
// the principal into the context.
func (s *Service) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := bearerToken(c.Request)
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		claims, err := s.Tokens.Verify(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		principal, err := s.Store.LoadPrincipal(c.Request.Context(), claims.UserID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "principal not found"})
			return
		}
		if !principal.User.Active {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user is inactive"})
			return
		}
		c.Set(principalKey, principal)
		c.Next()
	}
}

// RequirePermission is Gin middleware that only passes if the principal has the
// given permission. Use after Authenticate.
func (s *Service) RequirePermission(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalFrom(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if !p.Can(perm) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing permission: " + perm})
			return
		}
		c.Next()
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}
