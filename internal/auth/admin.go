package auth

import (
	"context"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// --- Users ---

// UserDetail is a user plus their role/project assignments for admin listing.
type UserDetail struct {
	User
	Roles    []string `json:"roles"`
	Projects []string `json:"projects"`
}

// ListUsers returns all users in an org with their role and project keys.
func (s *Store) ListUsers(ctx context.Context, orgID int64) ([]UserDetail, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, email, name, active FROM users WHERE org_id = $1 ORDER BY email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserDetail
	byID := map[int64]*UserDetail{}
	for rows.Next() {
		var u UserDetail
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.Active); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}

	mrows, err := s.pool.Query(ctx, `
		SELECT m.user_id, r.name, COALESCE(pr.key, '')
		FROM memberships m
		JOIN roles r ON r.id = m.role_id
		LEFT JOIN projects pr ON pr.id = m.project_id
		JOIN users u ON u.id = m.user_id
		WHERE u.org_id = $1`, orgID)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var uid int64
		var roleName, projKey string
		if err := mrows.Scan(&uid, &roleName, &projKey); err != nil {
			return nil, err
		}
		if ud := byID[uid]; ud != nil {
			ud.Roles = appendUnique(ud.Roles, roleName)
			if projKey != "" {
				ud.Projects = appendUnique(ud.Projects, projKey)
			}
		}
	}
	return out, mrows.Err()
}

// CreateUserInput describes a new user and their initial assignment.
type CreateUserInput struct {
	Email      string
	Name       string
	Password   string
	RoleKey    string  // built-in or custom role key
	ProjectIDs []int64 // optional project scope; empty = org-wide
}

// CreateUser creates a user and assigns the given role, scoped to the given
// projects (or org-wide when none are supplied).
func (s *Store) CreateUser(ctx context.Context, orgID int64, in CreateUserInput) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var userID int64
	err = tx.QueryRow(ctx,
		`INSERT INTO users (org_id, email, name, password_hash, active)
		 VALUES ($1,$2,$3,$4,true) RETURNING id`,
		orgID, in.Email, in.Name, string(hash)).Scan(&userID)
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}

	if in.RoleKey != "" {
		var roleID int64
		if err := tx.QueryRow(ctx,
			`SELECT id FROM roles WHERE key=$1 AND (org_id=$2 OR org_id IS NULL) ORDER BY org_id NULLS LAST LIMIT 1`,
			in.RoleKey, orgID).Scan(&roleID); err != nil {
			return 0, fmt.Errorf("resolve role %q: %w", in.RoleKey, err)
		}
		if len(in.ProjectIDs) == 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO memberships (user_id, role_id, project_id) VALUES ($1,$2,NULL)
				 ON CONFLICT DO NOTHING`, userID, roleID); err != nil {
				return 0, err
			}
		} else {
			for _, pid := range in.ProjectIDs {
				if _, err := tx.Exec(ctx,
					`INSERT INTO memberships (user_id, role_id, project_id) VALUES ($1,$2,$3)
					 ON CONFLICT DO NOTHING`, userID, roleID, pid); err != nil {
					return 0, err
				}
			}
		}
	}
	return userID, tx.Commit(ctx)
}

// SetUserActive activates or deactivates a user.
func (s *Store) SetUserActive(ctx context.Context, orgID, userID int64, active bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET active=$1 WHERE id=$2 AND org_id=$3`, active, userID, orgID)
	return err
}

// --- Roles ---

// RoleDetail is a role with its permission keys.
type RoleDetail struct {
	ID          int64    `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	ScopeLevel  string   `json:"scope_level"`
	Permissions []string `json:"permissions"`
}

// ListRoles returns built-in roles plus the org's custom roles.
func (s *Store) ListRoles(ctx context.Context, orgID int64) ([]RoleDetail, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.key, r.name, r.description, r.builtin, r.scope_level,
		       COALESCE(array_agg(p.key) FILTER (WHERE p.key IS NOT NULL), '{}')
		FROM roles r
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		LEFT JOIN permissions p ON p.id = rp.permission_id
		WHERE r.org_id IS NULL OR r.org_id = $1
		GROUP BY r.id ORDER BY r.builtin DESC, r.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleDetail
	for rows.Next() {
		var r RoleDetail
		if err := rows.Scan(&r.ID, &r.Key, &r.Name, &r.Description, &r.Builtin, &r.ScopeLevel, &r.Permissions); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateCustomRole creates an org-scoped custom role with the given permissions.
func (s *Store) CreateCustomRole(ctx context.Context, orgID int64, key, name, desc, scopeLevel string, perms []string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var roleID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO roles (org_id, key, name, description, builtin, scope_level)
		 VALUES ($1,$2,$3,$4,false,$5) RETURNING id`,
		orgID, key, name, desc, scopeLevel).Scan(&roleID); err != nil {
		return 0, err
	}
	for _, pk := range perms {
		if _, err := tx.Exec(ctx,
			`INSERT INTO role_permissions (role_id, permission_id)
			 SELECT $1, id FROM permissions WHERE key = $2`, roleID, pk); err != nil {
			return 0, err
		}
	}
	return roleID, tx.Commit(ctx)
}

// --- Projects ---

// Project is a scope unit.
type Project struct {
	ID   int64  `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// ListProjects returns the org's projects.
func (s *Store) ListProjects(ctx context.Context, orgID int64) ([]Project, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, key, name FROM projects WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Key, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateProject creates a project.
func (s *Store) CreateProject(ctx context.Context, orgID int64, key, name string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO projects (org_id, key, name) VALUES ($1,$2,$3) RETURNING id`,
		orgID, key, name).Scan(&id)
	return id, err
}

// ListPermissions returns the permission catalog.
func (s *Store) ListPermissions(ctx context.Context) ([]struct {
	Key, Description string
}, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, description FROM permissions ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct{ Key, Description string }
	for rows.Next() {
		var k, d string
		if err := rows.Scan(&k, &d); err != nil {
			return nil, err
		}
		out = append(out, struct{ Key, Description string }{k, d})
	}
	return out, rows.Err()
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
