package k8swi

import "testing"

func TestParseIRSA(t *testing.T) {
	b := Parse(map[string]string{AnnIRSA: "arn:aws:iam::111122223333:role/agent-exec"})
	if len(b) != 1 || b[0].Provider != ProviderAWS {
		t.Fatalf("expected one AWS binding, got %+v", b)
	}
	if b[0].CloudIdentityKey != "arn:aws:iam::111122223333:role/agent-exec" {
		t.Fatalf("wrong identity key: %q", b[0].CloudIdentityKey)
	}
}

func TestParseGKEWI(t *testing.T) {
	b := Parse(map[string]string{AnnGKEWI: "agent@bellu-gcp.iam.gserviceaccount.com"})
	if len(b) != 1 || b[0].Provider != ProviderGCP {
		t.Fatalf("expected one GCP binding, got %+v", b)
	}
}

func TestParseAzureWI(t *testing.T) {
	b := Parse(map[string]string{AnnAzureWI: "client-guid-123"})
	if len(b) != 1 || b[0].Provider != ProviderAzure {
		t.Fatalf("expected one Azure binding, got %+v", b)
	}
}

func TestParseMultiple(t *testing.T) {
	b := Parse(map[string]string{
		AnnIRSA:  "arn:aws:iam::1:role/a",
		AnnGKEWI: "sa@p.iam.gserviceaccount.com",
	})
	if len(b) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(b))
	}
}

func TestParseNone(t *testing.T) {
	if b := Parse(map[string]string{"unrelated": "x"}); len(b) != 0 {
		t.Fatalf("expected no bindings, got %+v", b)
	}
}
