package backtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"nofx/mcp"
	"nofx/store"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newTestManager builds a RunManager over an in-memory SQLite store with
// a fixed feed and the kernel-runner decider backed by a fake AI client.
func newTestManager(t *testing.T, prices []float64, ai *ProbeAIClient) (*RunManager, *store.Store) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:btest-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&store.BacktestRun{}, &store.BacktestEquityPoint{},
		&store.BacktestTradeEvent{}, &store.BacktestDecisionLog{},
	); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}

	feed := func(ctx context.Context, cfg RunConfig) (Feed, error) {
		return mkRunnerTestFeed(t, prices, cfg.Timeframe, cfg.StartTime), nil
	}
	m := NewRunManager(st, feed)
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		runner, err := NewKernelRunner(cfg, strCfg, ai, cache)
		if err != nil {
			t.Fatal(err)
		}
		return runner
	})
	return m, st
}

func waitStatus(t *testing.T, m *RunManager, userID, runID string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rec, err := m.Status(userID, runID)
		if err == nil && rec.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, err := m.Status(userID, runID)
	t.Fatalf("run %s never reached %s (last: %+v err=%v)", runID, want, rec, err)
}

func defaultStartReq() StartRequest {
	def := store.GetDefaultStrategyConfig("en")
	def.CoinSource.SourceType = "static"
	def.CoinSource.StaticCoins = []string{"BTCUSDT"}
	def.RiskControl.BTCETHMaxLeverage = 3
	def.RiskControl.AltcoinMaxLeverage = 2
	return StartRequest{
		UserID:         "u1",
		Name:           "mgmt",
		Symbols:        []string{"BTCUSDT"},
		Timeframe:      "5m",
		StartTime:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:        time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC),
		InitialBalance: 1000,
		ExchangeType:   "binance",
		StrategyConfig: &def,
	}
}

func TestRunManagerLifecycle(t *testing.T) {
	prices := []float64{100, 101, 102, 103, 104}
	ai := &ProbeAIClient{Response: fakeAIResponse}
	m, st := newTestManager(t, prices, ai)

	req := defaultStartReq()
	req.StrategyConfigJSON = `{"strategy_type":"ai_trading"}`
	req.ModelConfigJSON = `{"provider":"deepseek","model":"deepseek-chat"}`

	runID, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, m, "u1", runID, StatusCompleted, 10*time.Second)

	run, err := st.Backtest().GetRun("u1", runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != StatusCompleted {
		t.Fatalf("status = %s", run.Status)
	}
	pts, err := st.Backtest().GetEquityPoints(runID, nil, nil, 100)
	if err != nil || len(pts) < 2 {
		t.Fatalf("equity points = %d err=%v", len(pts), err)
	}
	logs, err := st.Backtest().GetDecisionLogs(runID, 10)
	if err != nil || len(logs) < 2 {
		t.Fatalf("decision logs = %d err=%v", len(logs), err)
	}
	if logs[0].ContextJSON == "" || logs[0].DecisionJSON == "" {
		t.Fatal("decision log payloads empty")
	}
	// A completed run's run-lock must be released.
	if m.lock.Held(runID) {
		t.Fatal("manager should have released the run lock")
	}
	// Per-user running count back to zero.
	m.mu.Lock()
	n := m.runningByUser["u1"]
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("running count = %d, want 0", n)
	}
}

func TestRunManagerConcurrencyLimit(t *testing.T) {
	block := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(block) }) }
	defer release()

	m, _ := newTestManager(t, []float64{100}, &ProbeAIClient{Response: fakeAIResponse})
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		return DeciderFunc(func(dc *DecisionContext) []Decision {
			<-block // hold the run open
			return nil
		})
	})

	req := defaultStartReq()
	id1, err := m.Start(req)
	if err != nil {
		t.Fatalf("start 1: %v", err)
	}
	id2, err := m.Start(req)
	if err != nil {
		t.Fatalf("start 2: %v", err)
	}
	if _, err := m.Start(req); err == nil {
		t.Fatal("third concurrent run should be rejected")
	}
	release()
	waitStatus(t, m, "u1", id1, StatusCompleted, 5*time.Second)
	waitStatus(t, m, "u1", id2, StatusCompleted, 5*time.Second)
	// After completion a new run is allowed again.
	if _, err := m.Start(req); err != nil {
		t.Fatalf("start after release: %v", err)
	}
}

func TestRunManagerTimeout(t *testing.T) {
	m, _ := newTestManager(t, []float64{100, 100, 100}, &ProbeAIClient{Response: fakeAIResponse})
	m.timeout = 50 * time.Millisecond
	// Decider sleeps longer than the timeout so the engine can't finish.
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		return DeciderFunc(func(dc *DecisionContext) []Decision {
			time.Sleep(200 * time.Millisecond)
			return nil
		})
	})
	req := defaultStartReq()
	runID, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "u1", runID, StatusFailed, 10*time.Second)
	rec, _ := m.Status("u1", runID)
	if rec.Err == "" {
		t.Fatalf("expected timeout error, got %+v", rec)
	}
}

func TestRunManagerPanicRecovery(t *testing.T) {
	m, _ := newTestManager(t, []float64{100}, &ProbeAIClient{Response: fakeAIResponse})
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		return DeciderFunc(func(dc *DecisionContext) []Decision {
			panic("boom")
		})
	})
	req := defaultStartReq()
	runID, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "u1", runID, StatusFailed, 5*time.Second)
	rec, _ := m.Status("u1", runID)
	if len(rec.Err) == 0 {
		t.Fatalf("expected panic error, got %+v", rec)
	}
}

func TestRunManagerStopAndDelete(t *testing.T) {
	block := make(chan struct{})
	// Many bars so the cancel is observed between decision cycles.
	prices := make([]float64, 100)
	for i := range prices {
		prices[i] = 100
	}
	m, st := newTestManager(t, prices, &ProbeAIClient{Response: fakeAIResponse})
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		return DeciderFunc(func(dc *DecisionContext) []Decision {
			select {
			case <-block:
			case <-time.After(50 * time.Millisecond):
			}
			return nil
		})
	})
	req := defaultStartReq()
	runID, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the run is actually running (registry active).
	waitStatus(t, m, "u1", runID, StatusRunning, 5*time.Second)

	if err := m.Stop("u1", runID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitStatus(t, m, "u1", runID, StatusCancelled, 5*time.Second)

	// Delete cleans up rows.
	if err := m.Delete("u1", runID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Backtest().GetRun("u1", runID); err == nil {
		t.Fatal("run should be deleted")
	}
	close(block)
}

func TestRunManagerPauseResume(t *testing.T) {
	block := make(chan struct{})
	prices := make([]float64, 15)
	for i := range prices {
		prices[i] = 100
	}
	m, st := newTestManager(t, prices, &ProbeAIClient{Response: fakeAIResponse})
	m.SetDeciderBuilder(func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
		return DeciderFunc(func(dc *DecisionContext) []Decision {
			select {
			case <-block:
			case <-time.After(50 * time.Millisecond):
			}
			return nil
		})
	})
	req := defaultStartReq()
	req.StrategyConfigJSON = `{"strategy_type":"ai_trading","risk_control":{"max_positions":5}}`
	req.ModelConfigJSON = `{"provider":"deepseek","model":"deepseek-chat"}`
	runID, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "u1", runID, StatusRunning, 5*time.Second)
	if err := m.Pause("u1", runID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitStatus(t, m, "u1", runID, StatusPaused, 5*time.Second)

	// Resume restarts execution and produces a fresh completed run.
	if err := m.Resume("u1", runID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// The resumed run replaces the paused one; the old row is removed.
	waitStatusCompletedOther(t, m, "u1", runID, 10*time.Second)
	if _, err := st.Backtest().GetRun("u1", runID); err == nil {
		t.Fatal("paused run should be removed after resume")
	}
	close(block)
}

// waitStatusCompletedOther waits until the user has a completed run that
// is not the given paused id.
func waitStatusCompletedOther(t *testing.T, m *RunManager, userID, excludeID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runs, _, err := m.List(userID, 1, 50, "all")
		if err == nil {
			for _, run := range runs {
				if run.ID != excludeID && run.Status == StatusCompleted {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no completed run appeared besides %s", excludeID)
}

func TestAICache(t *testing.T) {
	c := NewAICache(2)
	key1 := CacheKey("u1", map[string]float64{"BTCUSDT": 100}, nil, 1000, "d1")
	if c.Get(key1) != nil {
		t.Fatal("empty cache should miss")
	}
	c.Set(key1, []byte("dec1"))
	if string(c.Get(key1)) != "dec1" {
		t.Fatal("cache should hit after set")
	}
	c.Set(CacheKey("u1", map[string]float64{"BTCUSDT": 101}, nil, 1000, "d2"), []byte("dec2"))
	c.Set(CacheKey("u1", map[string]float64{"BTCUSDT": 102}, nil, 1000, "d3"), []byte("dec3"))
	if c.Size() > 2 {
		t.Fatalf("cache size = %d, want <= 2", c.Size())
	}
}
