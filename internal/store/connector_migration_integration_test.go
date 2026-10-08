//go:build integration

package store

import (
	"context"
	"testing"
)

// TestConnectorMigrationApplies asserts the 00007_connector migration applies
// cleanly on top of 00001-00006 and creates the four connector_* tables.
// newTestStore (store_integration_test.go) runs Store.Migrate, which applies
// every embedded migration including 00007.
func TestConnectorMigrationApplies(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	wantTables := []string{
		"connector_connections",
		"connector_capabilities",
		"connector_verification_results",
		"connector_discovery_sessions",
	}
	for _, tbl := range wantTables {
		var exists bool
		err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			  WHERE table_schema = 'public' AND table_name = $1)`, tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("expected table %s to exist after migration", tbl)
		}
	}
}

// TestConnectorConnectionsColumnsAndFKs asserts the connections table has the
// expected non-secret columns and FK references to organizations/projects.
func TestConnectorConnectionsColumnsAndFKs(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	wantCols := []string{
		"id", "org_id", "project_id", "provider", "account_id", "role_arn",
		"external_id", "region", "capabilities", "status", "created_at",
		"last_verified_at",
	}
	for _, col := range wantCols {
		var exists bool
		err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			  WHERE table_name = 'connector_connections' AND column_name = $1)`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("check column %s: %v", col, err)
		}
		if !exists {
			t.Errorf("connector_connections missing column %s", col)
		}
	}

	// FK targets: org_id -> organizations, project_id -> projects.
	rows, err := st.Pool().Query(ctx, `
		SELECT kcu.column_name, ccu.table_name
		  FROM information_schema.table_constraints tc
		  JOIN information_schema.key_column_usage kcu
		    ON tc.constraint_name = kcu.constraint_name
		  JOIN information_schema.constraint_column_usage ccu
		    ON tc.constraint_name = ccu.constraint_name
		 WHERE tc.constraint_type = 'FOREIGN KEY'
		   AND tc.table_name = 'connector_connections'`)
	if err != nil {
		t.Fatalf("query fks: %v", err)
	}
	defer rows.Close()
	fks := map[string]string{}
	for rows.Next() {
		var col, target string
		if err := rows.Scan(&col, &target); err != nil {
			t.Fatalf("scan fk: %v", err)
		}
		fks[col] = target
	}
	if fks["org_id"] != "organizations" {
		t.Errorf("org_id FK target = %q, want organizations", fks["org_id"])
	}
	if fks["project_id"] != "projects" {
		t.Errorf("project_id FK target = %q, want projects", fks["project_id"])
	}
}

// TestConnectorSchemaHasNoSecretColumns asserts no column in any connector_*
// table matches credential name patterns — the schema stores no secrets.
func TestConnectorSchemaHasNoSecretColumns(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	rows, err := st.Pool().Query(ctx, `
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name LIKE 'connector_%'`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	banned := []string{"secret", "access_key", "accesskey", "session_token", "sessiontoken", "password", "credential"}
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		for _, b := range banned {
			if containsFold(col, b) {
				t.Errorf("connector table %s has credential-like column %q (matched %q)", tbl, col, b)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
}

// containsFold reports whether s contains substr, case-insensitively.
func containsFold(s, substr string) bool {
	s, substr = toLower(s), toLower(substr)
	return len(substr) == 0 || indexOf(s, substr) >= 0
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
