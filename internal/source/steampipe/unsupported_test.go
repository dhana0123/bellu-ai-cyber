package steampipe

import (
	"errors"
	"testing"
)

func TestIsUnsupportedServiceErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"bedrock 404 unknown operation", errors.New("rpc error: code = Unknown desc = aws_conn_2: operation error Bedrock: ListCustomModels, https response error StatusCode: 404, RequestID: x, api error UnknownOperationException: Unknown Operation (SQLSTATE HV000)"), true},
		{"generic 404 operation error", errors.New("operation error Foo: Bar, https response error StatusCode: 404"), true},
		{"endpoint unreachable", errors.New("could not connect to the endpoint URL: https://bedrock.xx.amazonaws.com"), true},
		{"no such host", errors.New("dial tcp: lookup bedrock.xx: no such host"), true},
		{"real access denied is NOT swallowed", errors.New("operation error S3: ListBuckets, AccessDenied: not authorized"), false},
		{"real server error is NOT swallowed", errors.New("StatusCode: 500 internal"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isUnsupportedServiceErr(c.err); got != c.want {
				t.Errorf("isUnsupportedServiceErr(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
