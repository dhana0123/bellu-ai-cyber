// Package steampipe queries a running Steampipe service directly over its
// Postgres wire interface (port 9193) and emits a RawEvent per row.
//
// The set of tables to query, and how to extract identity/scope/region from
// each, is declared in a catalog YAML rather than hard-coded, so adding
// coverage never requires new Go code.
package steampipe

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

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
	// Regions optionally restricts the query to a fixed set of regions. It is
	// used for services that are not available in every AWS region (e.g.
	// Amazon Bedrock): querying such a table with regions = ["*"] fans the
	// call out to regions where the operation does not exist, which AWS answers
	// with a 404 UnknownOperationException. When Regions is non-empty AND the
	// table has a RegionColumn, the collector adds a
	// `<region_column> IN (...)` predicate so only supported regions are
	// queried. An empty Regions (the default) preserves the all-regions
	// behavior for every other table.
	Regions []string `yaml:"regions"`
	// Capability names the connector capability that grants the IAM permissions
	// this table needs (e.g. "eks", "workload", "ai"). Empty means ungated —
	// the table is always collected. The special value "core" is treated as
	// always-on. The collector skips a table whose capability is not in the
	// connection's enabled set, so no AccessDenied is produced for an ungranted
	// capability. It is intentionally NOT required by Catalog.Validate(): an
	// empty capability is valid and means ungated.
	Capability string `yaml:"capability"`
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

// LoadCatalog reads a catalog YAML from p. If the file is not present on disk
// (e.g. inside a service image that only ships the binary), it falls back to
// the catalog embedded in the binary, matched by base name
// (catalog/<name>.yaml).
func LoadCatalog(p string) (*Catalog, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		if eb, eerr := embeddedCatalogs.ReadFile("catalog/" + path.Base(p)); eerr == nil {
			b, err = eb, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("steampipe: read catalog %s: %w", p, err)
	}
	var c Catalog
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("steampipe: parse catalog %s: %w", p, err)
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

// whereClause builds the SQL predicate (without the leading WHERE keyword) for
// a table, combining an explicit Where predicate with an optional region
// restriction. It returns an empty string when neither applies.
//
// Region filtering only engages when BOTH a RegionColumn and a non-empty
// Regions list are set, so tables without region restrictions are untouched
// and keep querying every region. The region values come from the vetted
// catalog, not user input; they are still emitted as single-quoted,
// quote-escaped string literals so a stray value can never break out of the
// IN list.
func (t TableSpec) whereClause() string {
	var preds []string
	if t.Where != "" {
		preds = append(preds, t.Where)
	}
	if t.RegionColumn != "" && len(t.Regions) > 0 {
		lits := make([]string, 0, len(t.Regions))
		for _, r := range t.Regions {
			if r == "" {
				continue
			}
			lits = append(lits, "'"+strings.ReplaceAll(r, "'", "''")+"'")
		}
		if len(lits) > 0 {
			preds = append(preds, t.RegionColumn+" IN ("+strings.Join(lits, ", ")+")")
		}
	}
	return strings.Join(preds, " AND ")
}
