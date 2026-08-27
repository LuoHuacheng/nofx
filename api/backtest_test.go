package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nofx/auth"
	"nofx/backtest"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newBacktestTestServer builds a Server over an in-memory store with a
// fixed feed and a fake-AI decider, plus one strategy and one model row
// for the start request.
func newBacktestTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:api-bt-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&store.BacktestRun{}, &store.BacktestEquityPoint{},
		&store.BacktestTradeEvent{}, &store.BacktestDecisionLog{},
		&store.Strategy{}, &store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}

	// Strategy row with a valid config JSON.
	def := store.GetDefaultStrategyConfig("en")
	def.CoinSource.SourceType = "static"
	def.CoinSource.StaticCoins = []string{"BTCUSDT"}
	def.RiskControl.BTCETHMaxLeverage = 3
	def.RiskControl.AltcoinMaxLeverage = 2
	cfgJSON, _ := json.Marshal(def)
	if err := st.Strategy().Create(&store.Strategy{
		ID: "s1", UserID: "u1", Name: "test strategy", IsActive: true, Config: string(cfgJSON),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AIModel().Create("u1", "m1", "deepseek", "deepseek", true, "", ""); err != nil {
		t.Fatal(err)
	}

	// Manager with a fixed feed + fake AI decider.
	prices := []float64{100, 101, 102, 103, 104, 105}
	m := backtest.NewRunManager(st, func(ctx context.Context, cfg backtest.RunConfig) (backtest.Feed, error) {
		return mkFeedForAPITest(t, prices, cfg), nil
	})
	ai := &backtest.FakeAIClient{Response: `<reasoning>ok</reasoning><decision>[{"symbol":"BTCUSDT","action":"hold"}]</decision>`}
	m.SetDeciderBuilder(func(cfg backtest.RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *backtest.AICache) backtest.Decider {
		runner, err := backtest.NewKernelRunner(cfg, strCfg, ai, cache)
		if err != nil {
			t.Fatal(err)
		}
		return runner
	})

	gin.SetMode(gin.TestMode)
	s := &Server{router: gin.New(), store: st, backtestManager: m}
	s.setupRoutes()
	return s, st
}

func mkFeedForAPITest(t *testing.T, prices []float64, cfg backtest.RunConfig) backtest.Feed {
	t.Helper()
	tf, err := backtest.ParseTimeframe(cfg.Timeframe)
	if err != nil {
		t.Fatal(err)
	}
	var bars []backtest.Bar
	for i, p := range prices {
		bars = append(bars, backtest.Bar{
			Time: cfg.StartTime.Add(time.Duration(5*i+5) * time.Minute),
			Open: p - 1, High: p + 1, Low: p - 2, Close: p, Volume: 100,
		})
	}
	end := cfg.StartTime.Add(time.Duration(5*len(prices)+5) * time.Minute)
	klines := make([]market.Kline, len(bars))
	for i, b := range bars {
		klines[i] = market.Kline{
			OpenTime: b.Time.Add(-tf).UnixMilli(),
			Open:     b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume,
			CloseTime: b.Time.UnixMilli(),
		}
	}
	return backtest.NewMemoryFeedFromKlines(cfg.Symbols, map[string][]market.Kline{"BTCUSDT": klines}, tf, end)
}

func authedReq(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		buf.Write(b)
	}
	req := httptest.NewRequest(method, path, &buf)
	token, err := auth.GenerateJWT("u1", "test@nofx.local")
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestBacktestAPIFlow runs start -> status -> metrics -> trades ->
// equity -> export -> delete against the real handlers.
func TestBacktestAPIFlow(t *testing.T) {
	s, st := newBacktestTestServer(t)

	// 1. Start
	body := map[string]any{
		"name":            "api test",
		"symbols":         []string{"BTCUSDT"},
		"timeframe":       "5m",
		"start_time":      "2026-01-01T00:00:00Z",
		"end_time":        "2026-01-01T01:00:00Z",
		"initial_balance": 1000,
		"strategy_id":     "s1",
		"model_id":        "m1",
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, authedReq(t, http.MethodPost, "/api/backtest/start", body))
	if w.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", w.Code, w.Body.String())
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil || started.RunID == "" {
		t.Fatalf("start response = %s", w.Body.String())
	}
	runID := started.RunID

	// 2. Poll status until completed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		w = httptest.NewRecorder()
		s.router.ServeHTTP(w, authedReq(t, http.MethodGet, "/api/backtest/status?run_id="+runID, nil))
		if w.Code == http.StatusOK {
			var sts struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &sts)
			if sts.Status == backtest.StatusCompleted {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The run must be completed in the store.
	run, err := st.Backtest().GetRun("u1", runID)
	if err != nil || run.Status != backtest.StatusCompleted {
		t.Fatalf("run not completed: %+v err=%v", run, err)
	}

	// 3. metrics
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, authedReq(t, http.MethodGet, "/api/backtest/metrics?run_id="+runID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", w.Code)
	}
	var metrics map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &metrics)
	if metrics["status"] != backtest.StatusCompleted {
		t.Fatalf("metrics = %s", w.Body.String())
	}

	// 4. trades + equity + trace + decisions
	for _, path := range []string{
		"/api/backtest/trades?run_id=" + runID,
		"/api/backtest/equity?run_id=" + runID + "&limit=50",
		"/api/backtest/trace?run_id=" + runID,
		"/api/backtest/decisions?run_id=" + runID,
	} {
		w = httptest.NewRecorder()
		s.router.ServeHTTP(w, authedReq(t, http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, w.Code, w.Body.String())
		}
	}

	// 5. runs list
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, authedReq(t, http.MethodGet, "/api/backtest/runs?page=1&page_size=20", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("runs status = %d", w.Code)
	}
	var list struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list.Total < 1 {
		t.Fatalf("runs = %s", w.Body.String())
	}

	// 6. export (JSON)
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, authedReq(t, http.MethodGet, "/api/backtest/export?run_id="+runID+"&format=json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d", w.Code)
	}
	// 7. export (CSV)
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, authedReq(t, http.MethodGet, "/api/backtest/export?run_id="+runID+"&format=csv", nil))
	if w.Code != http.StatusOK || len(w.Body.String()) == 0 {
		t.Fatalf("csv export status = %d body=%s", w.Code, w.Body.String())
	}

	// 8. delete
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, authedReq(t, http.MethodPost, "/api/backtest/delete", map[string]any{"run_id": runID}))
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", w.Code, w.Body.String())
	}
	if _, err := st.Backtest().GetRun("u1", runID); err == nil {
		t.Fatal("run should be deleted")
	}
}

// TestBacktestAPIRoutesRequireAuth verifies the protected routes reject
// unauthenticated requests.
func TestBacktestAPIRoutesRequireAuth(t *testing.T) {
	s, _ := newBacktestTestServer(t)
	cases := []struct{ method, path string }{
		{http.MethodPost, "/api/backtest/start"},
		{http.MethodGet, "/api/backtest/status?run_id=x"},
		{http.MethodGet, "/api/backtest/runs"},
		{http.MethodGet, "/api/backtest/klines?symbol=BTCUSDT"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated status = %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}
