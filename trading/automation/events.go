package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// EventOverlap is how far back a restart re-reads the venue history. The
// overlap costs nothing but a few duplicate rows: dedup is by the venue's own
// identifiers, so a replay never double counts and never depends on the cursor
// landing exactly on a window boundary.
const EventOverlap = 60 * time.Second

// historyPageLimit bounds how many pages one reconcile may walk. A venue that
// keeps returning full pages is a signal to stop and try again later, not to
// spin forever inside a supervision tick.
const historyPageLimit = 50

// EventCursor records the last fully read window for one account and venue.
// Category is the venue: a reader returns every record type it owns, so one
// cursor covers what one reader can resume.
type EventCursor struct {
	AccountKey string    `gorm:"primaryKey" json:"account_key"`
	Category   string    `gorm:"primaryKey" json:"category"`
	Complete   time.Time `json:"complete"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName keeps the ledger's state next to the other managed tables.
func (EventCursor) TableName() string { return "managed_event_cursors" }

// pendingEvents is the in-memory buffer between an event arriving and the next
// snapshot that can attribute it. Losing it is safe: the durable path is the
// overlapping REST replay, this is only how a fill reaches the ledger promptly.
func (r *Runtime) bufferEvents(accountKey string, events []execution.AccountEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil {
		r.pending = map[string][]execution.AccountEvent{}
	}
	seen := make(map[string]bool, len(r.pending[accountKey]))
	for _, e := range r.pending[accountKey] {
		seen[e.ID] = true
	}
	for _, e := range events {
		if e.ID == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		r.pending[accountKey] = append(r.pending[accountKey], e)
	}
}

func (r *Runtime) drainEvents(accountKey string) []execution.AccountEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := r.pending[accountKey]
	delete(r.pending, accountKey)
	sort.SliceStable(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	return events
}

// Ingest accepts events from a private stream, records them against a fresh
// snapshot, and wakes the affected runs. Protection is re-checked in the same
// call: waiting for the next reconcile window would leave a partial fill
// uncovered for up to ReconcileInterval.
func (r *Runtime) Ingest(ctx context.Context, user, accountKey string, events []execution.AccountEvent) error {
	if len(events) == 0 {
		return nil
	}
	if user == "" || accountKey == "" {
		return errors.New("automation: ingest requires a user and an account")
	}
	r.bufferEvents(accountKey, events)
	lock := r.lock(accountKey)
	lock.Lock()
	defer lock.Unlock()
	if err := r.reconcileAccount(ctx, user, accountKey); err != nil {
		r.freeze(accountKey, err.Error(), freezeRisk)
		return err
	}
	if err := r.superviseRuns(ctx, user, accountKey); err != nil {
		r.freeze(accountKey, err.Error(), freezeRisk)
		return err
	}
	return nil
}

// syncHistory replays the venue's money-movement history from the overlapping
// cursor. A failed page leaves the cursor untouched so the next attempt
// re-reads the same window instead of skipping it.
func (r *Runtime) syncHistory(ctx context.Context, user, accountKey, venue string, reader execution.HistoryReader) error {
	if reader == nil {
		return nil
	}
	now := r.clock()
	from, err := r.historyStart(ctx, accountKey, venue, now)
	if err != nil {
		return err
	}
	cursor := ""
	for page := 0; page < historyPageLimit; page++ {
		events, next, complete, err := reader.History(ctx, from, cursor)
		if err != nil {
			return fmt.Errorf("automation: could not read the account history: %w", err)
		}
		r.bufferEvents(accountKey, events)
		if !complete {
			if next == "" || next == cursor {
				return errors.New("automation: the history reader returned a page without a cursor")
			}
			cursor = next
			continue
		}
		end, err := time.Parse(time.RFC3339, next)
		if err != nil {
			return fmt.Errorf("automation: the history reader returned a window end that is not a timestamp: %w", err)
		}
		return r.saveCursor(ctx, accountKey, venue, end)
	}
	return errors.New("automation: the account history did not finish within one reconcile")
}

// historyStart resumes EventOverlap before the last complete window. Without a
// cursor the replay starts at the current UTC day's start, which is the
// baseline the risk ledger needs anyway.
func (r *Runtime) historyStart(ctx context.Context, accountKey, venue string, now time.Time) (time.Time, error) {
	var cursor EventCursor
	err := r.db.WithContext(ctx).Where("account_key = ? AND category = ?", accountKey, venue).First(&cursor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return now.UTC().Truncate(24 * time.Hour), nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return cursor.Complete.Add(-EventOverlap), nil
}

func (r *Runtime) saveCursor(ctx context.Context, accountKey, venue string, end time.Time) error {
	cursor := EventCursor{AccountKey: accountKey, Category: venue, Complete: end.UTC(), UpdatedAt: r.clock()}
	err := r.db.WithContext(ctx).Save(&cursor).Error
	if err != nil {
		return fmt.Errorf("automation: could not record the history cursor: %w", err)
	}
	return nil
}

// ledgerKinds is the fixed event vocabulary the risk ledger understands. An
// unknown money movement is recorded as a gap so the day stays incomplete
// instead of silently rounding the equity.
func ledgerKinds(kind string) string {
	switch kind {
	case risk.KindCashflow, risk.KindFunding, risk.KindFee, risk.KindFundingGap:
		return kind
	}
	return risk.KindFundingGap
}

// fillEvent reports whether an event is evidence that a run's exposure changed.
func fillEvent(e execution.AccountEvent) bool {
	if e.Order.Filled == "" {
		return false
	}
	filled, err := execution.Number(string(e.Order.Filled))
	if err != nil {
		return false
	}
	return filled.Sign() > 0
}
