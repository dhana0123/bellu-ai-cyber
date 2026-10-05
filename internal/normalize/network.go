package normalize

import (
	"fmt"
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize/aiapi"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	zeeksrc "github.com/bellu/bellu-ai-cyber/internal/source/zeek"
)

// Network maps Zeek RawEvents to network entities and edges.
//
//	dns.log   -> ExternalService(domain) -[:RESOLVES_TO]-> NetworkEndpoint(ip)
//	ssl.log   -> SNI names the destination endpoint/service
//	http.log  -> Host header names the destination
//	conn.log  -> aggregated CONNECTS_TO (src endpoint -> dst endpoint) with
//	             count and total bytes
//
// Destinations matching a known AI API domain are tagged as AIService.
// Source IP -> Workload/Compute resolution is done by the resolver via
// time-bounded IP aliases; here we key endpoints by IP.
type Network struct{}

// Sources implements Normalizer.
func (Network) Sources() []string { return []string{"zeek"} }

// Normalize implements Normalizer.
func (Network) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result

	// Resolve IP -> domain names observed via DNS/SSL/HTTP so endpoints get
	// human-readable names and AIService tagging.
	ipName := map[string]string{}      // ip -> hostname
	seenEndpoint := map[string]bool{}  // ip -> emitted entity?
	seenService := map[string]bool{}   // domain -> emitted entity?

	// Flow aggregation keyed by "src->dst".
	type flow struct {
		src, dst   string
		count      int
		bytes      float64
		ev         rawevent.RawEvent
	}
	flows := map[string]*flow{}

	emitEndpoint := func(ip string, ev rawevent.RawEvent) {
		if ip == "" || seenEndpoint[ip] {
			return
		}
		seenEndpoint[ip] = true
		res.Entities = append(res.Entities, model.Entity{
			Kind: model.KindNetworkEndpoint, Key: ip, Provider: "network",
			Name: ip, Attributes: map[string]any{"ip": ip},
			Evidence: EvidenceFrom(ev),
		})
	}

	emitService := func(domain string, ev rawevent.RawEvent) {
		if domain == "" || seenService[domain] {
			return
		}
		seenService[domain] = true
		attrs := map[string]any{"domain": domain}
		kind := model.KindExternalService
		if svc, ok := aiapi.Match(domain); ok {
			attrs["ai_service"] = svc
			attrs["is_ai_api"] = true
		}
		res.Entities = append(res.Entities, model.Entity{
			Kind: kind, Key: domain, Provider: "network",
			Name: domain, Attributes: attrs, Evidence: EvidenceFrom(ev),
		})
	}

	for _, e := range events {
		if e.Provider != "network" {
			continue
		}
		rec := payloadMap(e)
		switch e.ResourceType {
		case zeeksrc.RTDNS:
			domain := stringAttr(rec, "query")
			answers := toStringSlice(rec["answers"])
			if domain != "" {
				emitService(domain, e)
			}
			for _, ans := range answers {
				if isIP(ans) {
					emitEndpoint(ans, e)
					ipName[ans] = domain
					res.Edges = append(res.Edges, model.Edge{
						Relation: model.RelResolvesTo, SrcKey: domain, DstKey: ans,
						Confidence: 1.0, Evidence: EvidenceFrom(e),
					})
				}
			}
		case zeeksrc.RTSSL:
			if sni := stringAttr(rec, "server_name"); sni != "" {
				emitService(sni, e)
				if dst := stringAttr(rec, "id.resp_h"); dst != "" {
					ipName[dst] = sni
				}
			}
		case zeeksrc.RTHTTP:
			if host := stringAttr(rec, "host"); host != "" {
				emitService(host, e)
				if dst := stringAttr(rec, "id.resp_h"); dst != "" {
					ipName[dst] = host
				}
			}
		case zeeksrc.RTConn:
			src := stringAttr(rec, "id.orig_h")
			dst := stringAttr(rec, "id.resp_h")
			if src == "" || dst == "" {
				continue
			}
			emitEndpoint(src, e)
			emitEndpoint(dst, e)
			key := src + "->" + dst
			f := flows[key]
			if f == nil {
				f = &flow{src: src, dst: dst, ev: e}
				flows[key] = f
			}
			f.count++
			f.bytes += recNum(rec, "orig_bytes") + recNum(rec, "resp_bytes")
		}
	}

	// Emit aggregated CONNECTS_TO edges. If a destination IP resolved to a
	// known hostname, also connect to the ExternalService by name.
	for _, f := range flows {
		res.Edges = append(res.Edges, model.Edge{
			Relation: model.RelConnectsTo, SrcKey: f.src, DstKey: f.dst,
			Attributes: map[string]any{"count": f.count, "bytes": f.bytes},
			Confidence: 1.0, Evidence: EvidenceFrom(f.ev),
		})
		if host := ipName[f.dst]; host != "" {
			emitService(host, f.ev)
			res.Edges = append(res.Edges, model.Edge{
				Relation: model.RelConnectsTo, SrcKey: f.src, DstKey: host,
				Attributes: map[string]any{"count": f.count, "bytes": f.bytes, "via": "dns"},
				Confidence: 0.9, Evidence: EvidenceFrom(f.ev),
			})
		}
	}

	return res, nil
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{t}
	}
	return nil
}

func isIP(s string) bool {
	// Minimal check: contains a dot and only digits/dots (IPv4) or a colon
	// (IPv6). Good enough to separate A/AAAA answers from CNAMEs.
	if strings.Contains(s, ":") {
		return true
	}
	if !strings.Contains(s, ".") {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func recNum(rec map[string]any, key string) float64 {
	switch v := rec[key].(type) {
	case float64:
		return v
	}
	return 0
}

var _ = fmt.Sprintf
