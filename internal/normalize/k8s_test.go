package normalize

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

func normalizeK8s(t *testing.T) Result {
	t.Helper()
	res, err := K8s{}.Normalize(loadJSONL(t, "../../testdata/k8s/eks.jsonl"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return res
}

func TestK8sWorkloadAndIdentity(t *testing.T) {
	res := normalizeK8s(t)
	if k, ok := kindOf(res, "k8s:pod-uid-1"); !ok || k != model.KindWorkload {
		t.Fatalf("pod not Workload (got %q ok=%v)", k, ok)
	}
	if k, ok := kindOf(res, "k8s:sa:sa-uid-1"); !ok || k != model.KindIdentity {
		t.Fatalf("service account not Identity (got %q ok=%v)", k, ok)
	}
}

func TestK8sRunsImageByDigest(t *testing.T) {
	res := normalizeK8s(t)
	if !hasEdge(res, model.RelRunsImage, "k8s:pod-uid-1",
		"sha256:aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa7777bbbb8888") {
		t.Fatalf("expected pod RUNS_IMAGE digest; edges=%v", edgeSummaries(res))
	}
}

func TestK8sRunsInCluster(t *testing.T) {
	res := normalizeK8s(t)
	if !hasEdge(res, model.RelRunsIn, "k8s:pod-uid-1",
		"arn:aws:eks:us-east-1:111122223333:cluster/bellu-eks") {
		t.Fatalf("expected pod RUNS_IN cluster; edges=%v", edgeSummaries(res))
	}
}

func TestK8sPodUsesServiceAccount(t *testing.T) {
	res := normalizeK8s(t)
	if !hasEdge(res, model.RelUses, "k8s:pod-uid-1", "k8s:sa:sa-uid-1") {
		t.Fatalf("expected pod USES service account; edges=%v", edgeSummaries(res))
	}
}

func TestK8sAuthenticatesAs(t *testing.T) {
	res := normalizeK8s(t)
	cases := []struct {
		saKey string
		cloud string
	}{
		{"k8s:sa:sa-uid-1", "arn:aws:iam::111122223333:role/agent-exec"},    // IRSA
		{"k8s:sa:sa-uid-2", "agent@bellu-gcp.iam.gserviceaccount.com"},       // GKE WI
		{"k8s:sa:sa-uid-3", "principal-aaaa"},                                 // Azure WI
	}
	for _, c := range cases {
		if !hasEdge(res, model.RelAuthenticatesAs, c.saKey, c.cloud) {
			t.Errorf("expected %s AUTHENTICATES_AS %s; edges=%v", c.saKey, c.cloud, edgeSummaries(res))
		}
	}
}
