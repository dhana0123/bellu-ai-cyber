package steampipe

import "testing"

// TestQualifiedTable pins the per-connection schema scoping: when a connection
// schema is set, table names are qualified so a query targets exactly that
// connection; when empty, the bare table name is used.
func TestQualifiedTable(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		table  string
		want   string
	}{
		{"no schema -> bare table", "", "aws_account", "aws_account"},
		{"schema -> qualified", "aws_conn_42", "aws_account", "aws_conn_42.aws_account"},
		{"different connection isolated", "aws_conn_7", "aws_s3_bucket", "aws_conn_7.aws_s3_bucket"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Source{schema: c.schema}
			if got := s.qualifiedTable(c.table); got != c.want {
				t.Errorf("qualifiedTable(%q) with schema %q = %q, want %q", c.table, c.schema, got, c.want)
			}
		})
	}
}

// TestFactorySetsConnectionSchema asserts Factory wires connection_schema onto
// the source so queries scope to that connection.
func TestFactorySetsConnectionSchema(t *testing.T) {
	src, err := Factory(map[string]any{
		"connection":        "aws",
		"catalog_dir":       "catalog",
		"connection_schema": "aws_conn_99",
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	sp, ok := src.(*Source)
	if !ok {
		t.Fatalf("factory returned %T, want *Source", src)
	}
	if sp.schema != "aws_conn_99" {
		t.Errorf("schema = %q, want aws_conn_99", sp.schema)
	}
	if got := sp.qualifiedTable("aws_account"); got != "aws_conn_99.aws_account" {
		t.Errorf("qualifiedTable = %q, want aws_conn_99.aws_account", got)
	}
}
