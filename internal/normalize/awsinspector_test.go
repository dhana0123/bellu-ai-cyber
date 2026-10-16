package normalize

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

func awsEvent(rt, rid, payload string) rawevent.RawEvent {
	e := rawevent.RawEvent{
		Source: "steampipe", Collector: "steampipe:aws", Provider: "aws",
		ResourceType: rt, ResourceID: rid,
		ObservedAt: mustTime(), Payload: []byte(payload), RunID: "run-test",
	}
	e.Finalize()
	return e
}

const (
	testInstanceARN = "arn:aws:ec2:us-east-1:111122223333:instance/i-0abc"
	testLambdaARN   = "arn:aws:lambda:us-east-1:111122223333:function:orders"
	testDigest      = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func findEdge(res Result, rel model.Relation, src, dst string) *model.Edge {
	for i := range res.Edges {
		e := &res.Edges[i]
		if e.Relation == rel && e.SrcKey == src && e.DstKey == dst {
			return e
		}
	}
	return nil
}

func countKind(res Result, k model.Kind, key string) int {
	n := 0
	for _, e := range res.Entities {
		if e.Kind == k && e.Key == key {
			n++
		}
	}
	return n
}

// An Inspector package-vulnerability finding on an EC2 instance, an ECR image
// and a Lambda function each yields a HAS_VULN edge from the discovered asset
// to ONE shared Vulnerability node keyed like the SBOM path ("cve:<ID>").
func TestInspectorFindingsLinkAssetsToVulnerability(t *testing.T) {
	events := []rawevent.RawEvent{
		awsEvent("aws_ec2_instance", testInstanceARN, `{"instance_id":"i-0abc","account_id":"111122223333"}`),
		awsEvent("aws_lambda_function", testLambdaARN, `{"account_id":"111122223333"}`),
		awsEvent("aws_ecr_image", testDigest, `{"image_digest":"`+testDigest+`","repository_name":"app"}`),
		awsEvent("aws_inspector2_finding", "arn:aws:inspector2:us-east-1:111122223333:finding/f1", `{
			"type":"PACKAGE_VULNERABILITY","status":"ACTIVE","vulnerability_id":"CVE-2024-0001",
			"severity":"HIGH","inspector_score":8.1,"fix_available":"YES","exploit_available":"YES",
			"title":"CVE-2024-0001 - openssl",
			"vulnerable_packages":[{"Name":"openssl","Version":"1.1.1","FixedInVersion":"1.1.2"}],
			"resources":[
				{"Type":"AWS_EC2_INSTANCE","Id":"i-0abc"},
				{"Type":"AWS_ECR_CONTAINER_IMAGE","Id":"arn:aws:ecr:us-east-1:111122223333:repository/app/`+testDigest+`"},
				{"Type":"AWS_LAMBDA_FUNCTION","Id":"`+testLambdaARN+`:$LATEST"}
			]}`),
	}
	res, err := AWS{}.Normalize(events)
	if err != nil {
		t.Fatal(err)
	}

	if n := countKind(res, model.KindVulnerability, "cve:CVE-2024-0001"); n != 1 {
		t.Fatalf("want exactly 1 Vulnerability entity, got %d", n)
	}
	for _, asset := range []string{testInstanceARN, testDigest, testLambdaARN} {
		e := findEdge(res, model.RelHasVuln, asset, "cve:CVE-2024-0001")
		if e == nil {
			t.Fatalf("missing HAS_VULN edge from %q", asset)
		}
		if e.Confidence != 1.0 {
			t.Errorf("confidence = %v, want 1.0", e.Confidence)
		}
	}
	e := findEdge(res, model.RelHasVuln, testInstanceARN, "cve:CVE-2024-0001")
	if e.Attributes["fixed_in_version"] != "1.1.2" || e.Attributes["package_name"] != "openssl" {
		t.Errorf("package attrs not carried on edge: %+v", e.Attributes)
	}
	for _, ent := range res.Entities {
		if ent.Kind == model.KindVulnerability {
			if ent.Attributes["severity"] != "high" || ent.Attributes["known_exploited"] != true {
				t.Errorf("vuln attrs wrong: %+v", ent.Attributes)
			}
		}
	}
}

// Findings that are not ACTIVE package vulnerabilities must not create nodes.
func TestInspectorSkipsNonPackageAndInactive(t *testing.T) {
	events := []rawevent.RawEvent{
		awsEvent("aws_inspector2_finding", "f-net", `{"type":"NETWORK_REACHABILITY","status":"ACTIVE","resources":[{"Type":"AWS_EC2_INSTANCE","Id":"i-1"}]}`),
		awsEvent("aws_inspector2_finding", "f-closed", `{"type":"PACKAGE_VULNERABILITY","status":"CLOSED","vulnerability_id":"CVE-1","resources":[{"Type":"AWS_EC2_INSTANCE","Id":"i-1"}]}`),
		awsEvent("aws_inspector2_finding", "f-noid", `{"type":"PACKAGE_VULNERABILITY","status":"ACTIVE","resources":[{"Type":"AWS_EC2_INSTANCE","Id":"i-1"}]}`),
	}
	res, _ := AWS{}.Normalize(events)
	for _, e := range res.Entities {
		if e.Kind == model.KindVulnerability {
			t.Fatalf("unexpected Vulnerability entity %q", e.Key)
		}
	}
	for _, e := range res.Edges {
		if e.Relation == model.RelHasVuln {
			t.Fatalf("unexpected HAS_VULN edge %+v", e)
		}
	}
}

// The same CVE on the same asset reported by several findings (one per
// package) yields a single edge; the same CVE on two assets yields one node.
func TestInspectorDedupes(t *testing.T) {
	f := func(id string) rawevent.RawEvent {
		return awsEvent("aws_inspector2_finding", id, `{"type":"PACKAGE_VULNERABILITY","status":"ACTIVE","vulnerability_id":"CVE-9",
			"resources":[{"Type":"AWS_EC2_INSTANCE","Id":"i-1"},{"Type":"AWS_EC2_INSTANCE","Id":"i-2"}]}`)
	}
	res, _ := AWS{}.Normalize([]rawevent.RawEvent{f("a"), f("b")})
	if n := countKind(res, model.KindVulnerability, "cve:CVE-9"); n != 1 {
		t.Fatalf("want 1 vuln node, got %d", n)
	}
	edges := 0
	for _, e := range res.Edges {
		if e.Relation == model.RelHasVuln {
			edges++
		}
	}
	if edges != 2 {
		t.Fatalf("want 2 HAS_VULN edges (one per instance), got %d", edges)
	}
}

// An instance Inspector reports but discovery did not collect keeps its raw id
// as the edge source, so the resolver records it as unresolved (visible)
// instead of the link silently vanishing.
func TestInspectorUnmappedInstanceKeepsRawID(t *testing.T) {
	events := []rawevent.RawEvent{
		awsEvent("aws_inspector2_finding", "f", `{"type":"PACKAGE_VULNERABILITY","status":"ACTIVE","vulnerability_id":"CVE-7",
			"resources":[{"Type":"AWS_EC2_INSTANCE","Id":"i-unknown"}]}`),
	}
	res, _ := AWS{}.Normalize(events)
	if findEdge(res, model.RelHasVuln, "i-unknown", "cve:CVE-7") == nil {
		t.Fatalf("expected edge keyed by raw instance id; edges=%+v", res.Edges)
	}
}

func TestInspectorKeyHelpers(t *testing.T) {
	if got := lambdaFunctionARN(testLambdaARN + ":$LATEST"); got != testLambdaARN {
		t.Errorf("lambdaFunctionARN = %q", got)
	}
	if got := lambdaFunctionARN(testLambdaARN + ":7"); got != testLambdaARN {
		t.Errorf("lambdaFunctionARN version = %q", got)
	}
	if got := digestFromInspectorID("arn:aws:ecr:r:1:repository/x/" + testDigest); got != testDigest {
		t.Errorf("digestFromInspectorID = %q", got)
	}
	if got := digestFromInspectorID("no-digest-here"); got != "" {
		t.Errorf("digestFromInspectorID(no digest) = %q, want empty", got)
	}
	if got := inspectorAssetKey("AWS_ECR_REPOSITORY", "arn:...", nil); got != "" {
		t.Errorf("repository should not map to an asset, got %q", got)
	}
}
