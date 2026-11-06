package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

// historyFixture serves recorded pages. It never talks to a venue: every page
// is a literal transcription of the docs' field names and ordering.
type historyFixture struct {
	pages  [][]execution.AccountEvent
	window time.Time
	failOn int // 1-based page number that fails
	calls  int
}

func (h *historyFixture) History(_ context.Context, _ time.Time, _ string) ([]execution.AccountEvent, string, bool, error) {
	h.calls++
	if h.failOn != 0 && h.calls == h.failOn {
		return nil, "", false, errors.New("recorded transport failure")
	}
	if h.calls > len(h.pages) {
		return nil, h.window.Format(time.RFC3339), true, nil
	}
	page := h.pages[h.calls-1]
	complete := h.calls == len(h.pages)
	return page, h.window.Format(time.RFC3339), complete, nil
}

func fundingEvent(id, amount string, at time.Time) execution.AccountEvent {
	return execution.AccountEvent{ID: id, Kind: risk.KindFunding, Amount: execution.Decimal(amount), Currency: "USDT", OccurredAt: at}
}

func ledgerRows(t *testing.T, r *automationRig) []risk.LedgerEvent {
	t.Helper()
	var rows []risk.LedgerEvent
	if err := r.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestManagedEventsDuplicateAndOutOfOrder proves dedup is the venue's own
// identifier: replaying the same event, or delivering a smaller cumulative
// quantity after a larger one, never changes the ledger.
func TestManagedEventsDuplicateAndOutOfOrder(t *testing.T) {
	r := rig(t, nil)
	rt := r.runtime(t, nil)
	ctx := context.Background()

	first := fundingEvent("binance:income:1", "-1.5", r.now)
	smaller := fundingEvent("binance:income:1", "-0.5", r.now.Add(time.Second))
	if err := rt.Ingest(ctx, "alice", "account", []execution.AccountEvent{first}); err != nil {
		t.Fatal(err)
	}
	rows := ledgerRows(t, r)
	if len(rows) != 1 {
		t.Fatalf("one funding event produced %d ledger rows", len(rows))
	}
	originalFundingEvents := len(rows)

	if err := rt.Ingest(ctx, "alice", "account", []execution.AccountEvent{first, smaller}); err != nil {
		t.Fatal(err)
	}
	if got := len(ledgerRows(t, r)); got != originalFundingEvents {
		t.Fatalf("recoveredFundingEvents %d != originalFundingEvents %d", got, originalFundingEvents)
	}
	var stored risk.LedgerEvent
	if err := r.db.Where("event_id = ?", first.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if string(stored.Amount) != "-1.5" {
		t.Fatalf("a duplicate replayed over the original amount: %s", stored.Amount)
	}
}

// TestManagedHistoryMissingPage proves a failed page never advances the cursor,
// so the next attempt re-reads the same window instead of skipping it.
func TestManagedHistoryMissingPage(t *testing.T) {
	r := rig(t, nil)
	rt := r.runtime(t, nil)
	ctx := context.Background()
	window := r.now.Add(2 * time.Minute)

	reader := &historyFixture{
		pages: [][]execution.AccountEvent{
			{fundingEvent("binance:income:1", "-1", r.now)},
			{fundingEvent("binance:income:2", "-2", r.now.Add(time.Second))},
		},
		window: window,
		failOn: 2,
	}
	r.venue.history = reader
	if err := rt.syncHistory(ctx, "alice", "account", "binance", reader); err == nil {
		t.Fatal("an incomplete history was reported as complete")
	}
	var cursor EventCursor
	if err := r.db.Where("account_key = ?", "account").First(&cursor).Error; err == nil {
		t.Fatal("incomplete history advanced cursor")
	}

	reader.failOn = 0
	reader.calls = 0
	if err := rt.syncHistory(ctx, "alice", "account", "binance", reader); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Where("account_key = ?", "account").First(&cursor).Error; err != nil {
		t.Fatal(err)
	}
	if !cursor.Complete.Equal(window) {
		t.Fatalf("the window ended at %s, wanted %s", cursor.Complete, window)
	}
	again, err := rt.historyStart(ctx, "account", "binance", r.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Equal(window.Add(-EventOverlap)) {
		t.Fatalf("the restart resumed at %s instead of the overlapping window", again)
	}
}

// TestManagedEventsReconnect proves a broken history read freezes new entries
// and keeps protection supervision running. The stream itself is venue code
// and is not started here: the reader stands in for the venue's record.
func TestManagedEventsReconnect(t *testing.T) {
	r := rig(t, nil)
	rt := r.runtime(t, nil)
	ctx := context.Background()
	r.venue.history = &historyFixture{pages: [][]execution.AccountEvent{{}}, window: r.now, failOn: 1}

	if err := rt.Tick(ctx, "alice", "account"); err == nil {
		t.Fatal("a failed history read did not stop the tick")
	}
	if _, frozen := rt.frozenReason("account"); !frozen {
		t.Fatal("a failed history read left the account enabled for new entries")
	}
	r.source.signals = append(r.source.signals, Signal{ID: "signal-1", Idea: r.idea()})
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if got := r.venue.placeCount(); got != 0 {
		t.Fatalf("a frozen account submitted %d orders", got)
	}
}

// TestManagedEventsPartialFillWakeup proves an event wakes protection in the
// same call instead of waiting for the reconcile tick.
func TestManagedEventsPartialFillWakeup(t *testing.T) {
	r := rig(t, &automationSource{})
	rt := r.runtime(t, r.source)
	ctx := context.Background()
	r.source.signals = append(r.source.signals, Signal{ID: "signal-1", Idea: r.idea()})
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatal(err)
	}
	r.waitPlaced(t, 1)

	before := r.venue.snapshotCalls()
	// No clock advance: only the event itself can trigger the extra read.
	fill := execution.AccountEvent{
		ID:         "binance:trade:42",
		Kind:       risk.KindCashflow,
		Symbol:     "BTCUSDT",
		Order:      execution.Order{Symbol: "BTCUSDT", Filled: "0.05", Status: "partially_filled"},
		Amount:     execution.Decimal("0"),
		OccurredAt: r.now,
	}
	if err := rt.Ingest(ctx, "alice", "account", []execution.AccountEvent{fill}); err != nil {
		t.Fatal(err)
	}
	if got := r.venue.snapshotCalls(); got <= before {
		t.Fatalf("the event did not re-read the account: %d calls", got)
	}
}
