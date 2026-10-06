package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
)

// registerAdminRoutes wires user/role/project management under the authenticated
// group. Each route enforces the appropriate management permission.
func (s *Server) registerAdminRoutes(g *gin.RouterGroup) {
	// Users.
	users := g.Group("/users")
	users.GET("", s.auth.RequirePermission(auth.PermUsersRead), s.handleListUsers)
	users.POST("", s.auth.RequirePermission(auth.PermUsersManage), s.handleCreateUser)
	users.PATCH("/:id/active", s.auth.RequirePermission(auth.PermUsersManage), s.handleSetUserActive)

	// Roles.
	roles := g.Group("/roles")
	roles.GET("", s.auth.RequirePermission(auth.PermUsersRead), s.handleListRoles)
	roles.POST("", s.auth.RequirePermission(auth.PermRolesManage), s.handleCreateRole)
	g.GET("/permissions", s.auth.RequirePermission(auth.PermUsersRead), s.handleListPermissions)

	// Projects.
	projects := g.Group("/projects")
	projects.GET("", s.auth.RequirePermission(auth.PermProjectsRead), s.handleListProjects)
	projects.POST("", s.auth.RequirePermission(auth.PermProjectsManage), s.handleCreateProject)
}

func (s *Server) handleListUsers(c *gin.Context) {
	p, _ := auth.PrincipalFrom(c)
	users, err := s.auth.Store.ListUsers(c.Request.Context(), p.User.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func (s *Server) handleCreateUser(c *gin.Context) {
	p, _ := auth.PrincipalFrom(c)
	var body struct {
		Email      string  `json:"email"`
		Name       string  `json:"name"`
		Password   string  `json:"password"`
		RoleKey    string  `json:"role_key"`
		ProjectIDs []int64 `json:"project_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.Email == "" || body.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email and password are required"})
		return
	}
	id, err := s.auth.Store.CreateUser(c.Request.Context(), p.User.OrgID, auth.CreateUserInput{
		Email: body.Email, Name: body.Name, Password: body.Password,
		RoleKey: body.RoleKey, ProjectIDs: body.ProjectIDs,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (s *Server) handleSetUserActive(c *gin.Context) {
	p, _ := auth.PrincipalFrom(c)
	id := pathInt64(c, "id")
	var body struct {
		Active bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := s.auth.Store.SetUserActive(c.Request.Context(), p.User.OrgID, id, body.Active); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleListRoles(c *gin.Context) {
	p, _ := auth.PrincipalFrom(c)
	roles, err := s.auth.Store.ListRoles(c.Request.Context(), p.User.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func (s *Server) handleCreateRole(c *gin.Context) {
	p, _ := auth.PrincipalFrom(c)
	var body struct {
		Key         string   `json:"key"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		ScopeLevel  string   `json:"scope_level"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.ScopeLevel == "" {
		body.ScopeLevel = auth.ScopeProject
	}
	id, err := s.auth.Store.CreateCustomRole(c.Request.Context(), p.User.OrgID,
		body.Key, body.Name, body.Description, body.ScopeLevel, body.Permissions)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (s *Server) handleListPermissions(c *gin.Context) {
	perms, err := s.auth.Store.ListPermissions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Flatten to a stable JSON shape.
	out := make([]gin.H, 0, len(perms))
	for _, p := range perms {
		out = append(out, gin.H{"key": p.Key, "description": p.Description})
	}
	c.JSON(http.StatusOK, gin.H{"permissions": out})
}

func (s *Server) handleListProjects(c *gin.Context) {
	p, _ := auth.PrincipalFrom(c)
	projects, err := s.auth.Store.ListProjects(c.Request.Context(), p.User.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (s *Server) handleCreateProject(c *gin.Context) {
	p, _ := auth.PrincipalFrom(c)
	var body struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	id, err := s.auth.Store.CreateProject(c.Request.Context(), p.User.OrgID, body.Key, body.Name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func pathInt64(c *gin.Context, key string) int64 {
	var n int64
	for _, ch := range c.Param(key) {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}
