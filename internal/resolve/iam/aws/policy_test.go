package aws

import (
	"encoding/json"
	"testing"
)

const bucket = "arn:aws:s3:::bellu-training-data"

func eval(t *testing.T, doc string) (Access, []string) {
	t.Helper()
	p, err := ParsePolicy(json.RawMessage(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return EvaluateS3(p, bucket)
}

func TestAllowReadOnly(t *testing.T) {
	acc, actions := eval(t, `{"Version":"2012-10-17","Statement":[
		{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":["arn:aws:s3:::bellu-training-data","arn:aws:s3:::bellu-training-data/*"]}
	]}`)
	if acc != AccessRead {
		t.Fatalf("expected read, got %q (actions=%v)", acc, actions)
	}
}

func TestAllowWrite(t *testing.T) {
	acc, _ := eval(t, `{"Statement":[
		{"Effect":"Allow","Action":"s3:PutObject","Resource":"arn:aws:s3:::bellu-training-data/*"}
	]}`)
	if acc != AccessWrite {
		t.Fatalf("expected write, got %q", acc)
	}
}

func TestAdminWildcard(t *testing.T) {
	acc, _ := eval(t, `{"Statement":[
		{"Effect":"Allow","Action":"s3:*","Resource":"*"}
	]}`)
	if acc != AccessAdmin {
		t.Fatalf("expected admin, got %q", acc)
	}
}

func TestDenyOverridesAllow(t *testing.T) {
	acc, _ := eval(t, `{"Statement":[
		{"Effect":"Allow","Action":"s3:*","Resource":"*"},
		{"Effect":"Deny","Action":"s3:*","Resource":["arn:aws:s3:::bellu-training-data","arn:aws:s3:::bellu-training-data/*"]}
	]}`)
	if acc != AccessNone {
		t.Fatalf("expected deny to remove all access, got %q", acc)
	}
}

func TestResourceMismatch(t *testing.T) {
	acc, _ := eval(t, `{"Statement":[
		{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::some-other-bucket/*"}
	]}`)
	if acc != AccessNone {
		t.Fatalf("expected no access for different bucket, got %q", acc)
	}
}

func TestPrefixWildcardAction(t *testing.T) {
	acc, actions := eval(t, `{"Statement":[
		{"Effect":"Allow","Action":"s3:Get*","Resource":"arn:aws:s3:::bellu-training-data/*"}
	]}`)
	if acc != AccessRead {
		t.Fatalf("expected read for s3:Get*, got %q (actions=%v)", acc, actions)
	}
}

func TestNotActionTreatedConservatively(t *testing.T) {
	// A NotAction allow should NOT grant access in V1 (conservative).
	acc, _ := eval(t, `{"Statement":[
		{"Effect":"Allow","NotAction":"s3:DeleteBucket","Resource":"*"}
	]}`)
	if acc != AccessNone {
		t.Fatalf("expected NotAction to grant nothing, got %q", acc)
	}
}

func TestResourceWildcardMatch(t *testing.T) {
	acc, _ := eval(t, `{"Statement":[
		{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::bellu-*/*"}
	]}`)
	if acc != AccessRead {
		t.Fatalf("expected read via wildcard resource, got %q", acc)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "anything", true},
		{"arn:aws:s3:::bellu-*", "arn:aws:s3:::bellu-training-data", true},
		{"arn:aws:s3:::bellu-*/*", "arn:aws:s3:::bellu-training-data/key", true},
		{"arn:aws:s3:::other", "arn:aws:s3:::bellu", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q)=%v want %v", c.pattern, c.s, got, c.want)
		}
	}
}
