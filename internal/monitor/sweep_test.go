package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/intelcorrelate"
)

// fakeCorrelator returns a fixed candidate set (or an error).
type fakeCorrelator struct {
	cands []intelcorrelate.Candidate
	err   error
}

func (f fakeCorrelator) Correlate(context.Context) ([]intelcorrelate.Candidate, error) {
	return f.cands, f.err
}

// fakeUpserter records each (org, finding) upsert and can report Created and an
// error per call so a test can model the delta and a bad row.
type fakeUpserter struct {
	calls   []finding.Finding
	orgs    []int64
	created bool
	err     error
}

func (f *fakeUpserter) UpsertFinding(_ context.Context, org int64, fnd finding.Finding) (finding.UpsertResult, error) {
	f.orgs = append(f.orgs, org)
	f.calls = append(f.calls, fnd)
	return finding.UpsertResult{Created: f.created}, f.err
}

func TestRunSweepUpsertsEveryMatchForOrg(t *testing.T) {
	cands := []intelcorrelate.Candidate{
		{AssetID: "a1", VulnID: "CVE-1", IntelRef: "CVE-1"},
		{AssetID: "a2", VulnID: "CVE-2"}, // empty IntelRef => defaults to VulnID
	}
	up := &fakeUpserter{created: true}
	factory := CorrelatorFactoryFunc(func(int64) (Correlator, error) {
		return fakeCorrelator{cands: cands}, nil
	})
	s := NewSweeper(factory, up)

	if err := s.RunSweep(context.Background(), 7); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}
	if len(up.calls) != 2 {
		t.Fatalf("want 2 upserts, got %d", len(up.calls))
	}
	for _, org := range up.orgs {
		if org != 7 {
			t.Fatalf("every upsert must carry the swept org 7, got %d", org)
		}
	}
	// A candidate with no IntelRef falls back to its VulnID.
	if up.calls[1].IntelRef != "CVE-2" {
		t.Fatalf("empty IntelRef should default to VulnID, got %q", up.calls[1].IntelRef)
	}
	if up.calls[0].Status != finding.StatusOpen {
		t.Fatalf("swept findings should be open, got %q", up.calls[0].Status)
	}
}

func TestRunSweepContinuesPastUpsertError(t *testing.T) {
	// The sweep must not abort when one row fails to upsert; it only re-correlates
	// and upserts, logging per-row failures. A failing upserter still gets every
	// candidate and RunSweep returns nil.
	up := &fakeUpserter{err: errors.New("boom")}
	factory := CorrelatorFactoryFunc(func(int64) (Correlator, error) {
		return fakeCorrelator{cands: []intelcorrelate.Candidate{
			{AssetID: "a1", VulnID: "CVE-1"},
			{AssetID: "a2", VulnID: "CVE-2"},
		}}, nil
	})
	s := NewSweeper(factory, up)
	s.logf = func(string, ...any) {} // silence expected per-row logs

	if err := s.RunSweep(context.Background(), 1); err != nil {
		t.Fatalf("a per-row upsert error must not fail the sweep: %v", err)
	}
	if len(up.calls) != 2 {
		t.Fatalf("sweep should attempt every candidate despite errors, got %d", len(up.calls))
	}
}

func TestRunSweepPropagatesCorrelateError(t *testing.T) {
	factory := CorrelatorFactoryFunc(func(int64) (Correlator, error) {
		return fakeCorrelator{err: errors.New("graph down")}, nil
	})
	s := NewSweeper(factory, &fakeUpserter{})
	if err := s.RunSweep(context.Background(), 1); err == nil {
		t.Fatal("RunSweep should surface a correlate failure")
	}
}

func TestRunSweepAllOrgsIsolatesPerOrgFailure(t *testing.T) {
	// Org 2's correlator fails to build; orgs 1 and 3 must still be swept.
	up := &fakeUpserter{}
	factory := CorrelatorFactoryFunc(func(org int64) (Correlator, error) {
		if org == 2 {
			return nil, errors.New("no correlator for org 2")
		}
		return fakeCorrelator{cands: []intelcorrelate.Candidate{{AssetID: "a", VulnID: "v"}}}, nil
	})
	s := NewSweeper(factory, up)
	s.logf = func(string, ...any) {}

	swept := s.RunSweepAllOrgs(context.Background(), []int64{1, 2, 3})
	if swept != 2 {
		t.Fatalf("want 2 orgs swept (1 and 3), got %d", swept)
	}
	for _, org := range up.orgs {
		if org == 2 {
			t.Fatal("org 2 must not be upserted after its correlator failed to build")
		}
	}
}

func TestNewSweepSchedulerClampsAndValidates(t *testing.T) {
	sw := NewSweeper(CorrelatorFactoryFunc(func(int64) (Correlator, error) { return fakeCorrelator{}, nil }), &fakeUpserter{})

	if _, err := NewSweepScheduler(SweepSchedulerOptions{Orgs: func(context.Context) ([]int64, error) { return nil, nil }}); err == nil {
		t.Fatal("scheduler without a Sweeper should error")
	}
	sched, err := NewSweepScheduler(SweepSchedulerOptions{
		Sweeper:  sw,
		Interval: time.Minute, // below MinSweepInterval
		Orgs:     func(context.Context) ([]int64, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("NewSweepScheduler: %v", err)
	}
	if sched.Interval() != MinSweepInterval {
		t.Fatalf("interval below the minimum should clamp up to %s, got %s", MinSweepInterval, sched.Interval())
	}
}
