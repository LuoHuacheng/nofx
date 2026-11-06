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

// TestManagedEventsBurstRespectsCadence proves a burst of stream events cannot
// outrun the REST budget: events inside the reconcile slot are buffered, and the
// next snapshot attributes every one of them.
func TestManagedEventsBurstRespectsCadence(t *testing.T) {
	r := rig(t, nil)
	rt := r.runtime(t, nil)
	ctx := context.Background()
	r.venue.history = &historyFixture{pages: [][]execution.AccountEvent{{}}, window: r.now}

	// First ingest takes the slot.
	first := fundingEvent("binance:income:1", "-1", r.now)
	if err := rt.Ingest(ctx, "alice", "account", []execution.AccountEvent{first}); err != nil {
		t.Fatal(err)
	}
	before := r.venue.snapshotCalls()
	burst := []execution.AccountEvent{
		fundingEvent("binance:income:2", "-2", r.now.Add(time.Millisecond)),
		fundingEvent("binance:income:3", "-3", r.now.Add(2*time.Millisecond)),
	}
	if err := rt.Ingest(ctx, "alice", "account", burst); err != nil {
		t.Fatal(err)
	}
	if got := r.venue.snapshotCalls(); got != before {
		t.Fatalf("a burst inside the reconcile slot re-read the venue: %d calls", got-before)
	}
	// The buffered events must survive to the next snapshot, not be dropped.
	r.advance(ReconcileInterval + time.Second)
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, row := range ledgerRows(t, r) {
		ids[row.EventID] = true
	}
	for _, e := range burst {
		if !ids[e.ID] {
			t.Fatalf("a buffered event was lost: %s", e.ID)
		}
	}
}

// TestManagedReplayMatchesTheFullFixture proves the disconnect compensation and
// a full replay end in the same ledger, the same cursor and the same quantity:
// the 60s overlap plus native-id dedup is what makes the two paths equal.
func TestManagedReplayMatchesTheFullFixture(t *testing.T) {
	pages := [][]execution.AccountEvent{
		{fundingEvent("binance:income:1", "-1", time.Unix(1700000000, 0).UTC())},
		{fundingEvent("binance:income:2", "-2", time.Unix(1700000100, 0).UTC())},
	}
	window := time.Unix(1700000200, 0).UTC()

	replay := func(t *testing.T, failFirst int) ([]risk.LedgerEvent, time.Time) {
		t.Helper()
		r := rig(t, nil)
		rt := r.runtime(t, nil)
		reader := &historyFixture{pages: pages, window: window, failOn: failFirst}
		r.venue.history = reader
		ctx := context.Background()
		// A failing read leaves the cursor where it was; the retry re-reads the
		// same window and reaches the same end.
		_ = rt.syncHistory(ctx, "alice", "account", "binance", reader)
		reader.calls = 0
		reader.failOn = 0
		if err := rt.syncHistory(ctx, "alice", "account", "binance", reader); err != nil {
			t.Fatal(err)
		}
		var cursor EventCursor
		if err := r.db.Where("account_key = ?", "account").First(&cursor).Error; err != nil {
			t.Fatal(err)
		}
		rows := ledgerRows(t, r)
		return rows, cursor.Complete
	}

	fullRows, fullCursor := replay(t, 0)
	compRows, compCursor := replay(t, 2)
	if !fullCursor.Equal(compCursor) {
		t.Fatalf("the compensated cursor ended at %s, the full replay at %s", compCursor, fullCursor)
	}
	if len(fullRows) != len(compRows) {
		t.Fatalf("recovered %d ledger rows, the full replay %d", len(compRows), len(fullRows))
	}
	for i := range fullRows {
		if fullRows[i].EventID != compRows[i].EventID || fullRows[i].Amount != compRows[i].Amount {
			t.Fatalf("row %d differs: %+v vs %+v", i, compRows[i], fullRows[i])
		}
	}
}

// TestManagedEventsPartialFillWakeup proves an event wakes protection in the
// same call instead of waiting for the reconcile tick.
// TestManagedEventsStreamReachesProtection drives the real stream path: the
// fixture venue pushes a fill through Events, the runtime ingests it, and the
// affected account is re-read without waiting for the REST cadence.
func TestManagedEventsStreamReachesProtection(t *testing.T) {
	r := rig(t, &automationSource{})
	r.venue.stream = make(chan execution.AccountEvent)
	r.source.signals = append(r.source.signals, Signal{ID: "signal-1", Idea: r.idea(), CreatedAt: r.now, ExpiresAt: r.now.Add(SignalExpiry)})
	rt := r.runtime(t, r.source)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelStop()
		if err := rt.Shutdown(stop); err != nil {
			t.Fatal(err)
		}
	}()
	// The supervisor's own tick must place the entry: that proves the stream
	// goroutine started and supervision still works beside it.
	r.waitPlaced(t, 1)

	// Past the reconcile slot, so the pushed event — not the cadence — re-reads.
	r.advance(ReconcileInterval + time.Second)
	before := r.venue.snapshotCalls()
	select {
	case r.venue.stream <- execution.AccountEvent{ID: "binance:trade:42", Kind: execution.KindFill, Symbol: "BTCUSDT", Order: execution.Order{Symbol: "BTCUSDT", Filled: "0.05", Status: "partially_filled"}, OccurredAt: r.now}:
	case <-time.After(5 * time.Second):
		t.Fatal("the fixture stream is not being read")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && r.venue.snapshotCalls() <= before {
		time.Sleep(5 * time.Millisecond)
	}
	if got := r.venue.snapshotCalls(); got <= before {
		t.Fatalf("a streamed fill did not wake the runtime: %d calls", got)
	}
}

func TestManagedEventsPartialFillWakeup(t *testing.T) {
	r := rig(t, &automationSource{})
	rt := r.runtime(t, r.source)
	ctx := context.Background()
	r.source.signals = append(r.source.signals, Signal{ID: "signal-1", Idea: r.idea()})
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatal(err)
	}
	r.waitPlaced(t, 1)

	// Past the reconcile slot, so the event — not the cadence — is what re-reads.
	r.advance(ReconcileInterval + time.Second)
	before := r.venue.snapshotCalls()
	// No further clock advance: only the event can trigger the extra read.
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
