package backtest

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestEngineFixedDecider is the P3.1 integration main line: a fixed
// input (1 symbol, deterministic klines, a fixed decision sequence)
// produces an exactly assertable balance/equity/trades/metrics.
func TestEngineFixedDecider(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	// 5m bars: 100 .. 110 then flat at 110.
	var bars []Bar
	for i := 0; i < 24; i++ {
		close := 100.0 + float64(i)
		if close > 110 {
			close = 110
		}
		bars = append(bars, Bar{
			Time: start.Add(time.Duration(5*i+5) * time.Minute),
			Open: close - 1, High: close + 1, Low: close - 2, Close: close, Volume: 100,
		})
	}

	feed := NewMemoryFeed([]string{"BTCUSDT"}, map[string][]Bar{"BTCUSDT": bars}, end, nil, nil)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: end, InitialBalance: 1000,
	}

	// Decision sequence: open long on the first decision, close once the
	// price reaches 110 (the last up-bar close).
	calls := 0
	decider := DeciderFunc(func(dc *DecisionContext) []Decision {
		calls++
		if !dc.Account.HasPosition("BTCUSDT") && dc.Prices["BTCUSDT"] < 110 {
			return []Decision{{Symbol: "BTCUSDT", Action: "open_long", PositionSizeUSD: 500, Leverage: 2}}
		}
		if dc.Account.HasPosition("BTCUSDT") && dc.Prices["BTCUSDT"] >= 110 {
			return []Decision{{Symbol: "BTCUSDT", Action: "close_long"}}
		}
		return nil
	})

	eng, err := NewEngine(cfg, feed, decider)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Exact expectations (fee taker 0.001 Binance default).
	// open at 100: fee 0.5, margin 250, qty 4.995, balance 749.5
	// close at 110: notional 549.45, fee 0.54945, pnl = 49.95 - 0.54945
	const wantBalance = 1048.90055
	if diff := abs(res.Equity[len(res.Equity)-1].Equity - wantBalance); diff > 1e-6 {
		t.Fatalf("final equity = %f, want %f", res.Equity[len(res.Equity)-1].Equity, wantBalance)
	}
	if len(res.Trades) != 2 {
		t.Fatalf("trades = %d, want 2 (open+close)", len(res.Trades))
	}
	if res.Trades[0].Position != "open" || res.Trades[0].Price != 100 {
		t.Fatalf("first trade = %+v", res.Trades[0])
	}
	if res.Trades[1].Position != "close" || res.Trades[1].Price != 110 {
		t.Fatalf("second trade = %+v", res.Trades[1])
	}
	if diff := abs(res.Trades[1].PnL - 49.40055); diff > 1e-6 {
		t.Fatalf("close pnl = %f, want 49.40055", res.Trades[1].PnL)
	}
	if calls < 2 {
		t.Fatalf("decider called %d times, want >= 2", calls)
	}

	m := res.Metrics(1000)
	if diff := abs(m.FinalBalance - wantBalance); diff > 1e-6 {
		t.Fatalf("metrics final = %f", m.FinalBalance)
	}
	if m.TradeCount != 1 {
		t.Fatalf("metrics trade count = %d, want 1", m.TradeCount)
	}
	if diff := abs(m.WinRate - 100); diff > 1e-9 {
		t.Fatalf("win rate = %f, want 100", m.WinRate)
	}
	if diff := abs(m.TotalReturnPct - 4.890055); diff > 1e-4 {
		t.Fatalf("return pct = %f, want 4.890055", m.TotalReturnPct)
	}
}

// TestEngineTakeProfit verifies bar-on-close TP exits at the TP level.
func TestEngineTakeProfit(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	var bars []Bar
	prices := []float64{100, 101, 102, 103, 106, 107}
	for i, p := range prices {
		bars = append(bars, Bar{
			Time: start.Add(time.Duration(5*i+5) * time.Minute),
			Open: p - 1, High: p + 3, Low: p - 2, Close: p, Volume: 100,
		})
	}
	feed := NewMemoryFeed([]string{"BTCUSDT"}, map[string][]Bar{"BTCUSDT": bars}, end, nil, nil)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: end, InitialBalance: 1000,
	}
	opened := false
	decider := DeciderFunc(func(dc *DecisionContext) []Decision {
		if !opened {
			opened = true
			return []Decision{{Symbol: "BTCUSDT", Action: "open_long", PositionSizeUSD: 100, Leverage: 1, TakeProfit: 105}}
		}
		return nil
	})
	eng, err := NewEngine(cfg, feed, decider)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The bar with close 106 (high 109) crosses TP 105 -> exit at 105.
	var tpExit *BacktestTrade
	for i := range res.Trades {
		if res.Trades[i].Position == "close" {
			tpExit = &res.Trades[i]
		}
	}
	if tpExit == nil {
		t.Fatal("no close trade found")
	}
	if diff := abs(tpExit.Price - 105); diff > 1e-9 {
		t.Fatalf("TP exit price = %f, want 105", tpExit.Price)
	}
	// Position must be closed and a TP trace recorded.
	if eng.Account().HasPosition("BTCUSDT") {
		t.Fatal("position should be closed by TP")
	}
	found := false
	for _, tr := range res.Trace {
		if tr.Kind == "fill" && strings.HasPrefix(tr.Message, "take_profit") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no take_profit trace: %+v", res.Trace)
	}
}

// TestEngineLiquidation verifies force-close when price crosses the
// liquidation level.
func TestEngineLiquidation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	var bars []Bar
	prices := []float64{100, 90, 80, 45}
	for i, p := range prices {
		bars = append(bars, Bar{
			Time: start.Add(time.Duration(5*i+5) * time.Minute),
			Open: p, High: p + 1, Low: p - 2, Close: p, Volume: 100,
		})
	}
	feed := NewMemoryFeed([]string{"BTCUSDT"}, map[string][]Bar{"BTCUSDT": bars}, end, nil, nil)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: end, InitialBalance: 1000,
	}
	decider := DeciderFunc(func(dc *DecisionContext) []Decision {
		if !dc.Account.HasPosition("BTCUSDT") && dc.Prices["BTCUSDT"] > 90 {
			return []Decision{{Symbol: "BTCUSDT", Action: "open_long", PositionSizeUSD: 500, Leverage: 2}}
		}
		return nil
	})
	eng, err := NewEngine(cfg, feed, decider)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !eng.Account().Liquidated() {
		t.Fatal("account should be liquidated")
	}
	liqFound := false
	for _, tr := range res.Trace {
		if tr.Kind == "liquidation" {
			liqFound = true
		}
	}
	if !liqFound {
		t.Fatalf("no liquidation trace: %+v", res.Trace)
	}
}

// TestEngineContextCancel verifies cancellation propagates.
func TestEngineContextCancel(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	bars := make([]Bar, 0, 100)
	for i := 0; i < 100; i++ {
		bars = append(bars, Bar{
			Time:  start.Add(time.Duration(5*i+5) * time.Minute),
			Close: 100, Open: 100, High: 100, Low: 100, Volume: 1,
		})
	}
	feed := NewMemoryFeed([]string{"BTCUSDT"}, map[string][]Bar{"BTCUSDT": bars}, end, nil, nil)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: end, InitialBalance: 1000,
	}
	eng, err := NewEngine(cfg, feed, DeciderFunc(func(dc *DecisionContext) []Decision { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.Run(ctx); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// TestEngineMultiSymbol exercises the outer-join path with two symbols
// on different cadences; both end up tradeable and priced.
func TestEngineMultiSymbol(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	mkBars := func(base float64) []Bar {
		var bs []Bar
		for i := 0; i < 20; i++ {
			bs = append(bs, Bar{
				Time: start.Add(time.Duration(5*i+5) * time.Minute),
				Open: base, High: base + 1, Low: base - 1, Close: base, Volume: 100,
			})
		}
		return bs
	}
	feed := NewMemoryFeed(
		[]string{"BTCUSDT", "ETHUSDT"},
		map[string][]Bar{"BTCUSDT": mkBars(100), "ETHUSDT": mkBars(50)},
		end, nil, nil,
	)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT", "ETHUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: end, InitialBalance: 10000,
	}
	decider := DeciderFunc(func(dc *DecisionContext) []Decision {
		return []Decision{
			{Symbol: "BTCUSDT", Action: "open_long", PositionSizeUSD: 1000, Leverage: 1},
			{Symbol: "ETHUSDT", Action: "open_long", PositionSizeUSD: 1000, Leverage: 1},
		}
	})
	eng, err := NewEngine(cfg, feed, decider)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if eng.Account().PositionAt("BTCUSDT") == nil || eng.Account().PositionAt("ETHUSDT") == nil {
		t.Fatalf("both positions should be open: %+v", eng.Account().Positions())
	}
	if len(res.Equity) < 2 {
		t.Fatalf("equity curve too short: %d", len(res.Equity))
	}
}

// TestEnginePartialClose verifies a scale-out keeps the remainder open
// with the protective stop intact, and releases margin proportionally.
func TestEnginePartialClose(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	var bars []Bar
	for i := 0; i < 20; i++ {
		bars = append(bars, Bar{
			Time: start.Add(time.Duration(5*(i+1)) * time.Minute),
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 100,
		})
	}
	feed := NewMemoryFeed([]string{"BTCUSDT"}, map[string][]Bar{"BTCUSDT": bars}, end, nil, nil)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: end, InitialBalance: 1000,
	}
	step := 0
	decider := DeciderFunc(func(dc *DecisionContext) []Decision {
		step++
		switch step {
		case 1:
			return []Decision{{Symbol: "BTCUSDT", Action: "open_long", PositionSizeUSD: 100, Leverage: 2, StopLoss: 95}}
		case 2:
			return []Decision{{Symbol: "BTCUSDT", Action: "partial_close", ClosePct: 0.5}}
		}
		return nil
	})
	eng, err := NewEngine(cfg, feed, decider)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	pos := eng.Account().PositionAt("BTCUSDT")
	if pos == nil {
		t.Fatal("position must remain open after partial close")
	}
	if pos.StopLoss != 95 {
		t.Fatalf("protective stop must survive partial close, got %v", pos.StopLoss)
	}
	// notional 100 USDT, leverage 2 -> ~1.0 qty / 50 margin before the
	// half close (fees come from the default taker fallback).
	if pos.Qty <= 0.4 || pos.Qty >= 0.6 {
		t.Fatalf("expected roughly half qty remaining, got %f", pos.Qty)
	}
	if pos.Margin <= 0 || pos.Margin >= 30 {
		t.Fatalf("expected roughly half margin released, got %f", pos.Margin)
	}
	var partial *BacktestTrade
	for i := range res.Trades {
		if res.Trades[i].Position == "close" {
			partial = &res.Trades[i]
		}
	}
	if partial == nil {
		t.Fatal("no partial close trade recorded")
	}
}

// TestEngineAdjustStopLoss verifies moving the stop changes the exit
// trigger price for the position.
func TestEngineAdjustStopLoss(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	var bars []Bar
	for i := 0; i < 20; i++ {
		bars = append(bars, Bar{
			Time: start.Add(time.Duration(5*(i+1)) * time.Minute),
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 100,
		})
	}
	feed := NewMemoryFeed([]string{"BTCUSDT"}, map[string][]Bar{"BTCUSDT": bars}, end, nil, nil)
	cfg := RunConfig{
		Symbols: []string{"BTCUSDT"}, Timeframe: "5m",
		StartTime: start, EndTime: end, InitialBalance: 1000,
	}
	step := 0
	decider := DeciderFunc(func(dc *DecisionContext) []Decision {
		step++
		switch step {
		case 1:
			return []Decision{{Symbol: "BTCUSDT", Action: "open_long", PositionSizeUSD: 100, Leverage: 2, StopLoss: 95}}
		case 2:
			return []Decision{{Symbol: "BTCUSDT", Action: "adjust_stop_loss", StopLoss: 98.5}}
		}
		return nil
	})
	eng, err := NewEngine(cfg, feed, decider)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pos := eng.Account().PositionAt("BTCUSDT")
	if pos == nil {
		t.Fatal("position must remain open after stop move")
	}
	if pos.StopLoss != 98.5 {
		t.Fatalf("stop must move to 98.5, got %v", pos.StopLoss)
	}
	found := false
	for _, tr := range res.Trace {
		if tr.Kind == "decision" && strings.Contains(tr.Message, "stop moved to 98.5000") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no stop-move trace recorded: %+v", res.Trace)
	}
}
