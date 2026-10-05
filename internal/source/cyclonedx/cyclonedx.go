// Package cyclonedx reads CycloneDX BOM JSON (from Syft, Trivy, cdxgen and
// cdxgen aibom) and emits RawEvents for the BOM subject, components, services,
// dependencies and vulnerabilities.
//
// One adapter handles all producers because they all emit CycloneDX JSON. The
// producing tool is detected from metadata.tools so downstream consumers can
// weight evidence by source.
package cyclonedx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cdx "github.com/CycloneDX/cyclonedx-go"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// Resource type strings used in emitted RawEvents.
const (
	RTImage      = "cyclonedx.image"
	RTRepository = "cyclonedx.repository"
	RTComponent  = "cyclonedx.component"
	RTService    = "cyclonedx.service"
	RTDependency = "cyclonedx.dependency"
	RTVuln       = "cyclonedx.vulnerability"
)

// Source reads one or more BOM files and streams RawEvents.
type Source struct {
	paths []string // files and/or directories
}

// New returns a Source that reads the given files or directories.
func New(paths ...string) *Source { return &Source{paths: paths} }

// Factory builds a Source from cfg["file"] or cfg["dir"].
func Factory(cfg map[string]any) (source.Source, error) {
	var paths []string
	if f, ok := cfg["file"].(string); ok && f != "" {
		paths = append(paths, f)
	}
	if d, ok := cfg["dir"].(string); ok && d != "" {
		paths = append(paths, d)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("cyclonedx: config requires \"file\" or \"dir\"")
	}
	return New(paths...), nil
}

// Name implements source.Source.
func (s *Source) Name() string { return "cyclonedx" }

// Collect walks the configured paths, parses each BOM, and streams events.
func (s *Source) Collect(ctx context.Context, run source.Run) (<-chan rawevent.RawEvent, <-chan error) {
	events := make(chan rawevent.RawEvent)
	errs := make(chan error, 4)

	go func() {
		defer close(events)
		defer close(errs)

		files, err := s.resolveFiles()
		if err != nil {
			errs <- err
			return
		}
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				errs <- err
				return
			}
			if err := s.emitFile(ctx, run, f, events); err != nil {
				errs <- fmt.Errorf("cyclonedx: %s: %w", f, err)
			}
		}
	}()

	return events, errs
}

func (s *Source) resolveFiles() ([]string, error) {
	var out []string
	for _, p := range s.paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cyclonedx: stat %s: %w", p, err)
		}
		if info.IsDir() {
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.ToLower(e.Name())
				if strings.HasSuffix(name, ".json") {
					out = append(out, filepath.Join(p, e.Name()))
				}
			}
		} else {
			out = append(out, p)
		}
	}
	return out, nil
}

// emitFile parses a single BOM and emits its events.
func (s *Source) emitFile(ctx context.Context, run source.Run, path string, out chan<- rawevent.RawEvent) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var bom cdx.BOM
	if err := cdx.NewBOMDecoder(f, cdx.BOMFileFormatJSON).Decode(&bom); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	tool := detectTool(&bom)
	observed := bomTimestamp(&bom)
	collector := "cyclonedx:" + tool

	emit := func(rt, rid string, payload any) error {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		ev := rawevent.RawEvent{
			RunID:        run.RunID,
			Source:       "cyclonedx",
			Collector:    collector,
			Provider:     providerForBOM(&bom),
			ResourceType: rt,
			ResourceID:   rid,
			ObservedAt:   observed,
			Payload:      json.RawMessage(b),
		}
		ev.Finalize()
		select {
		case out <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 1) Subject: the metadata.component (image or repo).
	subjectRef := ""
	if bom.Metadata != nil && bom.Metadata.Component != nil {
		mc := bom.Metadata.Component
		subjectRef = mc.BOMRef
		rt, rid := subjectResource(&bom, mc)
		payload := map[string]any{
			"tool":           tool,
			"bom_ref":        mc.BOMRef,
			"type":           string(mc.Type),
			"name":           mc.Name,
			"version":        mc.Version,
			"purl":           mc.PackageURL,
			"image_digest":   imageDigest(mc),
			"repo_url":       repoURL(&bom, mc),
			"properties":     propsToMap(mc.Properties),
		}
		if err := emit(rt, rid, payload); err != nil {
			return err
		}
	}

	// 2) Components (packages, models, nested AI components).
	if bom.Components != nil {
		for i := range *bom.Components {
			c := &(*bom.Components)[i]
			if err := emitComponent(emit, c); err != nil {
				return err
			}
		}
	}

	// 3) Services (AI services, MCP servers surface here in aibom).
	if bom.Services != nil {
		for i := range *bom.Services {
			svc := &(*bom.Services)[i]
			rid := svc.BOMRef
			if rid == "" {
				rid = "service:" + svc.Name
			}
			payload := map[string]any{
				"bom_ref":     svc.BOMRef,
				"name":        svc.Name,
				"group":       svc.Group,
				"version":     svc.Version,
				"description": svc.Description,
				"endpoints":   derefSlice(svc.Endpoints),
				"properties":  propsToMap(svc.Properties),
			}
			if err := emit(RTService, rid, payload); err != nil {
				return err
			}
		}
	}

	// 4) Dependencies (ref -> dependsOn edges).
	if bom.Dependencies != nil {
		for i := range *bom.Dependencies {
			d := &(*bom.Dependencies)[i]
			if d.Dependencies == nil {
				continue
			}
			payload := map[string]any{
				"ref":        d.Ref,
				"depends_on": derefSlice(d.Dependencies),
			}
			rid := "dep:" + d.Ref
			if err := emit(RTDependency, rid, payload); err != nil {
				return err
			}
		}
	}

	// 5) Vulnerabilities.
	if bom.Vulnerabilities != nil {
		for i := range *bom.Vulnerabilities {
			v := &(*bom.Vulnerabilities)[i]
			payload := map[string]any{
				"id":          v.ID,
				"source":      sourceName(v),
				"description": v.Description,
				"ratings":     ratings(v),
				"affects":     affects(v),
				"cwes":        derefSlice(v.CWEs),
			}
			if err := emit(RTVuln, "vuln:"+v.ID, payload); err != nil {
				return err
			}
		}
	}

	_ = subjectRef
	return nil
}

func emitComponent(emit func(rt, rid string, payload any) error, c *cdx.Component) error {
	rid := c.PackageURL
	if rid == "" {
		rid = c.BOMRef
	}
	if rid == "" {
		rid = "component:" + c.Group + ":" + c.Name + "@" + c.Version
	}
	payload := map[string]any{
		"bom_ref":     c.BOMRef,
		"type":        string(c.Type),
		"group":       c.Group,
		"name":        c.Name,
		"version":     c.Version,
		"purl":        c.PackageURL,
		"cpe":         c.CPE,
		"description": c.Description,
		"hashes":      hashes(c.Hashes),
		"properties":  propsToMap(c.Properties),
	}
	// AI model card metadata, when present (aibom / ML-BOM).
	if c.ModelCard != nil {
		mc, _ := json.Marshal(c.ModelCard)
		payload["model_card"] = json.RawMessage(mc)
	}
	if err := emit(RTComponent, rid, payload); err != nil {
		return err
	}
	// Recurse into nested components so sub-components are not lost.
	if c.Components != nil {
		for i := range *c.Components {
			if err := emitComponent(emit, &(*c.Components)[i]); err != nil {
				return err
			}
		}
	}
	return nil
}
