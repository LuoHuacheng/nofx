package backtest

import (
	"sort"
	"time"
)

// RetainedRun is the minimal view of a run the retention task needs:
// a completed run plus when it finished.
type RetainedRun struct {
	ID      string
	Status  string // only completed/failed/cancelled runs are retained
	EndedAt time.Time
}

// CleanupExpired returns the IDs of retained runs whose EndedAt is
// older than the retention window (default RetentionDays). Runs still
// pending/running/paused are never returned.
func CleanupExpired(runs []RetainedRun, retention time.Duration, now time.Time) []string {
	cutoff := now.Add(-retention)
	var expired []string
	for _, r := range runs {
		switch r.Status {
		case StatusCompleted, StatusFailed, StatusCancelled:
		default:
			continue // active runs are never cleaned
		}
		if r.EndedAt.IsZero() || r.EndedAt.Before(cutoff) {
			expired = append(expired, r.ID)
		}
	}
	sort.Strings(expired)
	return expired
}
