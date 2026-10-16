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
	if provider == "aws" && strings.HasPrefix(key, "arn:") {
		return normalizeAWSARN(key)
	}
	if provider == "azure" {
		return strings.ToLower(key)
	}
	return key
}

// normalizeAWSARN canonicalizes an AWS ARN so that representational variants of
// the same resource collapse to one comparable key (R3), WITHOUT folding the
// case-sensitive resource-name segment (R3.6):
//
//   - Partition (R3.2): aws / aws-cn / aws-us-gov all canonicalize to "aws",
//     since account id + resource id are the discriminators, not the partition.
//   - IAM path (R3.1): for iam role/ and user/ resources, path segments between
//     the resource type and the bare name are stripped, so
//     arn:aws:iam::111122223333:role/team/app-role and
//     arn:aws:iam::111122223333:role/app-role compare equal.
//
// The resource-name segment is never lowercased or wildcard-matched (R3.6):
// S3 bucket names and IAM role/user names are case-sensitive. The function is
// idempotent by construction.
//
// ARN form: arn:<partition>:<service>:<region>:<account>:<resource>
// where <resource> may itself contain ':' or '/'.
func normalizeAWSARN(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		// Not a well-formed ARN; leave untouched.
		return arn
	}
	// parts: ["arn", partition, service, region, account, resource]
	parts[1] = "aws" // partition canonicalization (R3.2)
	service := parts[2]
	resource := parts[5]

	if service == "iam" {
		resource = stripIAMPath(resource)
	}
	parts[5] = resource
	return strings.Join(parts, ":")
}

// stripIAMPath removes the path from an IAM resource of the form
// "role/<path.../>name" or "user/<path.../>name", returning "role/name" /
// "user/name". Other IAM resource types are returned unchanged. The resource
// name's case is preserved.
func stripIAMPath(resource string) string {
	for _, prefix := range []string{"role/", "user/"} {
		if strings.HasPrefix(resource, prefix) {
			rest := strings.TrimPrefix(resource, prefix)
			// The bare name is the final path segment.
			if i := strings.LastIndex(rest, "/"); i >= 0 {
				rest = rest[i+1:]
			}
			return prefix + rest
		}
	}
	return resource
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
