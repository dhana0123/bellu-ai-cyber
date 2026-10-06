// Package auth implements the Wiz-style authorization model for Bellu:
// Organization -> Users -> Roles + Groups -> Permissions -> Projects (scope)
// -> graph objects. It covers password auth, JWT issuance, the built-in role
// catalog, and permission/scope checks.
package auth

// Permission keys. These are the atomic capabilities roles grant.
const (
	PermGraphRead     = "graph:read"      // view the AI security graph
	PermFindingsRead  = "findings:read"   // view vulnerabilities/findings
	PermInvestigate   = "investigate"     // explain edges, trace evidence
	PermUsersRead     = "users:read"      // view users
	PermUsersManage   = "users:manage"    // create/update/deactivate users
	PermRolesManage   = "roles:manage"    // create custom roles
	PermProjectsRead  = "projects:read"   // view projects
	PermProjectsManage = "projects:manage" // create/edit projects
	PermPoliciesManage = "policies:manage" // modify detection policies
	PermIntegrations   = "integrations:manage"
)

// AllPermissions is the catalog seeded into the permissions table.
var AllPermissions = []struct {
	Key, Description string
}{
	{PermGraphRead, "View the AI security graph"},
	{PermFindingsRead, "View vulnerabilities and findings"},
	{PermInvestigate, "Investigate alerts and trace evidence"},
	{PermUsersRead, "View users"},
	{PermUsersManage, "Create, update and deactivate users"},
	{PermRolesManage, "Create and edit custom roles"},
	{PermProjectsRead, "View projects"},
	{PermProjectsManage, "Create and edit projects"},
	{PermPoliciesManage, "Modify detection policies"},
	{PermIntegrations, "Manage integrations"},
}

// Role scope levels.
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// Built-in role keys.
const (
	RoleGlobalAdmin   = "global_admin"
	RoleGlobalReader  = "global_reader"
	RoleProjectAdmin  = "project_admin"
	RoleSecurityAnalyst = "security_analyst"
	RoleDeveloper     = "developer"
	RoleViewer        = "viewer"
)

// BuiltinRole describes a seeded role and the permissions it grants.
type BuiltinRole struct {
	Key         string
	Name        string
	Description string
	ScopeLevel  string
	Permissions []string
}

// BuiltinRoles is the catalog of roles seeded for every tenant, mirroring
// Wiz's model (global admin/reader + project-scoped roles + custom later).
var BuiltinRoles = []BuiltinRole{
	{
		Key: RoleGlobalAdmin, Name: "Global Admin", ScopeLevel: ScopeGlobal,
		Description: "Tenant-wide administration: manage users, roles, projects, policies and integrations; see everything.",
		Permissions: []string{
			PermGraphRead, PermFindingsRead, PermInvestigate,
			PermUsersRead, PermUsersManage, PermRolesManage,
			PermProjectsRead, PermProjectsManage, PermPoliciesManage, PermIntegrations,
		},
	},
	{
		Key: RoleGlobalReader, Name: "Global Reader", ScopeLevel: ScopeGlobal,
		Description: "Tenant-wide read-only access to the graph and findings.",
		Permissions: []string{PermGraphRead, PermFindingsRead, PermProjectsRead, PermUsersRead},
	},
	{
		Key: RoleProjectAdmin, Name: "Project Admin", ScopeLevel: ScopeProject,
		Description: "Administrative access within assigned projects.",
		Permissions: []string{
			PermGraphRead, PermFindingsRead, PermInvestigate,
			PermProjectsRead, PermUsersRead,
		},
	},
	{
		Key: RoleSecurityAnalyst, Name: "Security Analyst", ScopeLevel: ScopeProject,
		Description: "Investigate the graph and findings within assigned projects.",
		Permissions: []string{PermGraphRead, PermFindingsRead, PermInvestigate, PermProjectsRead},
	},
	{
		Key: RoleDeveloper, Name: "Developer", ScopeLevel: ScopeProject,
		Description: "View assigned applications: agents, models, dependencies and findings.",
		Permissions: []string{PermGraphRead, PermFindingsRead, PermProjectsRead},
	},
	{
		Key: RoleViewer, Name: "Viewer", ScopeLevel: ScopeProject,
		Description: "Read-only access to assigned projects.",
		Permissions: []string{PermGraphRead, PermProjectsRead},
	},
}

// User is an authenticated principal.
type User struct {
	ID       int64  `json:"id"`
	OrgID    int64  `json:"org_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Active   bool   `json:"active"`
}

// Membership binds a user to a role, optionally scoped to a project.
type Membership struct {
	RoleKey    string `json:"role_key"`
	RoleName   string `json:"role_name"`
	ScopeLevel string `json:"scope_level"`
	ProjectID  *int64 `json:"project_id,omitempty"`
	ProjectKey string `json:"project_key,omitempty"`
}

// Principal is the resolved identity used for authorization: the user plus the
// effective permissions and the set of project ids they can access.
type Principal struct {
	User        User            `json:"user"`
	Memberships []Membership    `json:"memberships"`
	Permissions map[string]bool `json:"-"`
	// ProjectIDs the principal is scoped to. Empty + HasGlobal means all.
	ProjectIDs map[int64]bool `json:"-"`
	HasGlobal  bool           `json:"-"`
}

// Can reports whether the principal has a permission.
func (p *Principal) Can(perm string) bool {
	return p.Permissions[perm]
}

// CanAccessProject reports whether the principal may see a project. Global
// roles see every project.
func (p *Principal) CanAccessProject(projectID int64) bool {
	if p.HasGlobal {
		return true
	}
	return p.ProjectIDs[projectID]
}
