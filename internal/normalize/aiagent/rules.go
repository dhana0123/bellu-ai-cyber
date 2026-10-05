// Package aiagent derives AIAgent entities from repositories that carry AI-BOM
// signals (MCP configs, agent-framework dependencies, prompt files). Detection
// rules are data-driven from a YAML file so new frameworks are added without
// code changes.
package aiagent

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rules configures AI agent detection.
type Rules struct {
	// FrameworkPurlSubstrings: a package purl containing any of these marks the
	// repo as an agent (e.g. "pkg:pypi/langchain").
	FrameworkPurlSubstrings []string `yaml:"framework_purl_substrings"`
	// FrameworkNames: component names that indicate an agent framework.
	FrameworkNames []string `yaml:"framework_names"`
	// RequireMCP: if true, presence of any MCP server alone marks an agent.
	RequireMCP bool `yaml:"require_mcp"`
}

// DefaultRules returns the built-in detection rules.
func DefaultRules() Rules {
	return Rules{
		FrameworkPurlSubstrings: []string{
			"langchain", "langgraph", "llama-index", "llama_index",
			"openai", "anthropic", "autogen", "crewai", "semantic-kernel",
			"haystack", "dspy", "guidance",
		},
		FrameworkNames: []string{
			"langchain", "openai", "anthropic", "autogen", "crewai",
		},
		RequireMCP: true,
	}
}

// LoadRules reads detection rules from path, falling back to defaults when the
// file is absent.
func LoadRules(path string) (Rules, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultRules(), nil
		}
		return Rules{}, err
	}
	var r Rules
	if err := yaml.Unmarshal(b, &r); err != nil {
		return Rules{}, err
	}
	// Merge defaults for any empty section so a partial file still works.
	d := DefaultRules()
	if len(r.FrameworkPurlSubstrings) == 0 {
		r.FrameworkPurlSubstrings = d.FrameworkPurlSubstrings
	}
	if len(r.FrameworkNames) == 0 {
		r.FrameworkNames = d.FrameworkNames
	}
	return r, nil
}

// MatchPurl reports whether a package purl indicates an agent framework.
func (r Rules) MatchPurl(purl string) bool {
	p := strings.ToLower(purl)
	for _, s := range r.FrameworkPurlSubstrings {
		if strings.Contains(p, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// MatchName reports whether a component name indicates an agent framework.
func (r Rules) MatchName(name string) bool {
	n := strings.ToLower(name)
	for _, s := range r.FrameworkNames {
		if strings.Contains(n, strings.ToLower(s)) {
			return true
		}
	}
	return false
}
