package cloudquery

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// loadCatalogYAML parses a mapping YAML body into a Catalog without touching
// disk, so the parse/default/validate behavior can be exercised directly.
func loadCatalogYAML(t *testing.T, body string) (*Catalog, error) {
	t.Helper()
	var c Catalog
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("unmarshal mapping: %v", err)
	}
	return &c, c.Validate()
}

// TestTableSpecCQTable covers cq_table defaulting to the Bellu table name when
// empty and overriding it when set (Requirement 2.4).
func TestTableSpecCQTable(t *testing.T) {
	cases := []struct {
		name string
		spec TableSpec
		want string
	}{
		{
			name: "defaults to table name when cq_table empty",
			spec: TableSpec{Table: "aws_iam_role"},
			want: "aws_iam_role",
		},
		{
			name: "overrides with cq_table when set",
			spec: TableSpec{Table: "aws_iam_role", CQTable: "aws_iam_roles"},
			want: "aws_iam_roles",
		},
		{
			name: "override differs from table (pluralized staging name)",
			spec: TableSpec{Table: "aws_s3_bucket", CQTable: "aws_s3_buckets"},
			want: "aws_s3_buckets",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.cqTable(); got != tc.want {
				t.Fatalf("cqTable() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTableSpecResourceType covers resource_type defaulting to the table name
// when empty and overriding it when set.
func TestTableSpecResourceType(t *testing.T) {
	cases := []struct {
		name string
		spec TableSpec
		want string
	}{
		{
			name: "defaults to table name when resource_type empty",
			spec: TableSpec{Table: "aws_iam_role"},
			want: "aws_iam_role",
		},
		{
			name: "overrides with resource_type when set",
			spec: TableSpec{Table: "aws_iam_role", ResourceType: "iam_role"},
			want: "iam_role",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.resourceType(); got != tc.want {
				t.Fatalf("resourceType() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCatalogCapabilityParsing asserts the capability field parses from YAML,
// including the ungated (empty) case which Validate() must accept.
func TestCatalogCapabilityParsing(t *testing.T) {
	body := `
provider: aws
tables:
  - table: aws_iam_role
    id_column: arn
    capability: core
  - table: aws_eks_cluster
    id_column: arn
    capability: eks
  - table: aws_s3_bucket
    id_column: arn
`
	c, err := loadCatalogYAML(t, body)
	if err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	want := map[string]string{
		"aws_iam_role":    "core",
		"aws_eks_cluster": "eks",
		"aws_s3_bucket":   "", // ungated: empty capability is valid
	}
	for _, spec := range c.Tables {
		if got := spec.Capability; got != want[spec.Table] {
			t.Errorf("table %q capability = %q, want %q", spec.Table, got, want[spec.Table])
		}
	}
}

// TestCatalogRegionParsing asserts the regions restriction parses from YAML
// into the TableSpec.
func TestCatalogRegionParsing(t *testing.T) {
	body := `
provider: aws
tables:
  - table: aws_region_scoped
    id_column: arn
    region_column: region
    regions:
      - us-east-1
      - eu-west-1
  - table: aws_global
    id_column: arn
`
	c, err := loadCatalogYAML(t, body)
	if err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	byName := map[string]TableSpec{}
	for _, s := range c.Tables {
		byName[s.Table] = s
	}
	scoped := byName["aws_region_scoped"]
	if got, want := scoped.RegionColumn, "region"; got != want {
		t.Errorf("region_column = %q, want %q", got, want)
	}
	if got := scoped.Regions; len(got) != 2 || got[0] != "us-east-1" || got[1] != "eu-west-1" {
		t.Errorf("regions = %v, want [us-east-1 eu-west-1]", got)
	}
	if got := byName["aws_global"].Regions; len(got) != 0 {
		t.Errorf("aws_global regions = %v, want empty (all-regions default)", got)
	}
}

// TestWhereClauseRegionPushdown covers whereClause() combining an explicit
// predicate with the region IN(...) push-down and the all-regions default.
func TestWhereClauseRegionPushdown(t *testing.T) {
	cases := []struct {
		name string
		spec TableSpec
		want string
	}{
		{
			name: "no predicate and no regions yields empty clause",
			spec: TableSpec{Table: "t", RegionColumn: "region"},
			want: "",
		},
		{
			name: "regions without a region column stay all-regions",
			spec: TableSpec{Table: "t", Regions: []string{"us-east-1"}},
			want: "",
		},
		{
			name: "region column with regions pushes down IN list",
			spec: TableSpec{Table: "t", RegionColumn: "region", Regions: []string{"us-east-1", "eu-west-1"}},
			want: "region IN ('us-east-1', 'eu-west-1')",
		},
		{
			name: "explicit where only",
			spec: TableSpec{Table: "t", Where: "active = true"},
			want: "active = true",
		},
		{
			name: "explicit where combined with region push-down",
			spec: TableSpec{Table: "t", Where: "active = true", RegionColumn: "region", Regions: []string{"us-east-1"}},
			want: "active = true AND region IN ('us-east-1')",
		},
		{
			name: "empty region values are dropped",
			spec: TableSpec{Table: "t", RegionColumn: "region", Regions: []string{"", "us-east-1", ""}},
			want: "region IN ('us-east-1')",
		},
		{
			name: "all-empty region values yield no region predicate",
			spec: TableSpec{Table: "t", RegionColumn: "region", Regions: []string{"", ""}},
			want: "",
		},
		{
			name: "region literal with a quote is escaped",
			spec: TableSpec{Table: "t", RegionColumn: "region", Regions: []string{"us-'1"}},
			want: "region IN ('us-''1')",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.whereClause(); got != tc.want {
				t.Fatalf("whereClause() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCatalogValidate asserts Validate() accepts a well-formed mapping and
// rejects a missing provider, no tables, duplicate tables, and a missing
// id_column.
func TestCatalogValidate(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string // substring expected in the error; empty means no error
	}{
		{
			name: "valid mapping passes",
			body: `
provider: aws
tables:
  - table: aws_iam_role
    id_column: arn
  - table: aws_s3_bucket
    id_column: arn
`,
			wantErr: "",
		},
		{
			name: "missing provider rejected",
			body: `
tables:
  - table: aws_iam_role
    id_column: arn
`,
			wantErr: "missing provider",
		},
		{
			name:    "empty tables rejected",
			body:    `provider: aws`,
			wantErr: "no tables",
		},
		{
			name: "duplicate table rejected",
			body: `
provider: aws
tables:
  - table: aws_iam_role
    id_column: arn
  - table: aws_iam_role
    id_column: id
`,
			wantErr: `duplicate table "aws_iam_role"`,
		},
		{
			name: "missing id_column rejected",
			body: `
provider: aws
tables:
  - table: aws_iam_role
`,
			wantErr: "missing id_column",
		},
		{
			name: "missing table name rejected",
			body: `
provider: aws
tables:
  - id_column: arn
`,
			wantErr: "missing table name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadCatalogYAML(t, tc.body)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
