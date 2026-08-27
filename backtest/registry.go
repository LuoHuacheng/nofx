package backtest

import (
	"sync"
	"time"
)

// RunRecord is the in-memory lifecycle state of one run. Persistent
// metadata lives in store.BacktestRun (P3.2); the registry covers the
// volatile state the manager needs (status, start/end time, error).
type RunRecord struct {
	ID        string
	UserID    string
	Status    string
	Config    RunConfig
	StartedAt time.Time
	EndedAt   time.Time
	Err       string
}

// RunRegistry is an in-memory registry of run lifecycle state. It is
// NOT the source of truth for persisted runs (that is the store); it
// mirrors active runs for the manager and offers create/resume/cleanup.
type RunRegistry struct {
	mu   sync.RWMutex
	runs map[string]*RunRecord
}

// NewRunRegistry creates an empty registry.
func NewRunRegistry() *RunRegistry {
	return &RunRegistry{runs: make(map[string]*RunRecord)}
}

// Add inserts a record (replaces any existing one with the same ID).
func (r *RunRegistry) Add(rec *RunRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *rec
	r.runs[rec.ID] = &cp
}

// Get returns a copy of the record, or nil.
func (r *RunRegistry) Get(runID string) *RunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.runs[runID]
	if !ok {
		return nil
	}
	cp := *rec
	return &cp
}

// Update mutates a field of an existing record via the callback.
// Returns false if the run does not exist.
func (r *RunRegistry) Update(runID string, fn func(rec *RunRecord)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return false
	}
	fn(rec)
	return true
}

// Remove deletes the record.
func (r *RunRegistry) Remove(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, runID)
}

// All returns copies of all records.
func (r *RunRegistry) All() []RunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RunRecord, 0, len(r.runs))
	for _, rec := range r.runs {
		out = append(out, *rec)
	}
	return out
}
