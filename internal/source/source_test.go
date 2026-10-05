package source

import (
	"context"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

type stubSource struct{ name string }

func (s stubSource) Name() string { return s.name }
func (s stubSource) Collect(ctx context.Context, run Run) (<-chan rawevent.RawEvent, <-chan error) {
	ev := make(chan rawevent.RawEvent)
	errc := make(chan error)
	close(ev)
	close(errc)
	return ev, errc
}

func TestRegistryBuild(t *testing.T) {
	r := NewRegistry()
	r.Register("stub", func(cfg map[string]any) (Source, error) {
		return stubSource{name: "stub"}, nil
	})
	s, err := r.Build("stub", nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if s.Name() != "stub" {
		t.Fatalf("expected stub, got %q", s.Name())
	}
}

func TestRegistryUnknown(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Build("nope", nil); err == nil {
		t.Fatal("expected error for unknown source")
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	r := NewRegistry()
	f := func(cfg map[string]any) (Source, error) { return stubSource{}, nil }
	r.Register("dup", f)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register("dup", f)
}

func TestRegistryNamesSorted(t *testing.T) {
	r := NewRegistry()
	f := func(cfg map[string]any) (Source, error) { return stubSource{}, nil }
	r.Register("zeek", f)
	r.Register("aws", f)
	r.Register("cyclonedx", f)
	got := r.Names()
	want := []string{"aws", "cyclonedx", "zeek"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names not sorted: got %v want %v", got, want)
		}
	}
}
