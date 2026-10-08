package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// ctxWithQuery builds a throwaway *gin.Context whose request carries the given
// raw query string, so the pure query-param helpers can be exercised without a
// router, a store, or a live Postgres.
func ctxWithQuery(rawQuery string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?"+rawQuery, nil)
	return c
}

// TestQueryBool pins the parsing contract the intel handler relies on when it
// applies the optional kev filter (Requirement 13.3): a present, well-formed
// boolean reports (val, true); an absent or unparseable value reports
// (false, false) so the filter is left unapplied.
func TestQueryBool(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		wantVal  bool
		wantOK   bool
	}{
		{"absent", "", false, false},
		{"empty value", "kev=", false, false},
		{"true", "kev=true", true, true},
		{"false", "kev=false", false, true},
		{"one", "kev=1", true, true},
		{"zero", "kev=0", false, true},
		{"True mixed case", "kev=True", true, true},
		{"garbage", "kev=maybe", false, false},
		{"numeric garbage", "kev=2", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctxWithQuery(tc.query)
			val, ok := queryBool(c, "kev")
			if val != tc.wantVal || ok != tc.wantOK {
				t.Fatalf("queryBool(%q) = (%v, %v), want (%v, %v)",
					tc.query, val, ok, tc.wantVal, tc.wantOK)
			}
		})
	}
}

// TestQueryInt pins the limit-parsing contract shared by the findings handler
// (Requirement 13.1): a well-formed positive integer is used as-is, while an
// absent, non-numeric, zero, or negative value falls back to the default so a
// bad client input never produces an unbounded or nonsensical limit.
func TestQueryInt(t *testing.T) {
	const def = 200
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"absent uses default", "", def},
		{"empty uses default", "limit=", def},
		{"positive used as-is", "limit=25", 25},
		{"non-numeric uses default", "limit=abc", def},
		{"zero uses default", "limit=0", def},
		{"negative uses default", "limit=-5", def},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctxWithQuery(tc.query)
			if got := queryInt(c, "limit", def); got != tc.want {
				t.Fatalf("queryInt(%q, %d) = %d, want %d", tc.query, def, got, tc.want)
			}
		})
	}
}
