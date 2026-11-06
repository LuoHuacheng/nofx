package risk

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"nofx/trader/execution"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	ledgerSandboxKey = "binance:sandbox"
	ledgerLiveKey    = "okx:live"
)

func ledgerDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sql.Close() })
	return db
}

func ledgerStore(t *testing.T) (*Store, *gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "risk.db")
	db := ledgerDB(t, path)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store, db, path
}

func ledgerAccount(key string, env execution.Environment, equity string) execution.ManagedAccount {
	return execution.ManagedAccount{
		AccountKey:   key,
		Venue:        "binance",
		Environment:  env,
		PositionMode: "one-way",
		MarginMode:   "isolated",
		Equity:       execution.Decimal(equity),
	}
}

func observe(t *testing.T, store *Store, a execution.ManagedAccount, events []execution.AccountEvent, at time.Time) {
	t.Helper()
	if err := store.Observe(context.Background(), a, events, at); err != nil {
		t.Fatal(err)
	}
}

func ledgerState(t *testing.T, store *Store, accountKey, day string) EquityState {
	t.Helper()
	state, err := store.EquityState(context.Background(), accountKey, day)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func ledgerEventCount(t *testing.T, db *gorm.DB, accountKey string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&LedgerEvent{}).Where("account_key = ?", accountKey).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRiskLedgerCashflowNeutral(t *testing.T) {
	store, _, _ := ledgerStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	acct := ledgerAccount(ledgerSandboxKey, execution.EnvironmentSandbox, "100")
	observe(t, store, acct, nil, t0)

	state := ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if !execution.Equal(string(state.AdjustedEquity), "100") {
		t.Fatalf("first baseline adjusted=%s, want 100", state.AdjustedEquity)
	}
	if !state.Complete || state.DayStart != "100" || state.Peak != "100" {
		t.Fatalf("sandbox baseline not established: %+v", state)
	}

	deposit := execution.AccountEvent{ID: "dep-1", Kind: KindCashflow, Amount: "50", Currency: "USDT", OccurredAt: t0.Add(time.Minute)}
	acct.Equity = "150"
	observe(t, store, acct, []execution.AccountEvent{deposit}, t0.Add(2*time.Minute))

	state = ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if !execution.Equal(string(state.AdjustedEquity), "100") {
		t.Fatalf("cashflow counted as profit: adjusted=%s", state.AdjustedEquity)
	}
	if !execution.Equal(string(state.NetCashflow), "50") {
		t.Fatalf("net cashflow=%s, want 50", state.NetCashflow)
	}
	if !execution.Equal(string(state.Peak), "100") {
		t.Fatalf("a deposit raised the peak: peak=%s", state.Peak)
	}

	withdrawal := execution.AccountEvent{ID: "wd-1", Kind: KindCashflow, Amount: "-50", Currency: "USDT", OccurredAt: t0.Add(3 * time.Minute)}
	acct.Equity = "100"
	observe(t, store, acct, []execution.AccountEvent{withdrawal}, t0.Add(4*time.Minute))

	state = ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if !execution.Equal(string(state.AdjustedEquity), "100") {
		t.Fatalf("withdrawal counted as a loss: adjusted=%s", state.AdjustedEquity)
	}
	if !execution.Equal(string(state.NetCashflow), "0") {
		t.Fatalf("net cashflow=%s, want 0", state.NetCashflow)
	}
	if !state.Complete {
		t.Fatalf("ledger became incomplete: %+v", state)
	}
	if err := store.Observe(ctx, acct, []execution.AccountEvent{{ID: "bad-1", Kind: "transfer", Amount: "1", Currency: "USDT", OccurredAt: t0}}, t0.Add(5*time.Minute)); err == nil {
		t.Fatal("unknown ledger event kind was accepted")
	}
}

func TestRiskLedgerEventDedup(t *testing.T) {
	store, db, _ := ledgerStore(t)
	t0 := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	acct := ledgerAccount(ledgerSandboxKey, execution.EnvironmentSandbox, "100")
	observe(t, store, acct, nil, t0)

	// The venue equity already contains this fee and funding payment.
	funding := execution.AccountEvent{ID: "fund-1", Kind: KindFunding, Amount: "-1", Currency: "USDT", OccurredAt: t0.Add(time.Minute)}
	fee := execution.AccountEvent{ID: "fee-1", Kind: KindFee, Amount: "-0.5", Currency: "USDT", OccurredAt: t0.Add(2 * time.Minute)}
	observe(t, store, acct, []execution.AccountEvent{funding, fee}, t0.Add(3*time.Minute))

	state := ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if !execution.Equal(string(state.AdjustedEquity), "100") {
		t.Fatalf("fee/funding subtracted twice: adjusted=%s", state.AdjustedEquity)
	}
	if !execution.Equal(string(state.NetCashflow), "0") {
		t.Fatalf("fee/funding moved cashflow: net=%s", state.NetCashflow)
	}
	var fundingRows int64
	if err := db.Model(&LedgerEvent{}).Where("account_key = ? AND kind = ?", ledgerSandboxKey, KindFunding).Count(&fundingRows).Error; err != nil {
		t.Fatal(err)
	}
	if fundingRows != 1 {
		t.Fatalf("events=%d, want 1", fundingRows)
	}

	// Replaying the same venue events must not double count.
	observe(t, store, acct, []execution.AccountEvent{funding, fee}, t0.Add(4*time.Minute))
	if n := ledgerEventCount(t, db, ledgerSandboxKey); n != 2 {
		t.Fatalf("events=%d, want 2 after replay", n)
	}

	cashflow := execution.AccountEvent{ID: "cf-1", Kind: KindCashflow, Amount: "10", Currency: "USDT", OccurredAt: t0.Add(5 * time.Minute)}
	acct.Equity = "110"
	observe(t, store, acct, []execution.AccountEvent{cashflow}, t0.Add(6*time.Minute))
	observe(t, store, acct, []execution.AccountEvent{cashflow}, t0.Add(7*time.Minute))

	state = ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if !execution.Equal(string(state.NetCashflow), "10") {
		t.Fatalf("replayed cashflow counted twice: net=%s", state.NetCashflow)
	}
	if !execution.Equal(string(state.AdjustedEquity), "100") {
		t.Fatalf("adjusted=%s, want 100", state.AdjustedEquity)
	}
	if n := ledgerEventCount(t, db, ledgerSandboxKey); n != 3 {
		t.Fatalf("events=%d, want 3", n)
	}
}

func TestRiskLedgerUTCRollover(t *testing.T) {
	store, db, path := ledgerStore(t)
	d1 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	acct := ledgerAccount(ledgerSandboxKey, execution.EnvironmentSandbox, "100")
	observe(t, store, acct, nil, d1)
	acct.Equity = "130"
	observe(t, store, acct, nil, d1.Add(time.Hour))

	day1 := ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if day1.DayStart != "100" || !execution.Equal(string(day1.Peak), "130") {
		t.Fatalf("day1 baseline=%+v", day1)
	}

	// Restart before the boundary: the persisted peak must survive.
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sql.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := ledgerDB(t, path)
	store, err = NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	day1 = ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if !execution.Equal(string(day1.Peak), "130") {
		t.Fatalf("peak after restart=%s, want 130", day1.Peak)
	}

	// 23:59:59 -> 00:00:00 opens a new day with the carried peak.
	acct.Equity = "130"
	observe(t, store, acct, nil, time.Date(2026, 11, 5, 23, 59, 59, 0, time.UTC))
	d2 := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	observe(t, store, acct, nil, d2)

	day2 := ledgerState(t, store, ledgerSandboxKey, "2026-11-06")
	if day2.DayStart != "130" || !execution.Equal(string(day2.Peak), "130") || !day2.Complete {
		t.Fatalf("day2 did not open on the boundary: %+v", day2)
	}

	// A late deposit for the previous day rewrites that day's baseline but
	// must not lower the persisted peak a drawdown latch was derived from.
	late := execution.AccountEvent{ID: "dep-late", Kind: KindCashflow, Amount: "50", Currency: "USDT", OccurredAt: d1.Add(30 * time.Minute)}
	observe(t, store, acct, []execution.AccountEvent{late}, d2.Add(time.Minute))

	day1 = ledgerState(t, store, ledgerSandboxKey, "2026-11-05")
	if !execution.Equal(string(day1.AdjustedEquity), "80") {
		t.Fatalf("day1 adjusted=%s, want 80 after the late deposit", day1.AdjustedEquity)
	}
	if day1.DayStart != "100" {
		t.Fatalf("day1 start=%s, want the pre-deposit 100", day1.DayStart)
	}
	if !execution.Equal(string(day1.Peak), "130") {
		t.Fatalf("day1 peak lowered to %s, clearing a latch", day1.Peak)
	}

	day2 = ledgerState(t, store, ledgerSandboxKey, "2026-11-06")
	if day2.DayStart != "80" || !execution.Equal(string(day2.AdjustedEquity), "80") {
		t.Fatalf("day2 was not recomputed from persisted observations: %+v", day2)
	}
	if !execution.Equal(string(day2.Peak), "130") {
		t.Fatalf("day2 peak=%s, want the retained 130", day2.Peak)
	}
}

func TestRiskLedgerIncompleteHistory(t *testing.T) {
	store, _, _ := ledgerStore(t)
	live := ledgerAccount(ledgerLiveKey, execution.EnvironmentLive, "100")
	observe(t, store, live, nil, time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC))

	state := ledgerState(t, store, ledgerLiveKey, "2026-11-05")
	if state.Complete {
		t.Fatalf("live account fabricated a day start: %+v", state)
	}
	if state.DayStart != "" {
		t.Fatalf("live day start=%s, want unset", state.DayStart)
	}

	sandbox := ledgerAccount("binance:sandbox-2", execution.EnvironmentSandbox, "100")
	observe(t, store, sandbox, nil, time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC))
	if s := ledgerState(t, store, "binance:sandbox-2", "2026-11-05"); !s.Complete || s.DayStart != "100" {
		t.Fatalf("sandbox baseline rejected: %+v", s)
	}

	// A live account that observed the UTC boundary does have a day start.
	live2 := ledgerAccount("okx:live-2", execution.EnvironmentLive, "100")
	observe(t, store, live2, nil, time.Date(2026, 11, 5, 23, 59, 59, 0, time.UTC))
	d2 := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	observe(t, store, live2, nil, d2)
	if s := ledgerState(t, store, "okx:live-2", "2026-11-06"); !s.Complete || s.DayStart != "100" {
		t.Fatalf("observed boundary not accepted for live: %+v", s)
	}

	// A missing funding page marks the ledger incomplete.
	gap := execution.AccountEvent{ID: "gap-1", Kind: KindFundingGap, OccurredAt: d2.Add(time.Minute)}
	observe(t, store, live2, []execution.AccountEvent{gap}, d2.Add(2*time.Minute))
	if s := ledgerState(t, store, "okx:live-2", "2026-11-06"); s.Complete {
		t.Fatalf("missing funding history accepted: %+v", s)
	}

	// An unverifiable cashflow currency is retained but never counted.
	live3 := ledgerAccount("okx:live-3", execution.EnvironmentLive, "100")
	observe(t, store, live3, nil, time.Date(2026, 11, 5, 23, 59, 59, 0, time.UTC))
	live3.Equity = "125"
	unknown := execution.AccountEvent{ID: "dep-unknown", Kind: KindCashflow, Amount: "25", Currency: "", OccurredAt: d2.Add(time.Minute)}
	observe(t, store, live3, []execution.AccountEvent{unknown}, d2.Add(2*time.Minute))
	if s := ledgerState(t, store, "okx:live-3", "2026-11-06"); s.Complete {
		t.Fatalf("cashflow with no currency accepted: %+v", s)
	}
	if s := ledgerState(t, store, "okx:live-3", "2026-11-06"); !execution.Equal(string(s.AdjustedEquity), "125") {
		t.Fatalf("unverifiable cashflow counted: adjusted=%s", s.AdjustedEquity)
	}

	btc := execution.AccountEvent{ID: "dep-btc", Kind: KindCashflow, Amount: "5", Currency: "BTC", OccurredAt: d2.Add(3 * time.Minute)}
	observe(t, store, live3, []execution.AccountEvent{btc}, d2.Add(4*time.Minute))
	if s := ledgerState(t, store, "okx:live-3", "2026-11-06"); s.Complete {
		t.Fatalf("non-USDT cashflow accepted: %+v", s)
	}
}

func TestRiskLedgerGapIsDayLocal(t *testing.T) {
	store, _, _ := ledgerStore(t)
	const key = "okx:gap-local"
	live := ledgerAccount(key, execution.EnvironmentLive, "100")
	observe(t, store, live, nil, time.Date(2026, 11, 5, 0, 0, 30, 0, time.UTC))
	if s := ledgerState(t, store, key, "2026-11-05"); !s.Complete {
		t.Fatalf("day 1 without a gap should be complete: %+v", s)
	}

	// The gap belongs to day 1 only; day 2 observed at its own boundary is complete.
	gap := execution.AccountEvent{ID: "gap-day1", Kind: KindFundingGap, OccurredAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	observe(t, store, live, []execution.AccountEvent{gap}, time.Date(2026, 11, 6, 0, 0, 30, 0, time.UTC))
	if s := ledgerState(t, store, key, "2026-11-05"); s.Complete {
		t.Fatalf("day 1 with a funding gap became complete: %+v", s)
	}
	if s := ledgerState(t, store, key, "2026-11-06"); !s.Complete || s.DayStart != "100" {
		t.Fatalf("day 2 inherited the day 1 gap: %+v", s)
	}
}

func TestRiskLedgerReopenKeepsBalances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "risk.db")
	ctx := context.Background()
	t0 := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)

	type step struct {
		equity string
		at     time.Time
		events []execution.AccountEvent
	}
	deposit := execution.AccountEvent{ID: "dep-1", Kind: KindCashflow, Amount: "50", Currency: "USDT", OccurredAt: t0.Add(time.Minute)}
	withdrawal := execution.AccountEvent{ID: "wd-1", Kind: KindCashflow, Amount: "-50", Currency: "USDT", OccurredAt: t0.Add(3 * time.Minute)}
	steps := []step{
		{"100", t0, nil},
		{"150", t0.Add(2 * time.Minute), []execution.AccountEvent{deposit}},
		{"100", t0.Add(4 * time.Minute), []execution.AccountEvent{withdrawal}},
		{"150", t0.Add(6 * time.Minute), []execution.AccountEvent{deposit}},
	}

	replay := func(store *Store) (EquityState, int64) {
		t.Helper()
		for _, s := range steps {
			acct := ledgerAccount(ledgerSandboxKey, execution.EnvironmentSandbox, s.equity)
			if err := store.Observe(ctx, acct, s.events, s.at); err != nil {
				t.Fatal(err)
			}
		}
		return ledgerState(t, store, ledgerSandboxKey, "2026-11-05"), ledgerEventCount(t, store.db, ledgerSandboxKey)
	}

	db := ledgerDB(t, path)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	first, firstCount := replay(store)
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sql.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(ledgerDB(t, path))
	if err != nil {
		t.Fatal(err)
	}
	second, secondCount := replay(reopened)

	for _, v := range []struct{ name, a, b string }{
		{"adjusted equity", string(first.AdjustedEquity), string(second.AdjustedEquity)},
		{"day start", string(first.DayStart), string(second.DayStart)},
		{"peak", string(first.Peak), string(second.Peak)},
		{"net cashflow", string(first.NetCashflow), string(second.NetCashflow)},
	} {
		if !execution.Equal(v.a, v.b) {
			t.Fatalf("%s changed across reopen: %s != %s", v.name, v.a, v.b)
		}
	}
	if first.Complete != second.Complete {
		t.Fatalf("completeness changed across reopen: %v != %v", first.Complete, second.Complete)
	}
	if firstCount != secondCount {
		t.Fatalf("ledger events changed across reopen: %d != %d", firstCount, secondCount)
	}
	if firstCount != 2 {
		t.Fatalf("events=%d, want 2", firstCount)
	}
}

func TestRiskStorePolicyRoundTrip(t *testing.T) {
	store, _, _ := ledgerStore(t)
	ctx := context.Background()
	p := defaultTestPolicy()
	p.Symbols = []string{"BTCUSDT"}
	p.Enabled = true
	p.Version = "" // the server derives the version, callers cannot supply it
	put, err := store.PutPolicy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	want, err := PolicyVersion(put)
	if err != nil {
		t.Fatal(err)
	}
	if put.Version != want || put.ID == "" {
		t.Fatalf("stored policy not normalized: %+v", put)
	}

	got, err := store.Policy(ctx, p.UserID, p.AccountKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != put.Version || !got.Enabled || len(got.Symbols) != 1 || got.Symbols[0] != "BTCUSDT" {
		t.Fatalf("policy round trip mismatch: %+v", got)
	}

	if _, err := store.Policy(ctx, "nobody", "binance:none"); err == nil {
		t.Fatal("missing policy returned no error")
	}

	p.PerTradeRiskBPS = 100 // looser than the design default
	if _, err := store.PutPolicy(ctx, p); err == nil {
		t.Fatal("looser policy accepted")
	}
}
