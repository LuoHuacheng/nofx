package store

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestBacktestStore(t *testing.T) *BacktestStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	bs := NewBacktestStore(db)
	if err := bs.initTables(); err != nil {
		t.Fatalf("init backtest tables: %v", err)
	}
	return bs
}

// TestBacktestRunRoundTrip verifies the snapshot fields survive a
// create/load cycle unchanged (P3.2 §5: snapshot round-trip).
func TestBacktestRunRoundTrip(t *testing.T) {
	bs := newTestBacktestStore(t)

	strategyJSON, _ := json.Marshal(map[string]any{"strategy_type": "ai_trading", "language": "zh"})
	modelJSON, _ := json.Marshal(map[string]any{"provider": "deepseek", "model": "deepseek-chat"})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	run := &BacktestRun{
		ID:                 "run-1",
		Name:               "BTC 5m",
		UserID:             "user-1",
		Status:             "running",
		StrategyConfigJSON: string(strategyJSON),
		ModelConfigJSON:    string(modelJSON),
		Symbols:            MarshalSymbols([]string{"BTCUSDT", "ETHUSDT"}),
		StartTime:          now,
		EndTime:            now.Add(24 * time.Hour),
		Timeframe:          "5m",
		ExchangeType:       "binance",
		FeeMaker:           0.0002,
		FeeTaker:           0.001,
		InitialBalance:     1000,
		FinalBalance:       1234.5,
		TotalReturnPct:     23.45,
		MaxDrawdownPct:     3.21,
		SharpeRatio:        1.8,
		WinRate:            55.5,
		ProfitFactor:       2.1,
		TradeCount:         12,
	}
	if err := bs.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := bs.GetRun("user-1", "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.StrategyConfigJSON != run.StrategyConfigJSON {
		t.Fatalf("strategy snapshot mismatch: %q vs %q", got.StrategyConfigJSON, run.StrategyConfigJSON)
	}
	if got.ModelConfigJSON != run.ModelConfigJSON {
		t.Fatalf("model snapshot mismatch")
	}
	syms, err := UnmarshalSymbols(got.Symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 2 || syms[0] != "BTCUSDT" || syms[1] != "ETHUSDT" {
		t.Fatalf("symbols = %v", syms)
	}
	if got.FinalBalance != 1234.5 || got.TradeCount != 12 || got.Timeframe != "5m" {
		t.Fatalf("metrics mismatch: %+v", got)
	}
	if got.FeeTaker != 0.001 || got.SharpeRatio != 1.8 {
		t.Fatalf("fee/sharpe mismatch: %+v", got)
	}
}

// TestBacktestStoreListPaginationAndFilter verifies pagination and the
// status filter on ListRuns.
func TestBacktestStoreListPaginationAndFilter(t *testing.T) {
	bs := newTestBacktestStore(t)
	for i := 0; i < 5; i++ {
		status := "completed"
		if i%2 == 0 {
			status = "failed"
		}
		if err := bs.CreateRun(&BacktestRun{
			ID:      string(rune('a' + i)),
			UserID:  "user-1",
			Status:  status,
			Symbols: "[]",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// page 1, page_size 2
	runs, total, err := bs.ListRuns("user-1", 1, 2, "all")
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(runs) != 2 {
		t.Fatalf("page1: total=%d len=%d", total, len(runs))
	}
	runs2, _, err := bs.ListRuns("user-1", 3, 2, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs2) != 1 {
		t.Fatalf("page3: len=%d", len(runs2))
	}
	// status filter
	failed, total, err := bs.ListRuns("user-1", 1, 20, "failed")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(failed) != 3 {
		t.Fatalf("failed filter: total=%d len=%d", total, len(failed))
	}
	// user scoping
	other, _, err := bs.ListRuns("other-user", 1, 20, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other user should see nothing, got %d", len(other))
	}
}

// TestBacktestStoreEquityAndTrades verifies batch append + ranged reads.
func TestBacktestStoreEquityAndTrades(t *testing.T) {
	bs := newTestBacktestStore(t)
	if err := bs.CreateRun(&BacktestRun{ID: "run-1", UserID: "user-1", Symbols: "[]"}); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := make([]BacktestEquityPoint, 0, 10)
	for i := 0; i < 10; i++ {
		pts = append(pts, BacktestEquityPoint{
			RunID: "run-1", Time: base.Add(time.Duration(i) * time.Minute),
			Balance: 1000, Equity: 1000 + float64(i),
		})
	}
	if err := bs.AppendEquityPoints(pts); err != nil {
		t.Fatalf("append equity: %v", err)
	}
	n, err := bs.CountEquityPoints("run-1")
	if err != nil || n != 10 {
		t.Fatalf("count = %d, err=%v", n, err)
	}
	from := base.Add(3 * time.Minute)
	to := base.Add(6 * time.Minute)
	got, err := bs.GetEquityPoints("run-1", &from, &to, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("ranged points = %d, want 4 (3..6)", len(got))
	}
	if got[0].Equity != 1003 || got[3].Equity != 1006 {
		t.Fatalf("ranged equity values wrong: %+v", got)
	}

	trades := []BacktestTradeEvent{
		{RunID: "run-1", Time: base.Add(1 * time.Minute), Symbol: "BTCUSDT", Side: "buy", Position: "open", Price: 100, Size: 1, Fee: 0.1},
		{RunID: "run-1", Time: base.Add(2 * time.Minute), Symbol: "BTCUSDT", Side: "sell", Position: "close", Price: 110, Size: 1, Fee: 0.11, PnL: 9.79},
	}
	if err := bs.AppendTrades(trades); err != nil {
		t.Fatal(err)
	}
	gotTrades, err := bs.GetTrades("run-1", nil, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTrades) != 2 || gotTrades[1].PnL != 9.79 {
		t.Fatalf("trades = %+v", gotTrades)
	}

	// decision logs
	logs := []BacktestDecisionLog{
		{RunID: "run-1", Time: base, ContextJSON: `{"time":"t0"}`, DecisionJSON: `[{"symbol":"BTCUSDT","action":"hold"}]`},
	}
	if err := bs.AppendDecisionLogs(logs); err != nil {
		t.Fatal(err)
	}
	gotLogs, err := bs.GetDecisionLogs("run-1", 10)
	if err != nil || len(gotLogs) != 1 {
		t.Fatalf("logs = %+v err=%v", gotLogs, err)
	}

	// delete run cleans up everything
	if err := bs.DeleteRun("user-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := bs.GetRun("user-1", "run-1"); err == nil {
		t.Fatal("run should be deleted")
	}
	if n, _ := bs.CountEquityPoints("run-1"); n != 0 {
		t.Fatalf("equity points should be deleted, got %d", n)
	}
}

// TestBacktestStoreRetention verifies the retention listing + cleanup.
func TestBacktestStoreRetention(t *testing.T) {
	bs := newTestBacktestStore(t)
	old := time.Now().Add(-40 * 24 * time.Hour)
	new := time.Now().Add(-1 * 24 * time.Hour)
	runs := []*BacktestRun{
		{ID: "old", UserID: "u1", Status: "completed", EndedAt: old, Symbols: "[]"},
		{ID: "recent", UserID: "u1", Status: "completed", EndedAt: new, Symbols: "[]"},
		{ID: "active", UserID: "u1", Status: "running", Symbols: "[]"},
	}
	for _, r := range runs {
		if err := bs.CreateRun(r); err != nil {
			t.Fatal(err)
		}
	}
	retained, err := bs.ListRetainedRuns(30)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != 1 || retained[0].ID != "old" {
		t.Fatalf("retained = %+v", retained)
	}
	if err := bs.DeleteRunData("old"); err != nil {
		t.Fatal(err)
	}
	if _, err := bs.GetRunAnyUser("old"); err == nil {
		t.Fatal("old run should be removed")
	}
	if _, err := bs.GetRunAnyUser("recent"); err != nil {
		t.Fatal("recent run should remain")
	}
}

// TestBacktestStorePGTableNames ensures the table names don't collide
// with reserved words (the Postgres build only checks naming).
func TestBacktestStorePGTableNames(t *testing.T) {
	if (BacktestRun{}).TableName() != "backtest_runs" {
		t.Fatal("run table name mismatch")
	}
	if (BacktestEquityPoint{}).TableName() != "backtest_equity_points" {
		t.Fatal("equity table name mismatch")
	}
	if (BacktestTradeEvent{}).TableName() != "backtest_trade_events" {
		t.Fatal("trade table name mismatch")
	}
	if (BacktestDecisionLog{}).TableName() != "backtest_decision_logs" {
		t.Fatal("decision table name mismatch")
	}
}
