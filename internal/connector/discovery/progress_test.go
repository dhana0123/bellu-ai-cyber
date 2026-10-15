package discovery

import (
	"errors"
	"testing"
)

// TestProgressReducer exercises the reducer end to end: planned count, a table
// started -> done with rows, a cumulative resource total, an errored table that
// still counts as done, and the current-table bookkeeping.
func TestProgressReducer(t *testing.T) {
	p := newProgress()
	p.planned(3)
	if p.TablesTotal != 3 {
		t.Fatalf("TablesTotal = %d, want 3", p.TablesTotal)
	}

	p.started("aws_account")
	if p.CurrentTable != "aws_account" {
		t.Errorf("CurrentTable = %q, want aws_account", p.CurrentTable)
	}
	if e := p.find("aws_account"); e == nil || e.State != TableRunning {
		t.Fatalf("aws_account not running: %+v", e)
	}

	p.done("aws_account", 1)
	if p.TablesDone != 1 || p.ResourcesCollected != 1 {
		t.Errorf("after done: TablesDone=%d Resources=%d, want 1/1", p.TablesDone, p.ResourcesCollected)
	}
	if p.CurrentTable != "" {
		t.Errorf("CurrentTable should clear after done, got %q", p.CurrentTable)
	}

	p.started("aws_iam_role")
	p.done("aws_iam_role", 214)
	if p.ResourcesCollected != 215 {
		t.Errorf("cumulative resources = %d, want 215", p.ResourcesCollected)
	}

	p.started("aws_bedrock_custom_model")
	p.failed("aws_bedrock_custom_model", "region 404")
	if p.TablesDone != 3 {
		t.Errorf("errored table should count as done: TablesDone=%d, want 3", p.TablesDone)
	}
	if e := p.find("aws_bedrock_custom_model"); e == nil || e.State != TableErrorState || e.Error != "region 404" {
		t.Fatalf("errored table not recorded: %+v", e)
	}
	// A failed table contributes no resources.
	if p.ResourcesCollected != 215 {
		t.Errorf("resources changed on failure: %d, want 215", p.ResourcesCollected)
	}
}

// TestProgressClone proves snapshots are deep copies: mutating the original's
// table slice after Clone must not affect the clone.
func TestProgressClone(t *testing.T) {
	p := newProgress()
	p.planned(1)
	p.started("t1")
	p.done("t1", 5)

	c := p.Clone()
	p.done("t2", 7) // mutate original after cloning

	if len(c.Tables) != 1 {
		t.Fatalf("clone tables = %d, want 1 (deep copy)", len(c.Tables))
	}
	if c.ResourcesCollected != 5 {
		t.Errorf("clone resources = %d, want 5 (independent of later mutation)", c.ResourcesCollected)
	}
	if (*Progress)(nil).Clone() != nil {
		t.Error("nil.Clone() should be nil")
	}
}

// TestProgressHubReporter verifies the hub implements the reporter contract and
// publishes snapshots a subscriber can observe, then closes on finish.
func TestProgressHubReporter(t *testing.T) {
	h := newProgressHub()
	ch, cancel := h.subscribe()
	defer cancel()
	// First delivery is the current (empty) snapshot.
	<-ch

	h.TablesPlanned(2)
	h.TableStarted("a", 0)
	h.TableDone("a", 10)
	h.TableError("b", errors.New("boom"))

	// Drain to the latest coalesced snapshot and assert terminal-ish counts.
	var last *Progress
	drained := false
	for !drained {
		select {
		case last = <-ch:
		default:
			drained = true
		}
	}
	if last == nil {
		t.Fatal("no snapshot delivered")
	}
	if last.TablesTotal != 2 {
		t.Errorf("TablesTotal = %d, want 2", last.TablesTotal)
	}

	// Close terminates the subscriber channel.
	h.close()
	// Reading a closed channel eventually yields !ok.
	for {
		_, ok := <-ch
		if !ok {
			return
		}
	}
}
