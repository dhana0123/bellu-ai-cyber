package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Store is the auth/RBAC data access layer over the shared pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Seed makes the schema usable: it inserts all permissions, the built-in role
// catalog (global, org_id NULL) with their permissions, a default organization,
// and a default admin user if none exists. It is idempotent.
//
// The default admin credentials come from the caller (env), so they are never
// hard-coded. Returns whether an admin was created.
func (s *Store) Seed(ctx context.Context, adminEmail, adminPassword, orgName string) (createdAdmin bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	// 1) Permissions.
	permIDs := map[string]int64{}
	for _, p := range AllPermissions {
		var id int64
		err := tx.QueryRow(ctx,
			`INSERT INTO permissions (key, description) VALUES ($1,$2)
			 ON CONFLICT (key) DO UPDATE SET description = EXCLUDED.description
			 RETURNING id`, p.Key, p.Description).Scan(&id)
		if err != nil {
			return false, fmt.Errorf("seed permission %s: %w", p.Key, err)
		}
		permIDs[p.Key] = id
	}

	// 2) Built-in roles (org_id NULL) + their permissions.
	for _, r := range BuiltinRoles {
		var roleID int64
		err := tx.QueryRow(ctx,
			`INSERT INTO roles (org_id, key, name, description, builtin, scope_level)
			 VALUES (NULL, $1, $2, $3, true, $4)
			 ON CONFLICT (org_id, key) DO UPDATE SET name = EXCLUDED.name,
			   description = EXCLUDED.description, scope_level = EXCLUDED.scope_level
			 RETURNING id`, r.Key, r.Name, r.Description, r.ScopeLevel).Scan(&roleID)
		if err != nil {
			return false, fmt.Errorf("seed role %s: %w", r.Key, err)
		}
		for _, pk := range r.Permissions {
			if _, err := tx.Exec(ctx,
				`INSERT INTO role_permissions (role_id, permission_id) VALUES ($1,$2)
				 ON CONFLICT DO NOTHING`, roleID, permIDs[pk]); err != nil {
				return false, fmt.Errorf("seed role_permission %s/%s: %w", r.Key, pk, err)
			}
		}
	}

	// 3) Default organization.
	var orgID int64
	err = tx.QueryRow(ctx, `SELECT id FROM organizations ORDER BY id LIMIT 1`).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx,
			`INSERT INTO organizations (name) VALUES ($1) RETURNING id`, orgName).Scan(&orgID); err != nil {
			return false, fmt.Errorf("seed org: %w", err)
		}
	} else if err != nil {
		return false, err
	}

	// 4) Default admin user (only if there are no users yet).
	var userCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err != nil {
		return false, err
	}
	if userCount == 0 && adminEmail != "" && adminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
		if err != nil {
			return false, err
		}
		var adminID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO users (org_id, email, name, password_hash, active)
			 VALUES ($1,$2,'Administrator',$3,true) RETURNING id`,
			orgID, adminEmail, string(hash)).Scan(&adminID); err != nil {
			return false, fmt.Errorf("seed admin: %w", err)
		}
		// Global Admin membership (org-wide, no project).
		var gaID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM roles WHERE key=$1 AND builtin`, RoleGlobalAdmin).Scan(&gaID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO memberships (user_id, role_id, project_id) VALUES ($1,$2,NULL)
			 ON CONFLICT DO NOTHING`, adminID, gaID); err != nil {
			return false, err
		}
		createdAdmin = true
	}

	return createdAdmin, tx.Commit(ctx)
}

// Authenticate verifies an email/password and returns the user on success.
func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, email, name, active, password_hash FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.Active, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, ErrInactiveUser
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return &u, nil
}

// LoadPrincipal resolves a user's full authorization context: memberships,
// effective permissions (union across roles), and accessible project ids.
func (s *Store) LoadPrincipal(ctx context.Context, userID int64) (*Principal, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, email, name, active FROM users WHERE id = $1`, userID).
		Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	p := &Principal{
		User:        u,
		Permissions: map[string]bool{},
		ProjectIDs:  map[int64]bool{},
	}

	rows, err := s.pool.Query(ctx, `
		SELECT r.key, r.name, r.scope_level, m.project_id, COALESCE(pr.key, '')
		FROM memberships m
		JOIN roles r ON r.id = m.role_id
		LEFT JOIN projects pr ON pr.id = m.project_id
		WHERE m.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roleKeys := map[string]bool{}
	for rows.Next() {
		var m Membership
		var projID *int64
		if err := rows.Scan(&m.RoleKey, &m.RoleName, &m.ScopeLevel, &projID, &m.ProjectKey); err != nil {
			return nil, err
		}
		m.ProjectID = projID
		p.Memberships = append(p.Memberships, m)
		roleKeys[m.RoleKey] = true
		if m.ScopeLevel == ScopeGlobal {
			p.HasGlobal = true
		}
		if projID != nil {
			p.ProjectIDs[*projID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Effective permissions = union of permissions across the user's roles.
	prows, err := s.pool.Query(ctx, `
		SELECT DISTINCT perm.key
		FROM memberships m
		JOIN role_permissions rp ON rp.role_id = m.role_id
		JOIN permissions perm ON perm.id = rp.permission_id
		WHERE m.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var k string
		if err := prows.Scan(&k); err != nil {
			return nil, err
		}
		p.Permissions[k] = true
	}
	return p, prows.Err()
}

// Sentinel errors.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInactiveUser       = errors.New("user is inactive")
	ErrUserNotFound       = errors.New("user not found")
)
