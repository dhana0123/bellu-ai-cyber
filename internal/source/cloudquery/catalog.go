// Package cloudquery reads cloud inventory that a CloudQuery sync has written
// into a `cq_staging` schema in the customer PostgreSQL and emits a RawEvent
// per row. It replaces the Steampipe source at the same pipeline boundary, so
// every downstream normalizer is unchanged.
//
// The set of tables to read, and how to extract identity/scope/region from
// each, is declared in a table-mapping YAML rather than hard-coded, so adding
// coverage never requires new Go code. The mapping reuses the Steampipe
// catalog's field shape verbatim and adds a single new field, `cq_table`,
// that names the CloudQuery staging table when it differs from the Bellu
// table name (CloudQuery pluralizes, e.g. aws_iam_role -> aws_iam_roles).
package cloudquery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// TableSpec describes how to read one table from the CloudQuery staging schema.
//
// Every field except Table and CQTable mirrors the Steampipe catalog's
// TableSpec one-for-one (same YAML keys), so a Steampipe catalog translates
// into a CloudQuery mapping by copying the rows and adding `cq_table` where the
// CloudQuery staging table name differs from the Bellu table name.
type TableSpec struct {
	// Table is the Bellu table name, kept identical to the Steampipe catalog
	// (e.g. aws_iam_role) so the collector swap is a data migration, not a
	// schema change downstream.
	Table string `yaml:"table"`
	// CQTable is the CloudQuery staging table name when it differs from Table
	// (e.g. aws_iam_role -> aws_iam_roles). Empty means the staging table name
	// equals Table; see cqTable().
	CQTable string `yaml:"cq_table"`
	// ResourceType is the RawEvent resource_type, defaulting to Table.
	ResourceType string `yaml:"resource_type"`
	// IDColumn is the column holding the native resource id (arn, id, name).
	IDColumn string `yaml:"id_column"`
	// ScopeColumn holds the account/subscription/project id.
	ScopeColumn string `yaml:"scope_column"`
	// RegionColumn holds the region, when applicable.
	RegionColumn string `yaml:"region_column"`
	// Where is an optional SQL predicate appended to the staging query.
	Where string `yaml:"where"`
	// Regions optionally restricts the read to a fixed set of regions, for
	// services that are not available in every region. When Regions is
	// non-empty AND the table has a RegionColumn, the collector adds a
	// `<region_column> IN (...)` predicate so only supported regions are read.
	// An empty Regions (the default) preserves the all-regions behavior.
	Regions []string `yaml:"regions"`
	// Capability names the connector capability that grants the permissions
	// this table needs (e.g. "eks", "workload", "ai"). Empty means ungated —
	// the table is always collected. The special value "core" is treated as
	// always-on. The collector skips a table whose capability is not in the
	// connection's enabled set. It is intentionally NOT required by
	// Catalog.Validate(): an empty capability is valid and means ungated.
	Capability string `yaml:"capability"`
}

// Catalog is a provider's set of table specs (the table-mapping file).
type Catalog struct {
	// Provider is aws | azure | gcp | k8s.
	Provider string `yaml:"provider"`
	// Connection is the CloudQuery connection name selected for these tables
	// (used only for labeling the collector).
	Connection string      `yaml:"connection"`
	Tables     []TableSpec `yaml:"tables"`
}

// LoadCatalog reads a mapping YAML from p. If the file is not present on disk
// (e.g. inside a service image that only ships the binary), it falls back to
// the mapping embedded in the binary, matched by base name
// (mapping/<name>.yaml).
func LoadCatalog(p string) (*Catalog, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		if eb, eerr := embeddedCatalogs.ReadFile("mapping/" + path.Base(p)); eerr == nil {
			b, err = eb, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("cloudquery: read mapping %s: %w", p, err)
	}
	var c Catalog
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("cloudquery: parse mapping %s: %w", p, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the mapping is well-formed.
func (c *Catalog) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("cloudquery: mapping missing provider")
	}
	if len(c.Tables) == 0 {
		return fmt.Errorf("cloudquery: mapping %s has no tables", c.Provider)
	}
	seen := map[string]bool{}
	for i, t := range c.Tables {
		if t.Table == "" {
			return fmt.Errorf("cloudquery: mapping %s table[%d] missing table name", c.Provider, i)
		}
		if seen[t.Table] {
			return fmt.Errorf("cloudquery: mapping %s duplicate table %q", c.Provider, t.Table)
		}
		seen[t.Table] = true
		if t.IDColumn == "" {
			return fmt.Errorf("cloudquery: mapping %s table %q missing id_column", c.Provider, t.Table)
		}
	}
	return nil
}

// cqTable returns the CloudQuery staging table name for a spec, defaulting to
// the Bellu table name when CQTable is empty.
func (t TableSpec) cqTable() string {
	if t.CQTable != "" {
		return t.CQTable
	}
	return t.Table
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
// Regions list are set, so tables without region restrictions keep reading
// every region. The region values come from the vetted mapping, not user
// input; they are still emitted as single-quoted, quote-escaped string
// literals so a stray value can never break out of the IN list.
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
