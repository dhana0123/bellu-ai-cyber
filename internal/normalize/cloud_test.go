package normalize

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

func TestAzureKindMapping(t *testing.T) {
	res, err := Azure{}.Normalize(loadJSONL(t, "../../testdata/azure/inventory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Storage account -> DataStore (key lowercased).
	if k, ok := kindOf(res, "/subscriptions/sub-1111/resourcegroups/rg/providers/microsoft.storage/storageaccounts/bellutraining"); !ok || k != model.KindDataStore {
		t.Fatalf("storage account not DataStore (got %q ok=%v)", k, ok)
	}
	// Managed identity keyed by principal id.
	if k, ok := kindOf(res, "principal-aaaa"); !ok || k != model.KindIdentity {
		t.Fatalf("managed identity not Identity by principal id (got %q ok=%v)", k, ok)
	}
	// Azure OpenAI -> AIService.
	if k, ok := kindOf(res, "/subscriptions/sub-1111/resourcegroups/rg/providers/microsoft.cognitiveservices/accounts/bellu-openai"); !ok || k != model.KindAIService {
		t.Fatalf("openai not AIService (got %q ok=%v)", k, ok)
	}
}

func TestAzureCanReadFromRoleAssignment(t *testing.T) {
	res, err := Azure{}.Normalize(loadJSONL(t, "../../testdata/azure/inventory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	store := "/subscriptions/sub-1111/resourcegroups/rg/providers/microsoft.storage/storageaccounts/bellutraining"
	if !hasEdge(res, model.RelCanRead, "principal-aaaa", store) {
		t.Fatalf("expected CAN_READ principal -> storage; edges=%v", edgeSummaries(res))
	}
}

func TestGCPKindMapping(t *testing.T) {
	res, err := GCP{}.Normalize(loadJSONL(t, "../../testdata/gcp/inventory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key  string
		want model.Kind
	}{
		{"bellu-gcp", model.KindCloudAccount},
		{"agent@bellu-gcp.iam.gserviceaccount.com", model.KindIdentity},
		{"bellu-training-data", model.KindDataStore},
		{"projects/bellu-gcp/locations/us-central1/models/bellu-llm", model.KindAIService},
	}
	for _, c := range cases {
		if k, ok := kindOf(res, c.key); !ok || k != c.want {
			t.Errorf("gcp %q kind = %q ok=%v, want %q", c.key, k, ok, c.want)
		}
	}
}

func TestGCPCanReadFromBinding(t *testing.T) {
	res, err := GCP{}.Normalize(loadJSONL(t, "../../testdata/gcp/inventory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(res, model.RelCanRead, "agent@bellu-gcp.iam.gserviceaccount.com", "bellu-training-data") {
		t.Fatalf("expected CAN_READ SA -> bucket; edges=%v", edgeSummaries(res))
	}
}

// Cross-provider contract: every cloud produces at least one Identity,
// DataStore and CAN_READ edge, proving the model is cloud-independent.
func TestCrossProviderContract(t *testing.T) {
	type nz struct {
		name string
		res  Result
	}
	awsRes, _ := AWS{}.Normalize(loadJSONL(t, "../../testdata/aws/inventory.jsonl"))
	azRes, _ := Azure{}.Normalize(loadJSONL(t, "../../testdata/azure/inventory.jsonl"))
	gcpRes, _ := GCP{}.Normalize(loadJSONL(t, "../../testdata/gcp/inventory.jsonl"))

	for _, c := range []nz{{"aws", awsRes}, {"azure", azRes}, {"gcp", gcpRes}} {
		if !hasKind(c.res, model.KindIdentity) {
			t.Errorf("%s: no Identity entity", c.name)
		}
		if !hasKind(c.res, model.KindDataStore) {
			t.Errorf("%s: no DataStore entity", c.name)
		}
		if !hasRelation(c.res, model.RelCanRead) {
			t.Errorf("%s: no CAN_READ edge", c.name)
		}
	}
}

func hasKind(res Result, k model.Kind) bool {
	for _, e := range res.Entities {
		if e.Kind == k {
			return true
		}
	}
	return false
}

func hasRelation(res Result, r model.Relation) bool {
	for _, e := range res.Edges {
		if e.Relation == r {
			return true
		}
	}
	return false
}
