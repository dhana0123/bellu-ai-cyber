// Package config loads bellu.yaml and applies environment overrides.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level bellu configuration.
type Config struct {
	Postgres PostgresConfig `yaml:"postgres"`
	Memgraph MemgraphConfig `yaml:"memgraph"`
	Redaction RedactionConfig `yaml:"redaction"`
	Sources  map[string]map[string]any `yaml:"sources"`
}

// PostgresConfig configures the evidence store connection.
type PostgresConfig struct {
	DSN string `yaml:"dsn"`
}

// MemgraphConfig configures the graph writer connection.
type MemgraphConfig struct {
	URI      string `yaml:"uri"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// RedactionConfig configures the redaction stage.
type RedactionConfig struct {
	// KeyPatterns are case-insensitive regexes matched against JSON object
	// keys; matching values are replaced with the redaction marker.
	KeyPatterns []string `yaml:"key_patterns"`
	// JSONPaths are dotted paths (supporting * wildcard segments) whose values
	// are removed entirely.
	JSONPaths []string `yaml:"json_paths"`
}

// Default returns a config with sensible local defaults.
func Default() Config {
	return Config{
		Postgres: PostgresConfig{
			DSN: "postgres://bellu:bellu@localhost:5432/bellu?sslmode=disable",
		},
		Memgraph: MemgraphConfig{
			URI:      "bolt://localhost:7687",
			Username: "bellu",
			Password: "bellu",
		},
		Redaction: RedactionConfig{
			KeyPatterns: []string{
				"(?i)secret", "(?i)password", "(?i)token", "(?i)api[-_]?key",
				"(?i)access[-_]?key", "(?i)private[-_]?key", "(?i)credential",
			},
			JSONPaths: []string{
				// Lambda / Functions / Cloud Run environment variables.
				"environment.variables",
				"environment_variables",
				// K8s secret data and configmap values.
				"data",
				"string_data",
			},
		},
		Sources: map[string]map[string]any{},
	}
}

// Load reads path (if it exists) over the defaults, then applies env
// overrides. A missing file is not an error; defaults are used.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			if err := yaml.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("config: parse %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("config: read %s: %w", path, err)
		}
	}
	applyEnv(&cfg)
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("BELLU_POSTGRES_DSN"); v != "" {
		cfg.Postgres.DSN = v
	}
	if v := os.Getenv("BELLU_MEMGRAPH_URI"); v != "" {
		cfg.Memgraph.URI = v
	}
	if v := os.Getenv("BELLU_MEMGRAPH_USER"); v != "" {
		cfg.Memgraph.Username = v
	}
	if v := os.Getenv("BELLU_MEMGRAPH_PASSWORD"); v != "" {
		cfg.Memgraph.Password = v
	}
}
