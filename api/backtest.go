package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nofx/backtest"
	"nofx/logger"
	"nofx/market"
	"nofx/store"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// Backtest API (P3.4). All routes live under /api/backtest/* and require
// authentication. The RunManager runs backtests in-process with the
// guardrails of design §4 (per-user cap, 30-min timeout, retention).
// ============================================================================

// backtestStartRequest is the body of POST /api/backtest/start.
type backtestStartRequest struct {
	Name           string   `json:"name"`
	Symbols        []string `json:"symbols"`
	Timeframe      string   `json:"timeframe"` // 1m..1d
	StartTime      string   `json:"start_time"`
	EndTime        string   `json:"end_time"`
	InitialBalance float64  `json:"initial_balance"`
	StrategyID     string   `json:"strategy_id"`
	ModelID        string   `json:"model_id"`
	ExchangeID     string   `json:"exchange_id"` // optional: fee defaults by exchange type
	FeeMaker       float64  `json:"fee_maker"`
	FeeTaker       float64  `json:"fee_taker"`
}

// handleBacktestStart starts a backtest run.
func (s *Server) handleBacktestStart(c *gin.Context) {
	userID := c.GetString("user_id")
	var req backtestStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	if len(req.Symbols) == 0 {
		SafeBadRequest(c, "symbols is required")
		return
	}
	if req.Timeframe == "" {
		req.Timeframe = "5m"
	}
	if _, err := backtest.ParseTimeframe(req.Timeframe); err != nil {
		SafeBadRequest(c, "unsupported timeframe")
		return
	}
	start, err1 := time.Parse(time.RFC3339, req.StartTime)
	end, err2 := time.Parse(time.RFC3339, req.EndTime)
	if err1 != nil || err2 != nil {
		SafeBadRequest(c, "start_time/end_time must be RFC3339 (e.g. 2026-01-01T00:00:00Z)")
		return
	}
	if req.InitialBalance <= 0 {
		req.InitialBalance = 1000
	}

	// Strategy config from the user's strategy (snapshot for reproducibility).
	if req.StrategyID == "" {
		SafeBadRequest(c, "strategy_id is required")
		return
	}
	strategy, err := s.store.Strategy().Get(userID, req.StrategyID)
	if err != nil {
		SafeBadRequest(c, "strategy not found")
		return
	}
	strCfg, err := strategy.ParseConfig()
	if err != nil {
		SafeInternalError(c, "Failed to parse strategy config", err)
		return
	}
	strategyJSON, err := json.Marshal(strCfg)
	if err != nil {
		SafeInternalError(c, "Failed to serialize strategy config", err)
		return
	}

	// Model config from the user's AI model (API key is resolved here so
	// the run is reproducible; the snapshot is stored with the run).
	if req.ModelID == "" {
		SafeBadRequest(c, "model_id is required")
		return
	}
	model, err := s.store.AIModel().Get(userID, req.ModelID)
	if err != nil {
		SafeBadRequest(c, "model not found")
		return
	}
	modelSnap := backtest.ModelConfigSnapshot{
		Provider:        model.Provider,
		Model:           model.CustomModelName,
		APIKey:          string(model.APIKey),
		CustomAPIURL:    model.CustomAPIURL,
		CustomModelName: model.CustomModelName,
	}
	modelJSON, err := backtest.MarshalModelSnapshot(modelSnap)
	if err != nil {
		SafeInternalError(c, "Failed to serialize model config", err)
		return
	}

	// Exchange type (optional) feeds the fee defaults.
	exchangeType := ""
	if req.ExchangeID != "" {
		if ex, err := s.store.Exchange().GetByID(userID, req.ExchangeID); err == nil {
			exchangeType = ex.ExchangeType
		}
	}

	runID, err := s.backtestManager.Start(backtest.StartRequest{
		UserID:             userID,
		Name:               req.Name,
		Symbols:            req.Symbols,
		Timeframe:          req.Timeframe,
		StartTime:          start,
		EndTime:            end,
		InitialBalance:     req.InitialBalance,
		ExchangeType:       exchangeType,
		FeeMaker:           req.FeeMaker,
		FeeTaker:           req.FeeTaker,
		StrategyConfig:     strCfg,
		StrategyConfigJSON: string(strategyJSON),
		ModelSnapshot:      modelSnap,
		ModelConfigJSON:    modelJSON,
	})
	if err != nil {
		// The per-user concurrency cap surfaces as 429.
		if strings.Contains(err.Error(), "concurrent") {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return
		}
		SafeBadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": runID, "status": backtest.StatusRunning})
}

// handleBacktestStatus returns the current status of a run.
func (s *Server) handleBacktestStatus(c *gin.Context) {
	userID := c.GetString("user_id")
	runID := c.Query("run_id")
	if runID == "" {
		SafeBadRequest(c, "run_id is required")
		return
	}
	rec, err := s.backtestManager.Status(userID, runID)
	if err != nil {
		SafeBadRequest(c, "run not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"run_id":     rec.ID,
		"status":     rec.Status,
		"error":      rec.Err,
		"started_at": rec.StartedAt,
		"ended_at":   rec.EndedAt,
	})
}

// handleBacktestRuns lists runs with pagination + status filter.
func (s *Server) handleBacktestRuns(c *gin.Context) {
	userID := c.GetString("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	status := c.Query("status")

	runs, total, err := s.backtestManager.List(userID, page, pageSize, status)
	if err != nil {
		SafeInternalError(c, "Failed to list runs", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs, "total": total})
}

// handleBacktestMetrics returns the final metrics of a run.
func (s *Server) handleBacktestMetrics(c *gin.Context) {
	userID := c.GetString("user_id")
	runID := c.Query("run_id")
	run, err := s.store.Backtest().GetRun(userID, runID)
	if err != nil {
		SafeBadRequest(c, "run not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"run_id":           run.ID,
		"name":             run.Name,
		"status":           run.Status,
		"symbols":          mustUnmarshalSymbols(run.Symbols),
		"timeframe":        run.Timeframe,
		"start_time":       run.StartTime,
		"end_time":         run.EndTime,
		"initial_balance":  run.InitialBalance,
		"final_balance":    run.FinalBalance,
		"total_return_pct": run.TotalReturnPct,
		"max_drawdown_pct": run.MaxDrawdownPct,
		"sharpe_ratio":     run.SharpeRatio,
		"win_rate":         run.WinRate,
		"profit_factor":    run.ProfitFactor,
		"trade_count":      run.TradeCount,
		"error":            run.Error,
	})
}

// handleBacktestEquity returns equity-curve samples.
func (s *Server) handleBacktestEquity(c *gin.Context) {
	userID := c.GetString("user_id")
	runID := c.Query("run_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	from, to := parseTimeRange(c)

	pts, err := s.store.Backtest().GetEquityPoints(runID, from, to, limit)
	if err != nil || !runBelongsTo(s, userID, runID) {
		if err != nil {
			SafeInternalError(c, "Failed to get equity points", err)
			return
		}
		SafeBadRequest(c, "run not found")
		return
	}
	c.JSON(http.StatusOK, pts)
}

// handleBacktestTrades returns filled trades.
func (s *Server) handleBacktestTrades(c *gin.Context) {
	userID := c.GetString("user_id")
	runID := c.Query("run_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	from, to := parseTimeRange(c)

	trades, err := s.store.Backtest().GetTrades(runID, from, to, limit)
	if err != nil || !runBelongsTo(s, userID, runID) {
		if err != nil {
			SafeInternalError(c, "Failed to get trades", err)
			return
		}
		SafeBadRequest(c, "run not found")
		return
	}
	c.JSON(http.StatusOK, trades)
}

// handleBacktestTrace returns the in-memory trace buffer (newest first).
func (s *Server) handleBacktestTrace(c *gin.Context) {
	userID := c.GetString("user_id")
	runID := c.Query("run_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if !runBelongsTo(s, userID, runID) {
		SafeBadRequest(c, "run not found")
		return
	}
	trace := s.backtestManager.Trace(runID, limit)
	if trace == nil {
		trace = []backtest.TraceEntry{}
	}
	c.JSON(http.StatusOK, trace)
}

// handleBacktestDecisions returns decision snapshots (newest first).
func (s *Server) handleBacktestDecisions(c *gin.Context) {
	userID := c.GetString("user_id")
	runID := c.Query("run_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if !runBelongsTo(s, userID, runID) {
		SafeBadRequest(c, "run not found")
		return
	}
	logs, err := s.store.Backtest().GetDecisionLogs(runID, limit)
	if err != nil {
		SafeInternalError(c, "Failed to get decision logs", err)
		return
	}
	c.JSON(http.StatusOK, logs)
}

// handleBacktestExport exports trades (and equity for JSON) of a run.
// Below 10k trades the payload is inline; above it a file is written
// under BACKTEST_EXPORT_DIR (default ./data/backtest-exports) and a
// download URL is returned.
func (s *Server) handleBacktestExport(c *gin.Context) {
	userID := c.GetString("user_id")
	runID := c.Query("run_id")
	format := c.DefaultQuery("format", "json")
	if format != "csv" && format != "json" {
		SafeBadRequest(c, "format must be csv or json")
		return
	}
	run, err := s.store.Backtest().GetRun(userID, runID)
	if err != nil {
		SafeBadRequest(c, "run not found")
		return
	}

	count, err := s.store.Backtest().CountTrades(runID)
	if err != nil {
		SafeInternalError(c, "Failed to count trades", err)
		return
	}
	const inlineLimit = 10000
	if count <= inlineLimit {
		// Inline payload.
		trades, err := s.store.Backtest().GetTrades(runID, nil, nil, 10000)
		if err != nil {
			SafeInternalError(c, "Failed to get trades", err)
			return
		}
		if format == "csv" {
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=backtest-%s.csv", runID[:8]))
			c.Header("Content-Type", "text/csv")
			c.String(http.StatusOK, tradesCSV(run, trades))
			return
		}
		equity, _ := s.store.Backtest().GetEquityPoints(runID, nil, nil, 5000)
		c.JSON(http.StatusOK, gin.H{"run": run, "trades": trades, "equity": equity})
		return
	}

	// Large export: write a file and return a download link.
	dir := os.Getenv("BACKTEST_EXPORT_DIR")
	if dir == "" {
		dir = filepath.Join("data", "backtest-exports")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		SafeInternalError(c, "Failed to create export dir", err)
		return
	}
	filename := fmt.Sprintf("backtest-%s.%s", runID[:8], format)
	path := filepath.Join(dir, filename)
	if err := writeExportFile(path, format, run, s.store, runID); err != nil {
		SafeInternalError(c, "Failed to write export", err)
		return
	}
	logger.Infof("📤 [backtest %s] large export written to %s", runID, path)
	c.JSON(http.StatusOK, gin.H{"url": "/api/backtest/download/" + filename})
}

// handleBacktestDownload serves a previously generated export file.
func (s *Server) handleBacktestDownload(c *gin.Context) {
	filename := c.Param("filename")
	if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		SafeBadRequest(c, "invalid filename")
		return
	}
	dir := os.Getenv("BACKTEST_EXPORT_DIR")
	if dir == "" {
		dir = filepath.Join("data", "backtest-exports")
	}
	c.File(filepath.Join(dir, filename))
}

// handleBacktestKlines returns historical klines for the backtest data
// picker (reuses market.GetKlinesRange — the REST fallback channel).
func (s *Server) handleBacktestKlines(c *gin.Context) {
	symbol := c.Query("symbol")
	if symbol == "" {
		SafeBadRequest(c, "symbol is required")
		return
	}
	tf := c.DefaultQuery("timeframe", "5m")
	from, to := parseTimeRange(c)
	if from == nil || to == nil {
		SafeBadRequest(c, "from/to are required (RFC3339)")
		return
	}
	klines, err := market.GetKlinesRange(symbol, tf, *from, *to)
	if err != nil {
		SafeInternalError(c, "Failed to get klines", err)
		return
	}
	c.JSON(http.StatusOK, klines)
}

// handleBacktestPause / Resume / Stop / Delete — lifecycle verbs.
func (s *Server) handleBacktestPause(c *gin.Context) {
	s.backtestLifecycle(c, "pause")
}
func (s *Server) handleBacktestResume(c *gin.Context) {
	s.backtestLifecycle(c, "resume")
}
func (s *Server) handleBacktestStop(c *gin.Context) {
	s.backtestLifecycle(c, "stop")
}
func (s *Server) handleBacktestDelete(c *gin.Context) {
	s.backtestLifecycle(c, "delete")
}

func (s *Server) backtestLifecycle(c *gin.Context, action string) {
	userID := c.GetString("user_id")
	var req struct {
		RunID string `json:"run_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.RunID == "" {
		SafeBadRequest(c, "run_id is required in the request body")
		return
	}
	var err error
	switch action {
	case "pause":
		err = s.backtestManager.Pause(userID, req.RunID)
	case "resume":
		err = s.backtestManager.Resume(userID, req.RunID)
	case "stop":
		err = s.backtestManager.Stop(userID, req.RunID)
	case "delete":
		err = s.backtestManager.Delete(userID, req.RunID)
	}
	if err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": req.RunID, "action": action, "status": "ok"})
}

// ============================================================================
// Helpers
// ============================================================================

func runBelongsTo(s *Server, userID, runID string) bool {
	_, err := s.store.Backtest().GetRun(userID, runID)
	return err == nil
}

func parseTimeRange(c *gin.Context) (from, to *time.Time) {
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = &t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = &t
		}
	}
	return
}

func mustUnmarshalSymbols(s string) []string {
	out, err := store.UnmarshalSymbols(s)
	if err != nil {
		return []string{}
	}
	return out
}

func tradesCSV(run *store.BacktestRun, trades []store.BacktestTradeEvent) string {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write([]string{"time", "symbol", "side", "position", "price", "size", "fee", "pnl"})
	for _, t := range trades {
		_ = w.Write([]string{
			t.Time.Format(time.RFC3339), t.Symbol, t.Side, t.Position,
			strconv.FormatFloat(t.Price, 'f', 6, 64),
			strconv.FormatFloat(t.Size, 'f', 8, 64),
			strconv.FormatFloat(t.Fee, 'f', 6, 64),
			strconv.FormatFloat(t.PnL, 'f', 6, 64),
		})
	}
	w.Flush()
	return sb.String()
}

func writeExportFile(path, format string, run *store.BacktestRun, st *store.Store, runID string) error {
	trades, err := st.Backtest().GetTrades(runID, nil, nil, 100000)
	if err != nil {
		return err
	}
	if format == "csv" {
		return os.WriteFile(path, []byte(tradesCSV(run, trades)), 0o644)
	}
	equity, _ := st.Backtest().GetEquityPoints(runID, nil, nil, 5000)
	b, err := json.Marshal(gin.H{"run": run, "trades": trades, "equity": equity})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// marketBacktestFeed is the production FeedBuilder: it loads historical
// klines for every run symbol via market.GetKlinesRange (the existing
// REST fallback channel, §2/§7 — no new paid data source). Historical
// funding rates are not available through the current market channel,
// so the feed carries none; the engine settles funding at 0 and marks
// funding_missing traces (§2.2).
func marketBacktestFeed(ctx context.Context, cfg backtest.RunConfig) (backtest.Feed, error) {
	tf, err := backtest.ParseTimeframe(cfg.Timeframe)
	if err != nil {
		return nil, err
	}
	klinesBySymbol := make(map[string][]market.Kline, len(cfg.Symbols))
	for _, sym := range cfg.Symbols {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		ks, err := market.GetKlinesRange(sym, cfg.Timeframe, cfg.StartTime, cfg.EndTime)
		if err != nil {
			return nil, fmt.Errorf("load klines for %s: %w", sym, err)
		}
		if len(ks) == 0 {
			return nil, fmt.Errorf("no kline data for %s in the selected range", sym)
		}
		klinesBySymbol[sym] = ks
	}
	return backtest.NewMemoryFeedFromKlines(cfg.Symbols, klinesBySymbol, tf, cfg.EndTime), nil
}
