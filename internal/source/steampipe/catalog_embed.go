package steampipe

import "embed"

// embeddedCatalogs holds the provider catalog YAMLs compiled into the binary so
// the Steampipe source works regardless of the process working directory or
// container layout (the files are not copied into service images). LoadCatalog
// falls back to these when the on-disk path is absent.
//
//go:embed catalog/*.yaml
var embeddedCatalogs embed.FS
