package normalize

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// inspectorVulns turns Amazon Inspector v2 findings (Steampipe table
// aws_inspector2_finding) into Vulnerability entities and HAS_VULN edges from
// the affected asset:
//
//	EC2 instance     -> Compute   (keyed by instance ARN)
//	ECR image        -> ContainerImage (keyed by sha256 digest)
//	Lambda function  -> Compute   (keyed by function ARN)
//
// Vulnerabilities are keyed "cve:<ID>", the same convention the CycloneDX
// normalizer uses, so an Inspector finding and an SBOM entry for the same CVE
// resolve to ONE Vulnerability node (vuln:<ID>).
//
// Only PACKAGE_VULNERABILITY findings carry a CVE id; network-reachability and
// code findings are skipped. Non-ACTIVE findings are skipped defensively even
// though the catalog already filters on status.
//
// When an asset cannot be mapped to a discovered entity key, the edge is still
// emitted with the raw Inspector resource id so the resolver records it as an
// unresolved edge (visible) instead of the link vanishing silently.
func inspectorVulns(events []rawevent.RawEvent) ([]model.Entity, []model.Edge) {
	// instance id (i-0abc...) -> instance ARN, from the discovered EC2 entities.
	instanceARN := map[string]string{}
	for _, e := range events {
		if e.Provider != "aws" || e.ResourceType != "aws_ec2_instance" {
			continue
		}
		if id := stringAttr(payloadMap(e), "instance_id"); id != "" && e.ResourceID != "" {
			instanceARN[id] = e.ResourceID
		}
	}

	var ents []model.Entity
	var edges []model.Edge
	seenVuln := map[string]bool{} // one Vulnerability entity per CVE
	seenEdge := map[string]bool{} // one HAS_VULN edge per (asset, CVE)

	for _, e := range events {
		if e.Provider != "aws" || e.ResourceType != "aws_inspector2_finding" {
			continue
		}
		attrs := payloadMap(e)
		if t := stringAttr(attrs, "type"); t != "" && t != "PACKAGE_VULNERABILITY" {
			continue
		}
		if s := stringAttr(attrs, "status"); s != "" && s != "ACTIVE" {
			continue
		}
		vulnID := strings.TrimSpace(stringAttr(attrs, "vulnerability_id"))
		if vulnID == "" {
			continue
		}
		vulnKey := "cve:" + vulnID

		if !seenVuln[vulnKey] {
			seenVuln[vulnKey] = true
			ents = append(ents, model.Entity{
				Kind: model.KindVulnerability, Key: vulnKey, Provider: "aws",
				Name:       vulnID,
				Attributes: inspectorVulnAttrs(attrs),
				Evidence:   EvidenceFrom(e),
			})
		}

		pkgName, pkgVersion, fixedIn := firstVulnerablePackage(attrs)
		resources, _ := attrs["resources"].([]any)
		for _, r := range resources {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			assetKey := inspectorAssetKey(stringAttr(rm, "Type"), stringAttr(rm, "Id"), instanceARN)
			if assetKey == "" {
				continue
			}
			edgeKey := assetKey + "|" + vulnKey
			if seenEdge[edgeKey] {
				continue
			}
			seenEdge[edgeKey] = true

			ea := map[string]any{
				"source":            "inspector2",
				"finding_arn":       e.ResourceID,
				"severity":          strings.ToLower(stringAttr(attrs, "severity")),
				"inspector_score":   recNum(attrs, "inspector_score"),
				"fix_available":     stringAttr(attrs, "fix_available"),
				"exploit_available": stringAttr(attrs, "exploit_available"),
			}
			if pkgName != "" {
				ea["package_name"] = pkgName
			}
			if pkgVersion != "" {
				ea["package_version"] = pkgVersion
			}
			if fixedIn != "" {
				ea["fixed_in_version"] = fixedIn
			}
			edges = append(edges, model.Edge{
				Relation: model.RelHasVuln, SrcKey: assetKey, DstKey: vulnKey,
				Attributes: ea, Confidence: 1.0, Evidence: EvidenceFrom(e),
			})
		}
	}
	return ents, edges
}

// inspectorVulnAttrs builds the Vulnerability entity attributes. Names follow
// the vocabulary the intel-correlation step already uses (severity,
// known_exploited) so both sources read the same on the node.
func inspectorVulnAttrs(attrs map[string]any) map[string]any {
	out := map[string]any{
		"source":            "inspector2",
		"severity":          strings.ToLower(stringAttr(attrs, "severity")),
		"inspector_score":   recNum(attrs, "inspector_score"),
		"fix_available":     stringAttr(attrs, "fix_available"),
		"exploit_available": stringAttr(attrs, "exploit_available"),
		"known_exploited":   stringAttr(attrs, "exploit_available") == "YES",
	}
	if v := stringAttr(attrs, "title"); v != "" {
		out["title"] = v
	}
	if v := stringAttr(attrs, "description"); v != "" {
		out["description"] = v
	}
	if v := stringAttr(attrs, "vendor_severity"); v != "" {
		out["vendor_severity"] = v
	}
	return out
}

// firstVulnerablePackage returns the name/version/fix of the first vulnerable
// package on a finding (Steampipe exposes vulnerable_packages as a JSON array
// with Go-style field names).
func firstVulnerablePackage(attrs map[string]any) (name, version, fixedIn string) {
	pkgs, _ := attrs["vulnerable_packages"].([]any)
	if len(pkgs) == 0 {
		return "", "", ""
	}
	pm, ok := pkgs[0].(map[string]any)
	if !ok {
		return "", "", ""
	}
	return stringAttr(pm, "Name"), stringAttr(pm, "Version"), stringAttr(pm, "FixedInVersion")
}

// inspectorAssetKey maps an Inspector resource (Type, Id) to the key of the
// entity the normalizer emits for that asset. It returns "" for resource types
// that do not correspond to a discovered entity (e.g. an ECR repository).
func inspectorAssetKey(resType, id string, instanceARN map[string]string) string {
	if id == "" {
		return ""
	}
	switch resType {
	case "AWS_EC2_INSTANCE":
		if arn, ok := instanceARN[id]; ok {
			return arn
		}
		return id // unmapped: surfaces as an unresolved edge
	case "AWS_ECR_CONTAINER_IMAGE":
		return digestFromInspectorID(id)
	case "AWS_LAMBDA_FUNCTION":
		return lambdaFunctionARN(id)
	}
	return ""
}

// digestFromInspectorID extracts the bare "sha256:<hex>" digest from an
// Inspector ECR image resource id, which ends in ".../<repo>/sha256:<hex>".
// Returns "" when no digest is present.
func digestFromInspectorID(id string) string {
	i := strings.Index(id, "sha256:")
	if i < 0 {
		return ""
	}
	d := id[i:]
	if j := strings.IndexAny(d, "?# \t"); j >= 0 {
		d = d[:j]
	}
	return d
}

// lambdaFunctionARN trims a Lambda ARN to the unqualified function ARN
// (arn:aws:lambda:<region>:<acct>:function:<name>), dropping any version or
// alias suffix such as ":$LATEST", so it matches the discovered function key.
func lambdaFunctionARN(id string) string {
	parts := strings.Split(id, ":")
	if len(parts) >= 7 && parts[0] == "arn" && parts[5] == "function" {
		return strings.Join(parts[:7], ":")
	}
	return id
}
