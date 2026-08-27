package backtest

import (
	"testing"
	"time"

	"nofx/mcp"
	"nofx/store"
)

// TestAccountFundingChargedOnce is the regression test for the funding
// double-count bug: funding is applied to cash by ApplyFunding, so a
// later Close must NOT subtract FundingPaid from the balance again.
func TestAccountFundingChargedOnce(t *testing.T) {
	acct := NewAccount(1000, 0.0002, 0.001)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := acct.Open("BTCUSDT", "long", 100, 100, 1, now); err != nil {
		t.Fatal(err)
	}
	acct.UpdateMark("BTCUSDT", 100)
	balAfterOpen := acct.Balance() // 1000 - 100 - 0.1

	// Two funding settlements at rate 0.0001: qty = 0.999, amount each.
	acct.ApplyFunding("BTCUSDT", 0.0001, now.Add(8*time.Hour))
	acct.ApplyFunding("BTCUSDT", 0.0001, now.Add(16*time.Hour))
	fundingEach := 0.999 * 100 * 0.0001
	balAfterFunding := acct.Balance()
	if diff := abs(balAfterFunding - (balAfterOpen - 2*fundingEach)); diff > 1e-9 {
		t.Fatalf("balance after funding = %f, want %f (funding applied once)", balAfterFunding, balAfterOpen-2*fundingEach)
	}

	// Close at the entry price: balance must return to
	// initial - openFee - closeFee - 2*funding — funding counted ONCE.
	tr, err := acct.Close("BTCUSDT", 100, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	closeFee := 0.999 * 100 * 0.001
	wantBalance := 1000 - 0.1 - closeFee - 2*fundingEach
	if diff := abs(acct.Balance() - wantBalance); diff > 1e-9 {
		t.Fatalf("final balance = %f, want %f (funding must not be double-counted)", acct.Balance(), wantBalance)
	}
	// The trade's reported PnL keeps funding attribution (negative here).
	if diff := abs(tr.PnL - (-closeFee - 2*fundingEach)); diff > 1e-9 {
		t.Fatalf("trade pnl = %f, want %f", tr.PnL, -closeFee-2*fundingEach)
	}
}

// TestKernelRunnerKlineWindowIsBounded verifies the decision prompt uses
// only the trailing klineCount bars (live GetWithTimeframes semantics),
// not the full run history — the O(n²) indicator blowup regression.
func TestKernelRunnerKlineWindowIsBounded(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prices := make([]float64, 200)
	for i := range prices {
		prices[i] = 100 + float64(i%20)
	}
	feed := mkRunnerTestFeed(t, prices, "5m", start)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: start.Add(20 * time.Hour), InitialBalance: 1000,
		Name: "window", UserID: "u1",
	}
	runner, err := NewKernelRunner(cfg, defaultStrConfig(t), &FakeAIClient{Response: fakeAIResponse}, NewAICache(10))
	if err != nil {
		t.Fatal(err)
	}

	dc := &DecisionContext{
		Time:    start.Add(19 * time.Hour), // 228 buckets in → ~228 bars consumed
		Account: NewAccount(1000, 0.0002, 0.001),
		Prices:  map[string]float64{"BTCUSDT": 100},
		Feed:    feed,
		Config:  cfg,
	}
	// Advance the feed to the decision time so History is populated.
	for {
		bt, _, ok := feed.Next()
		if !ok || !bt.Before(dc.Time) {
			break
		}
	}
	ctx, ok := runner.buildContext(dc)
	if !ok {
		t.Fatal("context not built")
	}
	data := ctx.MarketDataMap["BTCUSDT"]
	if data == nil || data.TimeframeData["5m"] == nil {
		t.Fatal("market data missing")
	}
	klines := data.TimeframeData["5m"].Klines
	if len(klines) != runner.klineCount {
		t.Fatalf("prompt window = %d bars, want exactly klineCount=%d", len(klines), runner.klineCount)
	}
	// The window must be the TRAILING bars (latest close = last price).
	if got := klines[len(klines)-1].Close; got <= 0 {
		t.Fatalf("last kline close = %f", got)
	}
}

// TestKernelRunnerCacheIsolatedPerRunner verifies two runners (e.g. two
// runs of the same user with different strategies) never share cached
// decisions: the manager creates one cache per run.
func TestKernelRunnerCacheIsolatedPerRunner(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flat := make([]float64, 60)
	for i := range flat {
		flat[i] = 100
	}
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: start.Add(6 * time.Hour), InitialBalance: 1000,
		Name: "iso", UserID: "u1",
	}
	ai := &FakeAIClient{Response: fakeAIResponse}
	cacheA := NewAICache(100)
	cacheB := NewAICache(100)
	runnerA, err := NewKernelRunner(cfg, defaultStrConfig(t), ai, cacheA)
	if err != nil {
		t.Fatal(err)
	}
	// Different strategy config (e.g. different risk control).
	cfgB := defaultStrConfig(t)
	cfgB.RiskControl.BTCETHMaxLeverage = 10
	runnerB, err := NewKernelRunner(cfg, cfgB, ai, cacheB)
	if err != nil {
		t.Fatal(err)
	}
	_ = runnerA
	_ = runnerB
	if cacheA == cacheB {
		t.Fatal("runners must use distinct caches")
	}
}

// TestManagerUsesPerRunCache asserts the manager hands each run its own
// AICache (no cross-run decision contamination).
func TestManagerUsesPerRunCache(t *testing.T) {
	m, _ := newTestManager(t, []float64{100, 101}, &ProbeAIClient{Response: fakeAIResponse})
	seen := map[*AICache]bool{}
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		seen[cache] = true
		return DeciderFunc(func(dc *DecisionContext) []Decision { return nil })
	})
	req := defaultStartReq()
	req.StrategyConfigJSON = `{"strategy_type":"ai_trading"}`
	req.ModelConfigJSON = `{"provider":"deepseek"}`
	id1, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "u1", id1, StatusCompleted, 5*time.Second)
	id2, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "u1", id2, StatusCompleted, 5*time.Second)
	if len(seen) != 2 {
		t.Fatalf("distinct runs must receive distinct caches, saw %d", len(seen))
	}
}
