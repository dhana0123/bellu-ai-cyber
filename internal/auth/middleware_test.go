package auth

import (
	"net/http/httptest"
	"testing"
)

// TestRequestToken covers token extraction: the Authorization: Bearer header is
// preferred, the access_token query param is the fallback (for EventSource,
// which cannot set headers), and absence yields empty.
func TestRequestToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
		query  string
		want   string
	}{
		{"bearer header", "Bearer abc.def", "", "abc.def"},
		{"bearer case-insensitive", "bearer abc.def", "", "abc.def"},
		{"query param fallback", "", "tok123", "tok123"},
		{"header wins over query", "Bearer hdr", "qry", "hdr"},
		{"neither", "", "", ""},
		{"non-bearer header ignored, falls back to query", "Basic xyz", "tok", "tok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := "/api/connectors/1/discover/stream"
			if c.query != "" {
				target += "?access_token=" + c.query
			}
			r := httptest.NewRequest("GET", target, nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			if got := requestToken(r); got != c.want {
				t.Errorf("requestToken() = %q, want %q", got, c.want)
			}
		})
	}
}
