package normalize

import (
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	zeeksrc "github.com/bellu/bellu-ai-cyber/internal/source/zeek"
)

func mustTime() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }

func zeekEvent(rt, rid string, payload string) rawevent.RawEvent {
	e := rawevent.RawEvent{
		Source: "zeek", Collector: "zeek:sensor", Provider: "network",
		ResourceType: rt, ResourceID: rt + ":" + rid,
		ObservedAt: mustTime(), Payload: []byte(payload), RunID: "run-test",
	}
	e.Finalize()
	return e
}

func normalizeZeekFixtures(t *testing.T) Result {
	t.Helper()
	events := []rawevent.RawEvent{
		zeekEvent(zeeksrc.RTDNS, "d1", `{"query":"api.openai.com","answers":["104.18.6.192"]}`),
		zeekEvent(zeeksrc.RTConn, "c1", `{"id.orig_h":"10.0.1.5","id.resp_h":"104.18.6.192","orig_bytes":820,"resp_bytes":15300}`),
		zeekEvent(zeeksrc.RTConn, "c2", `{"id.orig_h":"10.0.1.5","id.resp_h":"104.18.6.192","orig_bytes":640,"resp_bytes":9200}`),
		zeekEvent(zeeksrc.RTSSL, "s1", `{"id.resp_h":"104.18.6.192","server_name":"api.openai.com"}`),
	}
	res, err := Network{}.Normalize(events)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestNetworkDNSResolvesTo(t *testing.T) {
	res := normalizeZeekFixtures(t)
	if !hasEdge(res, model.RelResolvesTo, "api.openai.com", "104.18.6.192") {
		t.Fatalf("expected RESOLVES_TO domain -> ip; edges=%v", edgeSummaries(res))
	}
}

func TestNetworkAIServiceTag(t *testing.T) {
	res := normalizeZeekFixtures(t)
	var svc *model.Entity
	for i := range res.Entities {
		if res.Entities[i].Key == "api.openai.com" {
			svc = &res.Entities[i]
		}
	}
	if svc == nil {
		t.Fatal("expected ExternalService for api.openai.com")
	}
	if svc.Attributes["is_ai_api"] != true {
		t.Fatalf("expected api.openai.com tagged as AI API, attrs=%v", svc.Attributes)
	}
	if svc.Attributes["ai_service"] != "OpenAI" {
		t.Fatalf("expected ai_service OpenAI, got %v", svc.Attributes["ai_service"])
	}
}

func TestNetworkFlowAggregation(t *testing.T) {
	res := normalizeZeekFixtures(t)
	// Two conn records 10.0.1.5 -> 104.18.6.192 aggregate into one CONNECTS_TO.
	var found *model.Edge
	for i := range res.Edges {
		e := &res.Edges[i]
		if e.Relation == model.RelConnectsTo && e.SrcKey == "10.0.1.5" && e.DstKey == "104.18.6.192" {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("expected aggregated CONNECTS_TO; edges=%v", edgeSummaries(res))
	}
	if found.Attributes["count"] != 2 {
		t.Fatalf("expected count 2, got %v", found.Attributes["count"])
	}
	// bytes = (820+15300)+(640+9200) = 25960
	if b, _ := found.Attributes["bytes"].(float64); b != 25960 {
		t.Fatalf("expected bytes 25960, got %v", found.Attributes["bytes"])
	}
}

func TestNetworkConnectsToServiceByDNS(t *testing.T) {
	res := normalizeZeekFixtures(t)
	// Because 104.18.6.192 resolved to api.openai.com, a CONNECTS_TO to the
	// service name should also exist.
	if !hasEdge(res, model.RelConnectsTo, "10.0.1.5", "api.openai.com") {
		t.Fatalf("expected CONNECTS_TO src -> service name; edges=%v", edgeSummaries(res))
	}
}
