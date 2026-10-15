package discovery

import "sync"

// progressHub owns a session's live Progress and fans out change
// notifications to any subscribers (e.g. SSE streams). All mutation of the
// Progress happens here under hubMu, so the status poll and the stream never
// observe a torn snapshot.
//
// One hub exists per running session. It implements source.ProgressReporter so
// it can be handed directly to the collector via source.Run.Reporter.
type progressHub struct {
	mu       sync.Mutex
	prog     *Progress
	subs     map[int]chan *Progress
	nextSub  int
	finished bool
}

func newProgressHub() *progressHub {
	return &progressHub{prog: newProgress(), subs: map[int]chan *Progress{}}
}

// snapshot returns a deep copy of the current Progress, safe to read without
// the hub lock.
func (h *progressHub) snapshot() *Progress {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.prog.Clone()
}

// subscribe registers a subscriber and returns a receive channel plus a cancel
// func that unregisters it. The channel is buffered depth 1 and coalescing:
// publish never blocks on a slow reader; it drops the stale pending snapshot
// and keeps the latest. The current snapshot is delivered immediately so a late
// subscriber is not blank.
func (h *progressHub) subscribe() (<-chan *Progress, func()) {
	h.mu.Lock()
	id := h.nextSub
	h.nextSub++
	ch := make(chan *Progress, 1)
	ch <- h.prog.Clone()
	h.subs[id] = ch
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// publish sends the current snapshot to all subscribers without blocking. If a
// subscriber's buffer is full, the pending snapshot is replaced with the newer
// one (coalescing) so readers always converge on the latest state.
func (h *progressHub) publishLocked() {
	for _, ch := range h.subs {
		snap := h.prog.Clone()
		select {
		case ch <- snap:
		default:
			// Drain the stale pending snapshot and replace with the latest.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
}

// close marks the hub finished and closes all subscriber channels so streams
// terminate. Called once the session reaches a terminal state.
func (h *progressHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.finished {
		return
	}
	h.finished = true
	for id, ch := range h.subs {
		delete(h.subs, id)
		close(ch)
	}
}

// --- source.ProgressReporter implementation ---------------------------------
//
// The collector calls these from a single goroutine in table order. Each
// mutates the Progress under the lock and publishes a snapshot to subscribers.

func (h *progressHub) TablesPlanned(total int) {
	h.mu.Lock()
	h.prog.planned(total)
	h.publishLocked()
	h.mu.Unlock()
}

func (h *progressHub) TableStarted(table string, _ int) {
	h.mu.Lock()
	h.prog.started(table)
	h.publishLocked()
	h.mu.Unlock()
}

func (h *progressHub) TableDone(table string, rows int) {
	h.mu.Lock()
	h.prog.done(table, rows)
	h.publishLocked()
	h.mu.Unlock()
}

func (h *progressHub) TableError(table string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	h.mu.Lock()
	h.prog.failed(table, msg)
	h.publishLocked()
	h.mu.Unlock()
}
