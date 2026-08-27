package backtest

import "sync"

// RunLock prevents concurrent execution of the same run ID (P3.1:
// 并发防重入). A run may be resumed after a pause, so Release must be
// called explicitly by the owner, not automatically on goroutine exit.
type RunLock struct {
	mu   sync.Mutex
	held map[string]bool
}

// NewRunLock creates an empty lock registry.
func NewRunLock() *RunLock {
	return &RunLock{held: make(map[string]bool)}
}

// TryAcquire acquires the lock for runID; false if already held.
func (l *RunLock) TryAcquire(runID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[runID] {
		return false
	}
	l.held[runID] = true
	return true
}

// Release releases the lock for runID (no-op if not held).
func (l *RunLock) Release(runID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, runID)
}

// Held reports whether runID is currently locked.
func (l *RunLock) Held(runID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held[runID]
}
