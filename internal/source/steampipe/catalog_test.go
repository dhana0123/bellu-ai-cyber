package steampipe

import "testing"

func TestAWSCatalogLoads(t *testing.T) {
	c, err := LoadCatalog("catalog/aws.yaml")
	if err != nil {
		t.Fatalf("load aws catalog: %v", err)
	}
	if c.Provider != "aws" {
		t.Fatalf("provider = %q, want aws", c.Provider)
	}
	// Spot-check a few expected tables.
	want := map[string]bool{
		"aws_iam_role": false, "aws_s3_bucket": false,
		"aws_lambda_function": false, "aws_ecr_image": false,
	}
	for _, tbl := range c.Tables {
		if _, ok := want[tbl.Table]; ok {
			want[tbl.Table] = true
		}
	}
	for tbl, found := range want {
		if !found {
			t.Errorf("expected table %q in aws catalog", tbl)
		}
	}
}

func TestCatalogValidation(t *testing.T) {
	cases := []struct {
		name    string
		cat     Catalog
		wantErr bool
	}{
		{"ok", Catalog{Provider: "aws", Tables: []TableSpec{{Table: "t", IDColumn: "arn"}}}, false},
		{"no provider", Catalog{Tables: []TableSpec{{Table: "t", IDColumn: "arn"}}}, true},
		{"no tables", Catalog{Provider: "aws"}, true},
		{"table missing name", Catalog{Provider: "aws", Tables: []TableSpec{{IDColumn: "arn"}}}, true},
		{"table missing id", Catalog{Provider: "aws", Tables: []TableSpec{{Table: "t"}}}, true},
		{"duplicate table", Catalog{Provider: "aws", Tables: []TableSpec{{Table: "t", IDColumn: "a"}, {Table: "t", IDColumn: "b"}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cat.Validate()
			if c.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResourceTypeDefault(t *testing.T) {
	if got := (TableSpec{Table: "aws_iam_role"}).resourceType(); got != "aws_iam_role" {
		t.Fatalf("default resource type = %q", got)
	}
	if got := (TableSpec{Table: "aws_iam_role", ResourceType: "iam.role"}).resourceType(); got != "iam.role" {
		t.Fatalf("explicit resource type = %q", got)
	}
}
