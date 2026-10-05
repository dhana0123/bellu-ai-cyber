package normalize

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

func loadJSONL(t *testing.T, path string) []rawevent.RawEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var events []rawevent.RawEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e rawevent.RawEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("decode: %v", err)
		}
		e.RunID = "run-test"
		e.Finalize()
		events = append(events, e)
	}
	return events
}

func normalizeAWS(t *testing.T) Result {
	t.Helper()
	res, err := AWS{}.Normalize(loadJSONL(t, "../../testdata/aws/inventory.jsonl"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return res
}

func TestAWSKindMapping(t *testing.T) {
	res := normalizeAWS(t)
	cases := []struct {
		key  string
		want model.Kind
	}{
		{"111122223333", model.KindCloudAccount},
		{"arn:aws:iam::111122223333:role/agent-exec", model.KindIdentity},
		{"arn:aws:s3:::bellu-training-data", model.KindDataStore},
		{"arn:aws:lambda:us-east-1:111122223333:function:agent", model.KindCompute},
		{"arn:aws:eks:us-east-1:111122223333:cluster/bellu-eks", model.KindCluster},
		{"arn:aws:ecs:us-east-1:111122223333:task-definition/agent-api:3", model.KindWorkload},
		{"arn:aws:bedrock:us-east-1:111122223333:custom-model/bellu-llm", model.KindAIService},
	}
	for _, c := range cases {
		got, ok := kindOf(res, c.key)
		if !ok {
			t.Errorf("entity %q not found", c.key)
			continue
		}
		if got != c.want {
			t.Errorf("entity %q kind = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestAWSUsesEdge(t *testing.T) {
	res := normalizeAWS(t)
	// Lambda uses role.
	if !hasEdge(res, model.RelUses,
		"arn:aws:lambda:us-east-1:111122223333:function:agent",
		"arn:aws:iam::111122223333:role/agent-exec") {
		t.Fatal("expected Lambda USES role edge")
	}
	// ECS task uses role.
	if !hasEdge(res, model.RelUses,
		"arn:aws:ecs:us-east-1:111122223333:task-definition/agent-api:3",
		"arn:aws:iam::111122223333:role/agent-exec") {
		t.Fatal("expected ECS task USES role edge")
	}
}

func TestAWSRunsImageEdge(t *testing.T) {
	res := normalizeAWS(t)
	if !hasEdge(res, model.RelRunsImage,
		"arn:aws:ecs:us-east-1:111122223333:task-definition/agent-api:3",
		"sha256:aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa7777bbbb8888") {
		t.Fatalf("expected ECS task RUNS_IMAGE digest edge; edges=%v", edgeSummaries(res))
	}
}

func TestAWSCanReadEdge(t *testing.T) {
	res := normalizeAWS(t)
	var found *model.Edge
	for i := range res.Edges {
		e := &res.Edges[i]
		if e.Relation == model.RelCanRead &&
			e.SrcKey == "arn:aws:iam::111122223333:role/agent-exec" &&
			e.DstKey == "arn:aws:s3:::bellu-training-data" {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("expected CAN_READ role -> bucket; edges=%v", edgeSummaries(res))
	}
	if found.Confidence >= 1.0 {
		t.Errorf("expected approximate confidence < 1, got %v", found.Confidence)
	}
	if found.Attributes["effective"] != "approx" {
		t.Errorf("expected effective=approx, got %v", found.Attributes["effective"])
	}
}

func TestAWSExposedToEdge(t *testing.T) {
	res := normalizeAWS(t)
	if !hasEdge(res, model.RelExposedTo, "sg-0123456789abcdef0", "0.0.0.0/0") {
		t.Fatal("expected EXPOSED_TO for SG open to world")
	}
}

func TestAWSContainsEdges(t *testing.T) {
	res := normalizeAWS(t)
	// Account CONTAINS the S3 bucket.
	if !hasEdge(res, model.RelContains, "111122223333", "arn:aws:s3:::bellu-training-data") {
		t.Fatal("expected account CONTAINS bucket")
	}
}

func edgeSummaries(res Result) []string {
	out := make([]string, 0, len(res.Edges))
	for _, e := range res.Edges {
		out = append(out, string(e.Relation)+" "+e.SrcKey+" -> "+e.DstKey)
	}
	return out
}
