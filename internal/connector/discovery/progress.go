package discovery

// TableState is the lifecycle state of a single table within a discovery run.
type TableState string

const (
	// TablePending: the table is planned but collection has not started.
	TablePending TableState = "pending"
	// TableRunning: the table is currently being collected.
	TableRunning TableState = "running"
	// TableDone: the table finished successfully.
	TableDone TableState = "done"
	// TableError: the table finished with an error (collection continued).
	TableErrorState TableState = "error"
)

// TableProgress is the per-table progress for one Steampipe table.
type TableProgress struct {
	Table string     `json:"table"`
	State TableState `json:"state"`
	Rows  int        `json:"rows"`
	Error string     `json:"error,omitempty"`
}

// Progress is the run-level, in-memory snapshot of a discovery session's
// progress. It is updated by the orchestrator under its lock and read by the
// status poll and the SSE stream. Snapshots handed to readers are deep copies
// (see Clone) so a reader never aliases live state.
type Progress struct {
	TablesTotal        int             `json:"tables_total"`
	TablesDone         int             `json:"tables_done"`
	ResourcesCollected int             `json:"resources_collected"`
	CurrentTable       string          `json:"current_table,omitempty"`
	Tables             []TableProgress `json:"tables"`
}

// newProgress returns an empty, initialized Progress.
func newProgress() *Progress {
	return &Progress{Tables: []TableProgress{}}
}

// Clone returns a deep copy of the Progress so a reader can hold it safely
// after the orchestrator lock is released. A nil receiver clones to nil.
func (p *Progress) Clone() *Progress {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Tables = make([]TableProgress, len(p.Tables))
	copy(cp.Tables, p.Tables)
	return &cp
}

// --- reducer -------------------------------------------------------------
//
// The methods below are the single place that mutates a Progress. The
// orchestrator calls them while holding its lock; they are not themselves
// synchronized. They are deliberately total: applying an event for an unknown
// table appends it, so a reducer can never panic on out-of-order input.

// planned records the total number of tables that will be collected and seeds a
// pending entry per table name so the UI can render the full checklist up front.
func (p *Progress) planned(total int) {
	p.TablesTotal = total
}

// started marks a table as the current, running table, creating its entry if it
// does not already exist.
func (p *Progress) started(table string) {
	p.CurrentTable = table
	e := p.find(table)
	if e == nil {
		p.Tables = append(p.Tables, TableProgress{Table: table, State: TableRunning})
		return
	}
	e.State = TableRunning
}

// done marks a table successful with its row count, adds the rows to the
// cumulative resource total, increments tables done, and clears the current
// table.
func (p *Progress) done(table string, rows int) {
	e := p.find(table)
	if e == nil {
		p.Tables = append(p.Tables, TableProgress{Table: table})
		e = &p.Tables[len(p.Tables)-1]
	}
	e.State = TableDone
	e.Rows = rows
	p.ResourcesCollected += rows
	p.TablesDone++
	if p.CurrentTable == table {
		p.CurrentTable = ""
	}
}

// failed marks a table errored with its message, increments tables done, and
// clears the current table. Collection continues with the next table.
func (p *Progress) failed(table string, msg string) {
	e := p.find(table)
	if e == nil {
		p.Tables = append(p.Tables, TableProgress{Table: table})
		e = &p.Tables[len(p.Tables)-1]
	}
	e.State = TableErrorState
	e.Error = msg
	p.TablesDone++
	if p.CurrentTable == table {
		p.CurrentTable = ""
	}
}

// find returns a pointer to the table entry with the given name, or nil.
func (p *Progress) find(table string) *TableProgress {
	for i := range p.Tables {
		if p.Tables[i].Table == table {
			return &p.Tables[i]
		}
	}
	return nil
}
