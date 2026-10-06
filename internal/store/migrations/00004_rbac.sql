-- +goose Up
-- Wiz-style RBAC: Organization -> Users -> Roles+Groups -> Permissions ->
-- Projects (scope) -> graph objects.

CREATE TABLE organizations (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    org_id        BIGINT NOT NULL REFERENCES organizations(id),
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    active        BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- roles: built-in (builtin=true, cannot be deleted) + custom per org.
CREATE TABLE roles (
    id          BIGSERIAL PRIMARY KEY,
    org_id      BIGINT REFERENCES organizations(id),  -- NULL for built-in
    key         TEXT NOT NULL,                         -- e.g. global_admin
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    builtin     BOOLEAN NOT NULL DEFAULT false,
    -- global roles apply tenant-wide; project roles are scoped to projects.
    scope_level TEXT NOT NULL DEFAULT 'project',       -- 'global' | 'project'
    CONSTRAINT roles_key_org_uniq UNIQUE (org_id, key)
);

CREATE TABLE permissions (
    id          BIGSERIAL PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE,                  -- e.g. graph:read
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE role_permissions (
    role_id       BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE groups (
    id     BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES organizations(id),
    name   TEXT NOT NULL,
    CONSTRAINT groups_name_org_uniq UNIQUE (org_id, name)
);

CREATE TABLE group_members (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

-- projects group cloud/AI resources by business purpose; the unit of scope.
CREATE TABLE projects (
    id         BIGSERIAL PRIMARY KEY,
    org_id     BIGINT NOT NULL REFERENCES organizations(id),
    key        TEXT NOT NULL,                          -- stable slug
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT projects_key_org_uniq UNIQUE (org_id, key)
);

-- memberships bind a user to a role, optionally scoped to a project.
-- project_id NULL means the membership applies org-wide (for global roles).
CREATE TABLE memberships (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    BIGINT NOT NULL REFERENCES roles(id),
    project_id BIGINT REFERENCES projects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT memberships_uniq UNIQUE (user_id, role_id, project_id)
);

CREATE INDEX memberships_user_idx ON memberships (user_id);

-- Optional: tag a normalized entity with a project for scoped graph reads.
-- V1 leaves this empty (all-projects); the hook exists for later enforcement.
CREATE TABLE entity_projects (
    canonical_id TEXT NOT NULL,
    project_id   BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (canonical_id, project_id)
);

-- +goose Down
DROP TABLE entity_projects;
DROP TABLE memberships;
DROP TABLE projects;
DROP TABLE group_members;
DROP TABLE groups;
DROP TABLE role_permissions;
DROP TABLE permissions;
DROP TABLE roles;
DROP TABLE users;
DROP TABLE organizations;
