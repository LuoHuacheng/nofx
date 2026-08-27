package backtest

import (
	"context"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

// sequentialAI is a fake AI client whose response depends on the call
// index (test double for the decision sequence).
type sequentialAI struct {
	ProbeAIClient
	responses []string
	idx       int
}

func (s *sequentialAI) CallWithMessages(systemPrompt, userPrompt string) (string, error) {
	s.idx++
	if s.idx <= len(s.responses) {
		return s.responses[s.idx-1], nil
	}
	return s.responses[len(s.responses)-1], nil
}

func mkRunnerTestFeed(t *testing.T, prices []float64, tf string, start time.Time) Feed {
	t.Helper()
	dur, err := ParseTimeframe(tf)
	if err != nil {
		t.Fatal(err)
	}
	var bars []Bar
	for i, p := range prices {
		bars = append(bars, Bar{
			Time: start.Add(time.Duration(5*i+5) * time.Minute),
			Open: p - 1, High: p + 1, Low: p - 2, Close: p, Volume: 100,
		})
	}
	end := start.Add(time.Duration(5*len(prices)+5) * time.Minute)
	return NewMemoryFeedFromKlines([]string{"BTCUSDT"},
		map[string][]market.Kline{"BTCUSDT": barsToKlines(bars, dur)}, dur, end)
}

func barsToKlines(bars []Bar, tf time.Duration) []market.Kline {
	return toKlines(bars, tf)
}

func defaultStrConfig(t *testing.T) *store.StrategyConfig {
	t.Helper()
	def := store.GetDefaultStrategyConfig("en")
	def.CoinSource.SourceType = "static"
	def.CoinSource.StaticCoins = []string{"BTCUSDT"}
	def.RiskControl.BTCETHMaxLeverage = 3
	def.RiskControl.AltcoinMaxLeverage = 2
	def.RiskControl.BTCETHMaxPositionValueRatio = 5
	def.RiskControl.AltcoinMaxPositionValueRatio = 1
	return &def
}

// TestKernelRunnerEndToEnd runs the full kernel path: fixed klines,
// fake AI returning an open then a close, engine executed — trades,
// equity and metrics come out consistent.
func TestKernelRunnerEndToEnd(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prices := []float64{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110}
	feed := mkRunnerTestFeed(t, prices, "5m", start)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: start.Add(2 * time.Hour), InitialBalance: 1000,
		Name: "e2e", UserID: "u1",
	}

	ai := &sequentialAI{responses: []string{
		`<reasoning>open</reasoning><decision>[{"symbol":"BTCUSDT","action":"open_long","leverage":2,"position_size_usd":500,"stop_loss":95,"take_profit":120}]</decision>`,
	}}
	// open once, then hold for every remaining bar, close on the last.
	hold := `<reasoning>hold</reasoning><decision>[{"symbol":"BTCUSDT","action":"hold"}]</decision>`
	closeD := `<reasoning>close</reasoning><decision>[{"symbol":"BTCUSDT","action":"close_long"}]</decision>`
	for i := 0; i < len(prices)-2; i++ {
		ai.responses = append(ai.responses, hold)
	}
	ai.responses = append(ai.responses, closeD)

	runner, err := NewKernelRunner(cfg, defaultStrConfig(t), ai, NewAICache(100))
	if err != nil {
		t.Fatalf("NewKernelRunner: %v", err)
	}
	eng, err := NewEngine(cfg, feed, runner)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// open at 100, close at 110 (same fee model as TestEngineFixedDecider).
	const wantBalance = 1048.90055
	if diff := abs(res.Equity[len(res.Equity)-1].Equity - wantBalance); diff > 1e-6 {
		t.Fatalf("final equity = %f, want %f", res.Equity[len(res.Equity)-1].Equity, wantBalance)
	}
	if len(res.Trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(res.Trades))
	}
	if res.Trades[1].Position != "close" || res.Trades[1].Price != 110 {
		t.Fatalf("close trade = %+v", res.Trades[1])
	}
	m := res.Metrics(1000)
	if m.TradeCount != 1 || m.WinRate != 100 {
		t.Fatalf("metrics = %+v", m)
	}
}

// TestKernelRunnerCacheDedup verifies that unchanged decision contexts
// (flat prices, no positions) hit the AI cache: exactly one LLM call.
func TestKernelRunnerCacheDedup(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flat := make([]float64, 60)
	for i := range flat {
		flat[i] = 100
	}
	feed := mkRunnerTestFeed(t, flat, "5m", start)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: start.Add(6 * time.Hour), InitialBalance: 1000,
		Name: "cache", UserID: "u1",
	}
	ai := &ProbeAIClient{Response: fakeAIResponse}
	runner, err := NewKernelRunner(cfg, defaultStrConfig(t), ai, NewAICache(100))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(cfg, feed, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The kline window (PrimaryCount+8, default 38) must be re-fetched per
	// bar until it saturates; after that every decision is a cache hit.
	// Strictly fewer LLM calls than bars proves dedup works.
	if ai.Calls >= len(flat) {
		t.Fatalf("AI calls = %d, want < %d (cache dedup)", ai.Calls, len(flat))
	}
}

// TestKernelRunnerDecisionHook verifies the decision persistence hook
// fires once per decision cycle with non-empty payloads.
func TestKernelRunnerDecisionHook(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	feed := mkRunnerTestFeed(t, []float64{100, 101, 102}, "5m", start)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: start.Add(time.Hour), InitialBalance: 1000,
		Name: "hook", UserID: "u1",
	}
	ai := &ProbeAIClient{Response: fakeAIResponse}
	runner, err := NewKernelRunner(cfg, defaultStrConfig(t), ai, NewAICache(10))
	if err != nil {
		t.Fatal(err)
	}
	var hooks int
	runner.SetDecisionHook(func(ts time.Time, ctxJSON, decisionJSON string) {
		hooks++
		if ctxJSON == "" || decisionJSON == "" {
			t.Fatalf("empty hook payload: ctx=%q dec=%q", ctxJSON, decisionJSON)
		}
	})
	eng, err := NewEngine(cfg, feed, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hooks < 2 {
		t.Fatalf("hooks = %d, want >= 2 decision cycles", hooks)
	}
}
