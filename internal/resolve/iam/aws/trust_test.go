package aws

import (
	"encoding/json"
	"testing"
)

func mustDoc(t *testing.T, s string) *PolicyDocument {
	t.Helper()
	doc, err := ParsePolicy(json.RawMessage(s))
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	return doc
}

// Feature: cloud-attack-path-graph, Property 10/11/12/13: CAN_ASSUME grant
// generation per principal type (R4.1-R4.4).
func TestAnalyzeTrustPolicy_PrincipalCases(t *testing.T) {
	t.Run("named role principal -> confidence 1.0", func(t *testing.T) {
		doc := mustDoc(t, `{"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole","Principal":{"AWS":"arn:aws:iam::111122223333:role/Caller"}}]}`)
		g := AnalyzeTrustPolicy("", doc)
		if len(g) != 1 || g[0].PrincipalKey != "arn:aws:iam::111122223333:role/Caller" || g[0].Confidence != 1.0 {
			t.Fatalf("unexpected grants: %+v", g)
		}
	})
	t.Run("account root -> account key, 0.7, account_grant", func(t *testing.T) {
		doc := mustDoc(t, `{"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole","Principal":{"AWS":"arn:aws:iam::111122223333:root"}}]}`)
		g := AnalyzeTrustPolicy("", doc)
		if len(g) != 1 || g[0].PrincipalKey != "aws-account:111122223333" || g[0].Confidence != 0.7 {
			t.Fatalf("unexpected grants: %+v", g)
		}
		if g[0].Attributes["account_grant"] != true {
			t.Fatalf("expected account_grant attribute: %+v", g[0].Attributes)
		}
	})
	t.Run("bare account id -> account key", func(t *testing.T) {
		doc := mustDoc(t, `{"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole","Principal":{"AWS":"111122223333"}}]}`)
		g := AnalyzeTrustPolicy("", doc)
		if len(g) != 1 || g[0].PrincipalKey != "aws-account:111122223333" {
			t.Fatalf("unexpected grants: %+v", g)
		}
	})
	t.Run("service principal -> excluded", func(t *testing.T) {
		doc := mustDoc(t, `{"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole","Principal":{"Service":"ec2.amazonaws.com"}}]}`)
		if g := AnalyzeTrustPolicy("", doc); len(g) != 0 {
			t.Fatalf("expected no grants for service principal, got %+v", g)
		}
	})
	t.Run("wildcard -> any-identity, unscoped", func(t *testing.T) {
		doc := mustDoc(t, `{"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole","Principal":"*"}]}`)
		g := AnalyzeTrustPolicy("", doc)
		if len(g) != 1 || g[0].PrincipalKey != AnyIdentityKey || g[0].Attributes["unscoped"] != true {
			t.Fatalf("unexpected grants: %+v", g)
		}
	})
}

// Feature: cloud-attack-path-graph, Property 14: explicit trust Deny overrides
// Allow for the same principal (R4.5).
func TestAnalyzeTrustPolicy_DenyOverridesAllow(t *testing.T) {
	doc := mustDoc(t, `{"Statement":[
		{"Effect":"Allow","Action":"sts:AssumeRole","Principal":{"AWS":"arn:aws:iam::111122223333:role/Caller"}},
		{"Effect":"Deny","Action":"sts:AssumeRole","Principal":{"AWS":"arn:aws:iam::111122223333:role/Caller"}}
	]}`)
	if g := AnalyzeTrustPolicy("", doc); len(g) != 0 {
		t.Fatalf("expected deny to override allow, got %+v", g)
	}
}

// A non-assume action must not produce a grant.
func TestAnalyzeTrustPolicy_IgnoresNonAssume(t *testing.T) {
	doc := mustDoc(t, `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Principal":{"AWS":"arn:aws:iam::111122223333:role/Caller"}}]}`)
	if g := AnalyzeTrustPolicy("", doc); len(g) != 0 {
		t.Fatalf("expected no grants for non-assume action, got %+v", g)
	}
}
