package main

import (
	"github.com/bellu/bellu-ai-cyber/internal/source"
	"github.com/bellu/bellu-ai-cyber/internal/source/cyclonedx"
	"github.com/bellu/bellu-ai-cyber/internal/source/fixture"
	"github.com/bellu/bellu-ai-cyber/internal/source/steampipe"
	"github.com/bellu/bellu-ai-cyber/internal/source/zeek"
)

// buildRegistry wires all available sources. New sources are registered here
// as later tasks add them (zeek).
func buildRegistry() *source.Registry {
	r := source.NewRegistry()
	r.Register("fixture", fixture.Factory)
	r.Register("cyclonedx", cyclonedx.Factory)
	r.Register("steampipe", steampipe.Factory)
	r.Register("zeek", zeek.Factory)
	return r
}
