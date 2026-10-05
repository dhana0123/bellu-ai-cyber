package resolve

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// NormalizeKey applies deterministic, strong-key normalization rules so that
// different source representations of the same entity collapse to one key:
//
//   - Image digests: strip registry/tag noise, keep the bare sha256:... digest.
//   - Azure resource IDs: lowercase.
//   - Repository URLs: canonicalize ssh/https forms and drop a trailing .git.
//
// These are all deterministic strong-key rules. Weak-key linking (repo->image)
// is handled separately with lower confidence and never silently merges nodes.
func NormalizeKey(kind model.Kind, provider, key string) string {
	switch kind {
	case model.KindContainerImage:
		return normalizeImageKey(key)
	case model.KindRepository:
		return NormalizeRepoURL(key)
	}
	if provider == "azure" {
		return strings.ToLower(key)
	}
	return key
}

// normalizeImageKey reduces any image reference to its bare sha256 digest when
// one is present, so ECR, K8s imageID and SBOM purl all collapse to the same
// key. If no digest is present the input is returned unchanged.
func normalizeImageKey(key string) string {
	if i := strings.Index(key, "sha256:"); i >= 0 {
		d := key[i:]
		// Trim anything after the hex digest (query params, tags).
		if q := strings.IndexAny(d, "?# \t"); q >= 0 {
			d = d[:q]
		}
		return d
	}
	return key
}

// NormalizeRepoURL canonicalizes a git repository URL:
//   - git@host:org/repo(.git)  -> https://host/org/repo
//   - ssh://git@host/org/repo  -> https://host/org/repo
//   - trailing .git removed, trailing slash removed, lowercased host
func NormalizeRepoURL(raw string) string {
	u := strings.TrimSpace(raw)
	u = strings.TrimSuffix(u, "/")

	// scp-like syntax: git@host:org/repo
	if strings.HasPrefix(u, "git@") {
		rest := strings.TrimPrefix(u, "git@")
		if i := strings.Index(rest, ":"); i >= 0 {
			host := rest[:i]
			path := rest[i+1:]
			u = "https://" + host + "/" + path
		}
	}
	// ssh:// and git:// -> https://
	for _, p := range []string{"ssh://git@", "ssh://", "git://"} {
		if strings.HasPrefix(u, p) {
			u = "https://" + strings.TrimPrefix(u, p)
			break
		}
	}
	u = strings.TrimSuffix(u, ".git")
	return u
}
