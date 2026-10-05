package cyclonedx

import (
	"strings"
	"time"

	cdx "github.com/CycloneDX/cyclonedx-go"
)

// detectTool identifies the producing tool from metadata.tools. Returns one of
// syft, trivy, cdxgen, or "unknown".
func detectTool(bom *cdx.BOM) string {
	if bom.Metadata == nil || bom.Metadata.Tools == nil {
		return "unknown"
	}
	t := bom.Metadata.Tools
	var names []string
	if t.Tools != nil {
		for _, tool := range *t.Tools {
			names = append(names, strings.ToLower(tool.Vendor+" "+tool.Name))
		}
	}
	if t.Components != nil {
		for _, c := range *t.Components {
			names = append(names, strings.ToLower(c.Group+" "+c.Name))
		}
	}
	joined := strings.Join(names, " ")
	switch {
	case strings.Contains(joined, "syft"):
		return "syft"
	case strings.Contains(joined, "trivy"):
		return "trivy"
	case strings.Contains(joined, "cdxgen"):
		return "cdxgen"
	}
	return "unknown"
}

// bomTimestamp parses metadata.timestamp, defaulting to now (UTC).
func bomTimestamp(bom *cdx.BOM) time.Time {
	if bom.Metadata != nil && bom.Metadata.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339, bom.Metadata.Timestamp); err == nil {
			return ts.UTC()
		}
	}
	return time.Now().UTC()
}

// providerForBOM returns "image" for container subjects and "repo" otherwise.
func providerForBOM(bom *cdx.BOM) string {
	if bom.Metadata != nil && bom.Metadata.Component != nil {
		if bom.Metadata.Component.Type == cdx.ComponentTypeContainer {
			return "image"
		}
	}
	return "repo"
}

// subjectResource picks the resource type and native id for the BOM subject.
// Container images are keyed by digest; everything else by repo URL or purl.
func subjectResource(bom *cdx.BOM, mc *cdx.Component) (rt, rid string) {
	if mc.Type == cdx.ComponentTypeContainer {
		if d := imageDigest(mc); d != "" {
			return RTImage, d
		}
		return RTImage, "image:" + mc.Name + "@" + mc.Version
	}
	if r := repoURL(bom, mc); r != "" {
		return RTRepository, r
	}
	if mc.PackageURL != "" {
		return RTRepository, mc.PackageURL
	}
	return RTRepository, "repo:" + mc.Name
}

// imageDigest extracts a sha256 digest for a container subject from its purl,
// hashes, or properties.
func imageDigest(mc *cdx.Component) string {
	// purl form: pkg:oci/name@sha256:...?...
	if i := strings.Index(mc.PackageURL, "sha256:"); i >= 0 {
		d := mc.PackageURL[i:]
		if q := strings.IndexAny(d, "?#"); q >= 0 {
			d = d[:q]
		}
		return d
	}
	if mc.Hashes != nil {
		for _, h := range *mc.Hashes {
			if h.Algorithm == cdx.HashAlgoSHA256 && h.Value != "" {
				return "sha256:" + h.Value
			}
		}
	}
	// Syft/Trivy expose the digest via properties.
	for _, k := range []string{"aquasecurity:trivy:RepoDigest", "syft:image:labels:org.opencontainers.image.digest"} {
		if v := propValue(mc.Properties, k); v != "" {
			if i := strings.Index(v, "sha256:"); i >= 0 {
				return v[i:]
			}
		}
	}
	return ""
}

// repoURL extracts the source repository URL from the OCI source label (via
// properties) or a VCS external reference.
func repoURL(bom *cdx.BOM, mc *cdx.Component) string {
	if v := propValue(mc.Properties, "org.opencontainers.image.source"); v != "" {
		return v
	}
	if mc.ExternalReferences != nil {
		for _, ref := range *mc.ExternalReferences {
			if ref.Type == cdx.ERTypeVCS && ref.URL != "" {
				return ref.URL
			}
		}
	}
	if bom.ExternalReferences != nil {
		for _, ref := range *bom.ExternalReferences {
			if ref.Type == cdx.ERTypeVCS && ref.URL != "" {
				return ref.URL
			}
		}
	}
	return ""
}

func propValue(props *[]cdx.Property, name string) string {
	if props == nil {
		return ""
	}
	for _, p := range *props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

func propsToMap(props *[]cdx.Property) map[string]string {
	if props == nil {
		return nil
	}
	m := make(map[string]string, len(*props))
	for _, p := range *props {
		m[p.Name] = p.Value
	}
	return m
}

func hashes(hs *[]cdx.Hash) map[string]string {
	if hs == nil {
		return nil
	}
	m := make(map[string]string, len(*hs))
	for _, h := range *hs {
		m[string(h.Algorithm)] = h.Value
	}
	return m
}

func sourceName(v *cdx.Vulnerability) string {
	if v.Source != nil {
		return v.Source.Name
	}
	return ""
}

func ratings(v *cdx.Vulnerability) []map[string]any {
	if v.Ratings == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(*v.Ratings))
	for _, r := range *v.Ratings {
		out = append(out, map[string]any{
			"severity": string(r.Severity),
			"score":    r.Score,
			"method":   string(r.Method),
			"vector":   r.Vector,
		})
	}
	return out
}

func affects(v *cdx.Vulnerability) []string {
	if v.Affects == nil {
		return nil
	}
	out := make([]string, 0, len(*v.Affects))
	for _, a := range *v.Affects {
		out = append(out, a.Ref)
	}
	return out
}

// derefSlice returns the underlying slice of a pointer-to-slice, or nil.
func derefSlice[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}
