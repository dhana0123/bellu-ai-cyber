package cloudquery

import "embed"

// embeddedCatalogs holds the provider table-mapping YAMLs compiled into the
// binary so the CloudQuery source works regardless of the process working
// directory or container layout (the files are not copied into service
// images). LoadCatalog falls back to these when the on-disk path is absent,
// matched by base name (mapping/<name>.yaml).
//
//go:embed mapping/*.yaml
var embeddedCatalogs embed.FS
