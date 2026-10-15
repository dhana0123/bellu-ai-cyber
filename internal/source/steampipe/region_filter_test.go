package steampipe

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// TestWhereClauseRegionFiltering pins the per-table region restriction: a table
// with a RegionColumn and a non-empty Regions list gets a `<col> IN (...)`
// predicate, while tables without a region restriction keep an empty clause so
// they still query every region. This is the fix for the Bedrock
// 404 UnknownOperationException from regions = ["*"].
func TestWhereClauseRegionFiltering(t *testing.T) {
	cases := []struct {
		name string
		spec TableSpec
		want string
	}{
		{
			name: "no region column, no where -> empty (unrestricted)",
			spec: TableSpec{Table: "aws_iam_role"},
			want: "",
		},
		{
			name: "region column but no regions -> empty (all regions)",
			spec: TableSpec{Table: "aws_ec2_instance", RegionColumn: "region"},
			want: "",
		},
		{
			name: "regions set but no region column -> empty (cannot filter)",
			spec: TableSpec{Table: "weird", Regions: []string{"us-east-1"}},
			want: "",
		},
		{
			name: "region column + regions -> IN predicate",
			spec: TableSpec{Table: "aws_bedrock_custom_model", RegionColumn: "region", Regions: []string{"us-east-1", "us-west-2"}},
			want: "region IN ('us-east-1', 'us-west-2')",
		},
		{
			name: "explicit where only -> preserved verbatim",
			spec: TableSpec{Table: "t", Where: "state = 'running'"},
			want: "state = 'running'",
		},
		{
			name: "explicit where + region restriction -> ANDed",
			spec: TableSpec{Table: "t", Where: "state = 'running'", RegionColumn: "region", Regions: []string{"us-east-1"}},
			want: "state = 'running' AND region IN ('us-east-1')",
		},
		{
			name: "empty region entries are skipped",
			spec: TableSpec{Table: "t", RegionColumn: "region", Regions: []string{"", "us-east-1", ""}},
			want: "region IN ('us-east-1')",
		},
		{
			name: "all-empty regions -> no predicate",
			spec: TableSpec{Table: "t", RegionColumn: "region", Regions: []string{"", ""}},
			want: "",
		},
		{
			name: "single quote in region value is escaped",
			spec: TableSpec{Table: "t", RegionColumn: "region", Regions: []string{"us-'east"}},
			want: "region IN ('us-''east')",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.whereClause(); got != c.want {
				t.Errorf("whereClause() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestBedrockCatalogHasRegionRestriction asserts the shipped AWS catalog
// restricts the Bedrock table to a set of regions (so ListCustomModels is never
// called in an unsupported region) while a representative all-regions table
// such as aws_ec2_instance stays unrestricted.
func TestBedrockCatalogHasRegionRestriction(t *testing.T) {
	c, err := LoadCatalog("catalog/aws.yaml")
	if err != nil {
		t.Fatalf("load aws catalog: %v", err)
	}
	var bedrock, ec2 *TableSpec
	for i := range c.Tables {
		switch c.Tables[i].Table {
		case "aws_bedrock_custom_model":
			bedrock = &c.Tables[i]
		case "aws_ec2_instance":
			ec2 = &c.Tables[i]
		}
	}
	if bedrock == nil {
		t.Fatal("aws_bedrock_custom_model not found in catalog")
	}
	if len(bedrock.Regions) == 0 {
		t.Error("aws_bedrock_custom_model has no region restriction; expected a fixed region list")
	}
	if bedrock.RegionColumn == "" {
		t.Error("aws_bedrock_custom_model missing region_column; region restriction cannot apply")
	}
	// us-east-1 must be present (Bedrock's primary region).
	found := false
	for _, r := range bedrock.Regions {
		if r == "us-east-1" {
			found = true
		}
	}
	if !found {
		t.Error("expected us-east-1 in Bedrock region restriction")
	}
	// The Bedrock table must produce an IN predicate.
	if w := bedrock.whereClause(); !strings.Contains(w, bedrock.RegionColumn+" IN (") {
		t.Errorf("bedrock whereClause() = %q, want a region IN (...) predicate", w)
	}
	// A normal regional table must NOT be region-restricted (preserve behavior).
	if ec2 == nil {
		t.Fatal("aws_ec2_instance not found in catalog")
	}
	if len(ec2.Regions) != 0 {
		t.Errorf("aws_ec2_instance should not be region-restricted, got regions = %v", ec2.Regions)
	}
	if ec2.whereClause() != "" {
		t.Errorf("aws_ec2_instance whereClause() = %q, want empty (all regions)", ec2.whereClause())
	}
}

// sqlCapturingQuerier records the full SQL of every query the collector issues,
// keyed by table name, and returns empty rows. It lets a test assert the exact
// WHERE clause emitted per table without a live Steampipe/DB.
type sqlCapturingQuerier struct {
	mu  sync.Mutex
	sql map[string]string
}

func (q *sqlCapturingQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	q.mu.Lock()
	if q.sql == nil {
		q.sql = map[string]string{}
	}
	q.sql[tableFromSQL(sql)] = sql
	q.mu.Unlock()
	return emptyRows{}, nil
}

// TestCollectEmitsRegionPredicateForBedrockOnly runs the real collector over the
// shipped AWS catalog with a SQL-capturing connection and asserts the Bedrock
// query carries a region IN (...) predicate while a normal regional table
// (aws_ec2_instance) is queried with no WHERE clause (all regions preserved).
func TestCollectEmitsRegionPredicateForBedrockOnly(t *testing.T) {
	cat, err := LoadCatalog("catalog/aws.yaml")
	if err != nil {
		t.Fatalf("load aws catalog: %v", err)
	}
	cap := &sqlCapturingQuerier{}
	s := New("postgres://unused", cat)
	s.connect = func(context.Context) (querier, func(), error) { return cap, func() {}, nil }

	evc, errc := s.Collect(context.Background(), source.Run{RunID: "run-region"})
	for evc != nil || errc != nil {
		select {
		case _, ok := <-evc:
			if !ok {
				evc = nil
			}
		case _, ok := <-errc:
			if !ok {
				errc = nil
			}
		}
	}

	bedrockSQL, ok := cap.sql["aws_bedrock_custom_model"]
	if !ok {
		t.Fatal("collector never queried aws_bedrock_custom_model")
	}
	if !strings.Contains(bedrockSQL, "WHERE region IN (") {
		t.Errorf("bedrock query missing region predicate: %q", bedrockSQL)
	}
	if !strings.Contains(bedrockSQL, "'us-east-1'") {
		t.Errorf("bedrock query missing us-east-1: %q", bedrockSQL)
	}

	ec2SQL, ok := cap.sql["aws_ec2_instance"]
	if !ok {
		t.Fatal("collector never queried aws_ec2_instance")
	}
	if strings.Contains(ec2SQL, "WHERE") {
		t.Errorf("aws_ec2_instance should have no WHERE clause (all regions), got: %q", ec2SQL)
	}
}
