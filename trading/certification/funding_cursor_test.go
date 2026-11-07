package certification

// Funding is replayed from the venue in overlapping windows after a restart, so
// the ledger has to stay exact: an event the process already recorded must not
// be counted twice because a crash discarded the page it was paging through.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"nofx/trader/execution"
	"nofx/trading/automation"
	"nofx/trading/risk"
)

// certFundingTrader supplies only the two managed seams the reconciler uses.
type certFundingTrader struct {
	execution.ManagedTrader
	reader  execution.HistoryReader
	account execution.ManagedAccount
}

func (t *certFundingTrader) ManagedHistory() execution.HistoryReader { return t.reader }

func (t *certFundingTrader) ManagedSnapshot(context.Context) (execution.ManagedAccount, error) {
	return t.account, nil
}

// Events is the private stream seam; the funding fixture only scripts history.
func (t *certFundingTrader) Events(context.Context, func(execution.AccountEvent) error) error {
	return errors.New("certification: the funding fixture has no private stream")
}

// certFundingReader scripts the venue's history pages. crashAt kills the
// process on that 1-based call, which is how the test stops before a page
// cursor is committed.
type certFundingReader struct {
	pages    [][]execution.AccountEvent
	next     []string
	complete []bool
	crashAt  int
	calls    int
}

func (r *certFundingReader) History(_ context.Context, _ time.Time, _ string) ([]execution.AccountEvent, string, bool, error) {
	r.calls++
	if r.crashAt == r.calls {
		crashProcess()
	}
	index := r.calls - 1
	if index >= len(r.pages) {
		return nil, "", false, errors.New("certification: funding reader exhausted its script")
	}
	return r.pages[index], r.next[index], r.complete[index], nil
}

func certFundingEvents(clock time.Time, accountKey string) (a, b, c, d execution.AccountEvent) {
	day := clock.Truncate(24 * time.Hour)
	event := func(id string, amount string, at time.Time) execution.AccountEvent {
		return execution.AccountEvent{
			ID:         id,
			AccountKey: accountKey,
			Kind:       risk.KindFunding,
			Amount:     execution.Decimal(amount),
			Currency:   "USDT",
			OccurredAt: at,
		}
	}
	return event("funding-a", "1.25", day.Add(-3*time.Hour)),
		event("funding-b", "-0.5", day.Add(-2*time.Hour)),
		event("funding-c", "0.75", day.Add(1*time.Hour)),
		event("funding-d", "-0.25", day.Add(2*time.Hour))
}

func certFundingRuntime(t *testing.T, f *exchangeFixture, reader execution.HistoryReader) *automation.Runtime {
	t.Helper()
	ctx := context.Background()
	trader := &certFundingTrader{reader: reader, account: f.account}
	ctrl := certificationController(t, f.db, f.risks, trader, f.binding.ID, f.venue, f.now)
	runtime, err := automation.NewRuntime(f.db, ctrl, f.risks,
		func(context.Context, string, string) (execution.ManagedTrader, execution.Binding, error) {
			return trader, f.binding, nil
		}, nil, f.now)
	if err != nil {
		t.Fatalf("certification: funding runtime: %v", err)
	}
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("certification: funding runtime start: %v", err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runtime.Shutdown(shutdown)
	})
	return runtime
}

func certTick(t *testing.T, runtime *automation.Runtime, f *exchangeFixture) {
	t.Helper()
	if err := runtime.Tick(context.Background(), "alice", f.binding.ID); err != nil {
		t.Fatalf("certification: funding tick: %v", err)
	}
}

// certLedgerEventIDs returns the persisted ledger keys.
func certLedgerEventIDs(t *testing.T, f *exchangeFixture) map[string]string {
	t.Helper()
	var rows []risk.LedgerEvent
	if err := f.db.Where("account_key = ?", f.binding.ID).Find(&rows).Error; err != nil {
		t.Fatalf("certification: load ledger events: %v", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		if _, duplicate := out[row.EventID]; duplicate {
			t.Fatalf("certification: ledger event %s was counted twice", row.EventID)
		}
		out[row.EventID] = string(row.Amount)
	}
	return out
}

func certFundingCursor(t *testing.T, f *exchangeFixture) automation.EventCursor {
	t.Helper()
	var cursor automation.EventCursor
	if err := f.db.Where("account_key = ? AND category = ?", f.binding.ID, f.venue).First(&cursor).Error; err != nil {
		t.Fatalf("certification: load funding cursor: %v", err)
	}
	return cursor
}

// TestManagedFundingCursorRestart kills the process mid-window, then replays the
// overlapping window and requires the ledger to stay at one row per event ID.
func TestManagedFundingCursorRestart(t *testing.T) {
	f := newExchangeFixture(t, venueBinance)
	clock := f.clock
	a, b, c, d := certFundingEvents(clock, f.binding.ID)
	windowOneEnd := clock.Add(10 * time.Minute)
	windowTwoEnd := clock.Add(30 * time.Minute)

	// A full window is committed before the crash, so the overlap is real.
	first := certFundingRuntime(t, f, &certFundingReader{
		pages:    [][]execution.AccountEvent{{a, b}},
		next:     []string{windowOneEnd.Format(time.RFC3339Nano)},
		complete: []bool{true},
	})
	certTick(t, first, f)
	if rows := certLedgerEventIDs(t, f); len(rows) != 2 {
		t.Fatalf("certification: first window recorded %d events, want 2", len(rows))
	}
	if cursor := certFundingCursor(t, f); !cursor.Complete.Equal(windowOneEnd.UTC()) {
		t.Fatalf("certification: first window cursor is %s, want %s", cursor.Complete, windowOneEnd.UTC())
	}

	// The next process dies while paging through the following window: the page
	// it already read is discarded with the process, and no cursor is committed.
	f.clock = clock.Add(time.Minute)
	runUntilKilled(t, crashHelperFundingCurse, helperEnv(crashHelperFundingCurse,
		certHelperBase+"="+f.fake.url(),
		certHelperDB+"="+f.dbPath,
		certHelperVenue+"="+f.venue,
		certHelperAccount+"="+f.binding.ID,
		certHelperNow+"="+f.clock.Format(time.RFC3339Nano),
		certHelperLockDir+"="+t.TempDir(),
	))
	if rows := certLedgerEventIDs(t, f); len(rows) != 2 {
		t.Fatalf("certification: the crash committed ledger rows: %d, want 2", len(rows))
	}
	if cursor := certFundingCursor(t, f); !cursor.Complete.Equal(windowOneEnd.UTC()) {
		t.Fatalf("certification: the crash advanced the cursor to %s, want %s", cursor.Complete, windowOneEnd.UTC())
	}

	// Restart re-pulls from the overlap: the already recorded event B arrives
	// again alongside the two the dead process never committed.
	f.clock = f.clock.Add(2 * automation.ReconcileInterval)
	second := certFundingRuntime(t, f, &certFundingReader{
		pages:    [][]execution.AccountEvent{{b, c, d}},
		next:     []string{windowTwoEnd.Format(time.RFC3339Nano)},
		complete: []bool{true},
	})
	certTick(t, second, f)
	rows := certLedgerEventIDs(t, f)
	if len(rows) != 4 {
		t.Fatalf("certification: overlap replay left %d ledger events, want 4 (one per event ID): %v", len(rows), rows)
	}
	for id, amount := range map[string]string{"funding-a": "1.25", "funding-b": "-0.5", "funding-c": "0.75", "funding-d": "-0.25"} {
		if rows[id] != amount {
			t.Fatalf("certification: ledger event %s is %q, want %q", id, rows[id], amount)
		}
	}
	if cursor := certFundingCursor(t, f); !cursor.Complete.Equal(windowTwoEnd.UTC()) {
		t.Fatalf("certification: replay cursor is %s, want %s", cursor.Complete, windowTwoEnd.UTC())
	}

	// Another restart re-pulls the same window again.
	f.clock = f.clock.Add(2 * automation.ReconcileInterval)
	third := certFundingRuntime(t, f, &certFundingReader{
		pages:    [][]execution.AccountEvent{{b, c, d}},
		next:     []string{windowTwoEnd.Format(time.RFC3339Nano)},
		complete: []bool{true},
	})
	certTick(t, third, f)
	if rows := certLedgerEventIDs(t, f); len(rows) != 4 {
		t.Fatalf("certification: a second replay double counted funding: %d events, want 4", len(rows))
	}
}

// runFundingCursorHelper is the child half of the crash: it starts with a
// committed cursor, then dies on the second page of the next window.
func runFundingCursorHelper(t *testing.T) {
	venue := os.Getenv(certHelperVenue)
	account := os.Getenv(certHelperAccount)
	db := openCertificationDB(t, os.Getenv(certHelperDB))
	clock, err := time.Parse(time.RFC3339Nano, os.Getenv(certHelperNow))
	if err != nil {
		t.Fatalf("certification: funding helper clock: %v", err)
	}
	risks, err := risk.NewStore(db)
	if err != nil {
		t.Fatalf("certification: funding helper risk store: %v", err)
	}
	var binding execution.Binding
	if err := db.Where("id = ?", account).First(&binding).Error; err != nil {
		t.Fatalf("certification: funding helper binding: %v", err)
	}
	_, _, c, _ := certFundingEvents(clock, account)
	trader := &certFundingTrader{
		reader: &certFundingReader{
			// The first page succeeds; the second call is where the process dies.
			pages:    [][]execution.AccountEvent{{c}, nil},
			next:     []string{"cursor-page-2", ""},
			complete: []bool{false, false},
			crashAt:  2,
		},
		account: execution.ManagedAccount{AccountKey: account, Venue: venue},
	}
	ctrl := certificationController(t, db, risks, trader, account, venue, func() time.Time { return clock })
	runtime, err := automation.NewRuntime(db, ctrl, risks,
		func(context.Context, string, string) (execution.ManagedTrader, execution.Binding, error) {
			return trader, binding, nil
		}, nil, func() time.Time { return clock })
	if err != nil {
		t.Fatalf("certification: funding helper runtime: %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("certification: funding helper runtime start: %v", err)
	}
	if err := runtime.Tick(context.Background(), "alice", account); err != nil {
		t.Fatalf("certification: funding helper tick: %v", err)
	}
	t.Fatalf("certification: the funding helper never reached its crash page")
}
