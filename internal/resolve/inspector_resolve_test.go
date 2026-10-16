package resolve

import (
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

func inspEvent(rt, rid, payload string) rawevent.RawEvent {
	e := rawevent.RawEvent{
		Source: "steampipe", Collector: "steampipe:aws", Provider: "aws",
		ResourceType: rt, ResourceID: rid,
		ObservedAt: time.Unix(1700000000, 0).UTC(), Payload: []byte(payload), RunID: "run-test",
	}
	e.Finalize()
	return e
}

// End to end through the real normalizer and resolver: Inspector findings on a
// discovered EC2 instance and ECR image become resolved HAS_VULN edges to a
// single Vulnerability node, with nothing recorded as unresolved.
func TestInspectorFindingsResolveToConnectedGraph(t *testing.T) {
	const (
		instARN = "arn:aws:ec2:us-east-1:111122223333:instance/i-0abc"
		digest  = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	events := []rawevent.RawEvent{
		inspEvent("aws_ec2_instance", instARN, `{"instance_id":"i-0abc","account_id":"111122223333"}`),
		inspEvent("aws_ecr_image", digest, `{"image_digest":"`+digest+`","repository_name":"app","account_id":"111122223333"}`),
		inspEvent("aws_inspector2_finding", "arn:aws:inspector2:us-east-1:111122223333:finding/f1", `{
			"type":"PACKAGE_VULNERABILITY","status":"ACTIVE","vulnerability_id":"CVE-2024-0002","severity":"CRITICAL",
			"resources":[
				{"Type":"AWS_EC2_INSTANCE","Id":"i-0abc"},
				{"Type":"AWS_ECR_CONTAINER_IMAGE","Id":"arn:aws:ecr:us-east-1:111122223333:repository/app/`+digest+`"}
			]}`),
	}

	nres, err := normalize.AWS{}.Normalize(events)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDeterministic(nil)
	out, err := d.Resolve(nres)
	if err != nil {
		t.Fatal(err)
	}

	for _, u := range d.Unresolved {
		if u.Relation == string(model.RelHasVuln) {
			t.Fatalf("HAS_VULN edge was unresolved (dropped): %+v", u)
		}
	}

	want := map[string]bool{
		"compute:aws:" + instARN: false,
		"image:" + digest:        false,
	}
	for _, e := range out.Edges {
		if e.Relation != model.RelHasVuln {
			continue
		}
		if e.DstCanonicalID != "vuln:CVE-2024-0002" {
			t.Errorf("HAS_VULN dst = %q, want vuln:CVE-2024-0002", e.DstCanonicalID)
		}
		if _, ok := want[e.SrcCanonicalID]; ok {
			want[e.SrcCanonicalID] = true
		}
	}
	for src, found := range want {
		if !found {
			t.Errorf("no resolved HAS_VULN edge from %q", src)
		}
	}
}
