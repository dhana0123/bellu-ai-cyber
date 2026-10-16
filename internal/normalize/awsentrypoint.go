package normalize

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// entrypoint_subtype values classify an EntryPoint entity. Like
// network_subtype, this vocabulary is open-ended; adding a GCP/Azure entry
// point is additive and requires no change to model.KindEntryPoint (R9.4).
const (
	subRESTAPI = "rest_api"
	subHTTPAPI = "http_api"
)

// AWSEntryPoint maps AWS API Gateway resources to EntryPoint entities and their
// internet-exposure / backend edges (R8). It is a standalone Steampipe
// normalizer processing provider=="aws" events only.
//
// Edges:
//
//	CONTAINS    CloudAccount -> EntryPoint
//	EXPOSED_TO  EntryPoint -> 0.0.0.0/0 (REST unless PRIVATE; HTTP always)
//	CALLS       EntryPoint -> Lambda function ARN (per distinct target)
type AWSEntryPoint struct{}

// Sources implements Normalizer.
func (AWSEntryPoint) Sources() []string { return []string{"steampipe"} }

// Normalize implements Normalizer.
func (AWSEntryPoint) Normalize(events []rawevent.RawEvent) (Result, error) {
	var res Result
	internetNeeded := false

	for _, e := range events {
		if e.Provider != "aws" {
			continue
		}
		attrs := payloadMap(e)
		switch e.ResourceType {
		case "aws_api_gateway_rest_api":
			res.Entities = append(res.Entities, entryPointEntity(e, attrs, subRESTAPI))
			res.Edges = append(res.Edges, containsEdge(e, e.ResourceID))
			// REST APIs are internet-exposed unless the endpoint configuration
			// restricts them to PRIVATE (R8.2, R8.4).
			if !restAPIPrivate(attrs) {
				res.Edges = append(res.Edges, exposedToInternet(e))
				internetNeeded = true
			}
			res.Edges = append(res.Edges, lambdaCallEdges(e, attrs)...)
		case "aws_api_gatewayv2_api":
			res.Entities = append(res.Entities, entryPointEntity(e, attrs, subHTTPAPI))
			res.Edges = append(res.Edges, containsEdge(e, e.ResourceID))
			// HTTP APIs are always internet-exposed (no private option) (R8.5).
			res.Edges = append(res.Edges, exposedToInternet(e))
			internetNeeded = true
			res.Edges = append(res.Edges, lambdaCallEdges(e, attrs)...)
		}
	}

	// Emit the synthetic internet NetworkEndpoint once if any EXPOSED_TO was
	// produced, mirroring aws.go so the exposure edge resolves.
	if internetNeeded {
		res.Entities = append(res.Entities, model.Entity{
			Kind: model.KindNetworkEndpoint, Key: "0.0.0.0/0", Provider: "network",
			Name: "internet (0.0.0.0/0)", Attributes: map[string]any{"cidr": "0.0.0.0/0"},
		})
	}

	return res, nil
}

func entryPointEntity(e rawevent.RawEvent, attrs map[string]any, subtype string) model.Entity {
	a := attrs
	if a == nil {
		a = map[string]any{}
	}
	a["entrypoint_subtype"] = subtype
	return model.Entity{
		Kind: model.KindEntryPoint, Key: e.ResourceID, Provider: "aws",
		Name:       firstNonEmpty(stringAttr(attrs, "name"), stringAttr(attrs, "title"), e.ResourceID),
		Attributes: a, Evidence: EvidenceFrom(e),
	}
}

func exposedToInternet(e rawevent.RawEvent) model.Edge {
	return model.Edge{
		Relation: model.RelExposedTo, SrcKey: e.ResourceID, DstKey: "0.0.0.0/0",
		Attributes: map[string]any{"source": "api_gateway"},
		Confidence: 1.0, Evidence: EvidenceFrom(e),
	}
}

// restAPIPrivate reports whether a REST API's endpoint configuration restricts
// it to a PRIVATE endpoint type (R8.4).
func restAPIPrivate(attrs map[string]any) bool {
	cfg, ok := attrs["endpoint_configuration"].(map[string]any)
	if !ok {
		return false
	}
	var types []string
	types = append(types, toStringSlice(cfg["Types"])...)
	types = append(types, toStringSlice(cfg["types"])...)
	for _, t := range types {
		if strings.EqualFold(t, "PRIVATE") {
			return true
		}
	}
	return false
}

// lambdaCallEdges emits one CALLS edge (EntryPoint -> Lambda ARN) per distinct
// Lambda function referenced by the API's integration/target data. Best-effort:
// Steampipe integration data is often sparse, so this scans several plausible
// shapes and extracts any ARN that names a Lambda function.
func lambdaCallEdges(e rawevent.RawEvent, attrs map[string]any) []model.Edge {
	seen := map[string]bool{}
	var out []model.Edge
	for _, arn := range lambdaTargets(attrs) {
		if arn == "" || seen[arn] {
			continue
		}
		seen[arn] = true
		out = append(out, model.Edge{
			Relation: model.RelCalls, SrcKey: e.ResourceID, DstKey: arn,
			Confidence: 1.0, Evidence: EvidenceFrom(e),
		})
	}
	return out
}

// lambdaTargets gathers Lambda function ARNs from integration/target columns.
func lambdaTargets(attrs map[string]any) []string {
	var out []string
	collect := func(v any) {
		for _, item := range toAnySlice(v) {
			im, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, k := range []string{"IntegrationUri", "integration_uri", "Uri", "uri", "target"} {
				if arn := lambdaARNFrom(stringAttr(im, k)); arn != "" {
					out = append(out, arn)
				}
			}
		}
	}
	collect(attrs["integrations"])
	collect(attrs["routes"])
	// Flattened single-value hints.
	for _, k := range []string{"integration_uri", "target"} {
		if arn := lambdaARNFrom(stringAttr(attrs, k)); arn != "" {
			out = append(out, arn)
		}
	}
	return out
}

// lambdaARNFrom extracts a Lambda function ARN from an integration URI/target
// string. API Gateway Lambda integration URIs embed the function ARN, e.g.
// arn:aws:apigateway:REGION:lambda:path/2015-03-31/functions/<LAMBDA_ARN>/invocations.
func lambdaARNFrom(s string) string {
	if s == "" {
		return ""
	}
	// Direct function ARN.
	if strings.HasPrefix(s, "arn:") && strings.Contains(s, ":lambda:") && strings.Contains(s, ":function:") {
		// If embedded in a path, extract the inner function ARN.
		if i := strings.Index(s, "/functions/"); i >= 0 {
			rest := s[i+len("/functions/"):]
			rest = strings.TrimSuffix(rest, "/invocations")
			if strings.HasPrefix(rest, "arn:") {
				return rest
			}
		}
		return s
	}
	if i := strings.Index(s, "/functions/"); i >= 0 {
		rest := s[i+len("/functions/"):]
		rest = strings.TrimSuffix(rest, "/invocations")
		if strings.HasPrefix(rest, "arn:") && strings.Contains(rest, ":function:") {
			return rest
		}
	}
	return ""
}
