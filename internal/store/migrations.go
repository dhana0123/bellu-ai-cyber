package store

import "embed"

// MigrationsFS holds the embedded goose SQL migrations for the evidence store
// and (added in Task 4) the normalized tables.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
