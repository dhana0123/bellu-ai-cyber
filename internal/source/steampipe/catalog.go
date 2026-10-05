// Package steampipe queries a running Steampipe service directly over its
// Postgres wire interface (port 9193) and emits a RawEvent per row.
//
// The set of tables to query, and how to extract identity/scope/region from
// each, is declared in a catalog YAML rather than hard-coded, so adding
// coverage never requires new Go code.
package steampipe

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// TableSpec describes how to collect one Steampipe table.
type TableSpec struct {
	// Table is the Steampipe table name, e.g. aws_iam_role.
	Table string `yaml:"table"`
	// ResourceType is the RawEvent resource_type, defaulting to Table.
	ResourceType string `yaml:"resource_type"`
	// IDColumn is the column holding the native resource id (arn, id, name).
	IDColumn string `yaml:"id_column"`
	// ScopeColumn holds the account/subscription/project id.
	ScopeColumn string `yaml:"scope_column"`
	// RegionColumn holds the region, when applicable.
	RegionColumn string `yaml:"region_column"`
	// Where is an optional SQL predicate appended to the query.
	Where string `yaml:"where"`
}

// Catalog is a provider's set of table specs.
type Catalog struct {
	// Provider is aws | azure | gcp | k8s.
	Provider string `yaml:"provider"`
	// Connection is the Steampipe connection name selected for these tables
	// (used only for labeling the collector; the service exposes all tables).
	Connection string      `yaml:"connection"`
	Tables     []TableSpec `yaml:"tables"`
}

// LoadCatalog reads a catalog YAML from path.
func LoadCatalog(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("steampipe: read catalog %s: %w", path, err)
	}
	var c Catalog
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("steampipe: parse catalog %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the catalog is well-formed.
func (c *Catalog) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("steampipe: catalog missing provider")
	}
	if len(c.Tables) == 0 {
		return fmt.Errorf("steampipe: catalog %s has no tables", c.Provider)
	}
	seen := map[string]bool{}
	for i, t := range c.Tables {
		if t.Table == "" {
			return fmt.Errorf("steampipe: catalog %s table[%d] missing table name", c.Provider, i)
		}
		if seen[t.Table] {
			return fmt.Errorf("steampipe: catalog %s duplicate table %q", c.Provider, t.Table)
		}
		seen[t.Table] = true
		if t.IDColumn == "" {
			return fmt.Errorf("steampipe: catalog %s table %q missing id_column", c.Provider, t.Table)
		}
	}
	return nil
}

// resourceType returns the resource_type for a spec, defaulting to the table
// name.
func (t TableSpec) resourceType() string {
	if t.ResourceType != "" {
		return t.ResourceType
	}
	return t.Table
}
