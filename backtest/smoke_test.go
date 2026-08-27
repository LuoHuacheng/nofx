//go:build smoke
// +build smoke

package backtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"nofx/market"
	"nofx/mcp"
	"nofx/store"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestLiveDataSmoke loads REAL BTCUSDT 1h klines via market.GetKlinesRange
// (the production feed path), runs the kernel runner with a fake AI, and
// verifies a completed run with persisted equity.
func TestLiveDataSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("network smoke test")
	}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:smoke-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&store.BacktestRun{}, &store.BacktestEquityPoint{}, &store.BacktestTradeEvent{}, &store.BacktestDecisionLog{}); err != nil {
		t.Fatal(err)
	}
	st, _ := store.NewFromGorm(db)

	feedBuilder := func(ctx context.Context, cfg RunConfig) (Feed, error) {
		tf, _ := ParseTimeframe(cfg.Timeframe)
		ks, err := market.GetKlinesRange("BTCUSDT", cfg.Timeframe, cfg.StartTime, cfg.EndTime)
		if err != nil {
			return nil, err
		}
		return NewMemoryFeedFromKlines(cfg.Symbols, map[string][]market.Kline{"BTCUSDT": ks}, tf, cfg.EndTime), nil
	}
	m := NewRunManager(st, feedBuilder)
	ai := &FakeAIClient{Response: fakeAIResponse}
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		runner, err := NewKernelRunner(cfg, strCfg, ai, cache)
		if err != nil {
			t.Fatal(err)
		}
		return runner
	})

	def := store.GetDefaultStrategyConfig("en")
	def.CoinSource.SourceType = "static"
	def.CoinSource.StaticCoins = []string{"BTCUSDT"}
	end := time.Now().UTC().Truncate(time.Hour)
	runID, err := m.Start(StartRequest{
		UserID: "smoke", Name: "live-smoke", Symbols: []string{"BTCUSDT"}, Timeframe: "1h",
		StartTime: end.Add(-48 * time.Hour), EndTime: end,
		InitialBalance: 1000, ExchangeType: "binance",
		StrategyConfig: &def,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		rec, _ := m.Status("smoke", runID)
		if rec != nil && (rec.Status == StatusCompleted || rec.Status == StatusFailed) {
			t.Logf("final status: %s (err=%q) ai calls=%d", rec.Status, rec.Err, ai.Calls)
			if rec.Status != StatusCompleted {
				t.Fatalf("run failed: %s", rec.Err)
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	run, err := st.Backtest().GetRun("smoke", runID)
	if err != nil {
		t.Fatal(err)
	}
	pts, _ := st.Backtest().GetEquityPoints(runID, nil, nil, 100)
	trades, _ := st.Backtest().GetTrades(runID, nil, nil, 100)
	t.Logf("run: %s status=%s return=%.2f%% trades=%d equityPoints=%d", run.ID, run.Status, run.TotalReturnPct, len(trades), len(pts))
	if len(pts) == 0 {
		t.Fatal("no equity points persisted")
	}
}
