package resolve

import (
	"os"

	"gopkg.in/yaml.v3"
)

// MappingsFile is the optional repo->image fallback used by the resolver when
// an image has no OCI source label. This is the weakest link in resolution.
type MappingsFile struct {
	// RepoToImage maps a repository URL to an image digest (sha256:...).
	RepoToImage map[string]string `yaml:"repo_to_image"`
}

// LoadMappings reads a mappings file. A missing file is not an error.
func LoadMappings(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var mf MappingsFile
	if err := yaml.Unmarshal(b, &mf); err != nil {
		return nil, err
	}
	if mf.RepoToImage == nil {
		return map[string]string{}, nil
	}
	return mf.RepoToImage, nil
}
