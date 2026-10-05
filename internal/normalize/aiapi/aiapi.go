// Package aiapi recognizes well-known AI API endpoints by hostname so network
// flows to them can be tagged as reaching an AIService.
package aiapi

import "strings"

// patterns is a curated list of known AI API host patterns. A leading "*."
// matches any subdomain; "*" inside matches a label run.
var patterns = []struct {
	pattern string
	service string
}{
	{"api.openai.com", "OpenAI"},
	{"*.openai.azure.com", "Azure OpenAI"},
	{"api.anthropic.com", "Anthropic"},
	{"generativelanguage.googleapis.com", "Google Gemini"},
	{"*.googleapis.com", "Google AI"},
	{"bedrock-runtime.*.amazonaws.com", "AWS Bedrock"},
	{"bedrock.*.amazonaws.com", "AWS Bedrock"},
	{"*.sagemaker.*.amazonaws.com", "AWS SageMaker"},
	{"*.aiplatform.googleapis.com", "Vertex AI"},
	{"api.cohere.ai", "Cohere"},
	{"api.mistral.ai", "Mistral"},
	{"huggingface.co", "Hugging Face"},
	{"api-inference.huggingface.co", "Hugging Face Inference"},
}

// Match reports whether host is a known AI API endpoint and returns the service
// name. Matching is case-insensitive.
func Match(host string) (service string, ok bool) {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range patterns {
		if hostMatch(p.pattern, h) {
			return p.service, true
		}
	}
	return "", false
}

// hostMatch does label-aware glob matching with "*" matching a single label
// run (no dots crossing unless the pattern has them). We reuse a simple glob
// where "*" matches any run including dots, which is sufficient for these
// patterns combined with the fixed anchors around them.
func hostMatch(pattern, host string) bool {
	// Leading "*." means any subdomain prefix (including none).
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".openai.azure.com"
		return host == pattern[2:] || strings.HasSuffix(host, suffix)
	}
	return glob(pattern, host)
}

// glob matches "*" (any run) and "?" (one char).
func glob(pattern, s string) bool {
	var pi, si int
	star := -1
	var ss int
	for si < len(s) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]) {
			pi++
			si++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			star = pi
			ss = si
			pi++
		} else if star != -1 {
			pi = star + 1
			ss++
			si = ss
		} else {
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
