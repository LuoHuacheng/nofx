package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"nofx/logger"
	"nofx/mcp"
	"nofx/store"

	"github.com/google/uuid"
)

// ErrTooManyConcurrent is returned by Start when the user already has
// MaxRunningPerUser runs executing; the API maps it to HTTP 429.
var ErrTooManyConcurrent = errors.New("too many concurrent backtests")

// StartRequest is the manager's input for launching a backtest run. The
// API handler (P3.4) resolves the strategy + model snapshots and passes
// them here so the run is fully reproducible.
type StartRequest struct {
	UserID             string
	Name               string
	Symbols            []string
	Timeframe          string
	StartTime          time.Time
	EndTime            time.Time
	InitialBalance     float64
	ExchangeType       string
	FeeMaker           float64 // 0 => exchange default
	FeeTaker           float64 // 0 => exchange default
	StrategyConfig     *store.StrategyConfig
	StrategyConfigJSON string
	ModelSnapshot      ModelConfigSnapshot
	ModelConfigJSON    string
}

// FeedBuilder loads the klines for a run. The production implementation
// (P3.4) reads via market.GetKlinesRange; tests inject fixed data.
type FeedBuilder func(ctx context.Context, cfg RunConfig) (Feed, error)

// DeciderBuilder builds the run decider. Defaults to the kernel adapter;
// tests inject fixed deciders.
type DeciderBuilder func(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider

// RunManager owns the lifecycle of in-process backtest runs (§4): a
// per-user running cap, a 30-minute wall-clock timeout, panic recovery
// (run -> failed) and the retention sweep. Runs execute in goroutines
// inside the HTTP process.
type RunManager struct {
	st       *store.Store
	lock     *RunLock
	registry *RunRegistry
	feed     FeedBuilder
	decider  DeciderBuilder

	mu            sync.Mutex
	runningByUser map[string]int
	cancelByRun   map[string]context.CancelFunc
	runCancel     map[string]context.CancelFunc // engine-level cancel (stop/pause/timeout)
	pauseByRun    map[string]bool

	traceMu sync.Mutex
	traces  map[string][]TraceEntry // in-memory trace buffer (diagnostics only)

	retentionStop chan struct{}
	started       bool
	timeout       time.Duration
}

// NewRunManager creates a manager. feed is the kline loader; when nil a
// builder that always errors is used (the API wires the real one).
func NewRunManager(st *store.Store, feed FeedBuilder) *RunManager {
	if st == nil {
		st = &store.Store{} // tests may pass a store; never nil in prod
	}
	if feed == nil {
		feed = func(context.Context, RunConfig) (Feed, error) {
			return nil, fmt.Errorf("no data feed builder configured")
		}
	}
	m := &RunManager{
		st:            st,
		lock:          NewRunLock(),
		registry:      NewRunRegistry(),
		feed:          feed,
		runningByUser: make(map[string]int),
		cancelByRun:   make(map[string]context.CancelFunc),
		runCancel:     make(map[string]context.CancelFunc),
		pauseByRun:    make(map[string]bool),
		traces:        make(map[string][]TraceEntry),
		retentionStop: make(chan struct{}),
		timeout:       RunTimeout,
	}
	m.decider = DefaultDeciderBuilder
	return m
}

// DefaultDeciderBuilder is the kernel adapter used by production runs.
func DefaultDeciderBuilder(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
	runner, err := NewKernelRunner(cfg, strCfg, client, cache)
	if err != nil {
		logger.Warnf("⚠️ [backtest] kernel runner init failed: %v", err)
		return nil
	}
	return runner
}

// SetDeciderBuilder overrides the decider factory (tests).
func (m *RunManager) SetDeciderBuilder(db DeciderBuilder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decider = db
}

// Start validates the request and launches the run asynchronously.
// Returns the new run ID. Errors are returned for invalid input and
// when the per-user running cap (MaxRunningPerUser) is exceeded.
func (m *RunManager) Start(req StartRequest) (string, error) {
	if req.UserID == "" {
		return "", fmt.Errorf("user is required")
	}
	if len(req.Symbols) == 0 {
		return "", fmt.Errorf("at least one symbol is required")
	}
	if len(req.Symbols) > MaxSymbolsPerRun {
		return "", fmt.Errorf("too many symbols: %d (max %d)", len(req.Symbols), MaxSymbolsPerRun)
	}
	if req.StartTime.IsZero() || req.EndTime.IsZero() {
		return "", fmt.Errorf("start and end time are required")
	}
	if !req.EndTime.After(req.StartTime) {
		return "", fmt.Errorf("end time must be after start time")
	}
	if req.EndTime.Sub(req.StartTime) > MaxRunSpan {
		return "", fmt.Errorf("backtest span exceeds the %d-day limit", int(MaxRunSpan.Hours()/24))
	}
	if req.InitialBalance <= 0 {
		return "", fmt.Errorf("initial balance must be positive")
	}
	if _, err := ParseTimeframe(req.Timeframe); err != nil {
		return "", err
	}
	if req.StrategyConfig == nil {
		return "", fmt.Errorf("strategy config is required")
	}

	// Resolve fee fallbacks now so the RunMetadata snapshot (§2.2) records
	// exactly what the engine will charge — results stay reproducible.
	feeMaker := req.FeeMaker
	if feeMaker <= 0 {
		feeMaker = DefaultMakerFee
	}
	feeTaker := req.FeeTaker
	if feeTaker <= 0 {
		feeTaker = DefaultTakerFee(req.ExchangeType)
	}
	cfg := RunConfig{
		UserID:             req.UserID,
		Name:               req.Name,
		Symbols:            UniqueSymbols(req.Symbols),
		Timeframe:          req.Timeframe,
		StartTime:          req.StartTime,
		EndTime:            req.EndTime,
		InitialBalance:     req.InitialBalance,
		FeeMaker:           feeMaker,
		FeeTaker:           feeTaker,
		ExchangeType:       req.ExchangeType,
		StrategyConfigJSON: req.StrategyConfigJSON,
		ModelConfigJSON:    req.ModelConfigJSON,
	}
	if cfg.Name == "" {
		cfg.Name = fmt.Sprintf("backtest-%s", time.Now().Format("20060102-150405"))
	}

	// Per-user concurrency guardrail (§4): at most 2 running per user.
	m.mu.Lock()
	if m.runningByUser[req.UserID] >= MaxRunningPerUser {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: at most %d running per user", ErrTooManyConcurrent, MaxRunningPerUser)
	}
	m.runningByUser[req.UserID]++
	m.mu.Unlock()

	runID := uuid.NewString()
	if err := m.lock.TryAcquire(runID); !err {
		return "", fmt.Errorf("run %s is already active", runID)
	}
	defer m.lock.Release(runID)

	if err := m.persistStart(req, runID); err != nil {
		// persistStart failed: the goroutine never starts, so release the
		// per-user running slot here (otherwise two failures would lock
		// the user out with 429s until process restart).
		m.mu.Lock()
		m.runningByUser[req.UserID]--
		m.mu.Unlock()
		return "", err
	}

	engineCtx, engineCancel := context.WithTimeout(context.Background(), m.timeout)
	m.mu.Lock()
	m.cancelByRun[runID] = engineCancel
	m.runCancel[runID] = engineCancel
	m.pauseByRun[runID] = false
	m.mu.Unlock()

	m.registry.Add(&RunRecord{ID: runID, UserID: req.UserID, Status: StatusRunning, Config: cfg, StartedAt: time.Now()})

	go m.execute(runID, req, cfg, engineCtx, engineCancel)

	return runID, nil
}

// persistStart creates the BacktestRun row (status pending -> running
// handled by execute).
func (m *RunManager) persistStart(req StartRequest, runID string) error {
	if m.st.GormDB() == nil {
		return nil // store not available (tests)
	}
	run := &store.BacktestRun{
		ID:                 runID,
		Name:               req.Name,
		UserID:             req.UserID,
		Status:             StatusRunning,
		StrategyConfigJSON: req.StrategyConfigJSON,
		ModelConfigJSON:    req.ModelConfigJSON,
		Symbols:            store.MarshalSymbols(req.Symbols),
		StartTime:          req.StartTime,
		EndTime:            req.EndTime,
		Timeframe:          req.Timeframe,
		ExchangeType:       req.ExchangeType,
		FeeMaker:           req.FeeMaker,
		FeeTaker:           req.FeeTaker,
		InitialBalance:     req.InitialBalance,
		StartedAt:          time.Now(),
	}
	return m.st.Backtest().CreateRun(run)
}

// execute runs the backtest loop with the guards (§4): timeout, panic
// recovery, cancellation from stop/pause/delete.
func (m *RunManager) execute(runID string, req StartRequest, cfg RunConfig, engineCtx context.Context, engineCancel context.CancelFunc) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("🚨 [backtest %s] PANIC: %v\n%s", runID, r, debug.Stack())
			m.finish(runID, StatusFailed, fmt.Sprintf("panic: %v", r), nil)
		}
		m.cleanup(runID, engineCancel)
		// Release the per-user running slot once the run terminates.
		m.mu.Lock()
		m.runningByUser[cfg.UserID]--
		m.mu.Unlock()
	}()

	// Load historical klines.
	feed, err := m.feed(engineCtx, cfg)
	if err != nil {
		m.finish(runID, StatusFailed, fmt.Sprintf("data load failed: %v", err), nil)
		return
	}

	// Build the decider (kernel adapter by default). The API key is
	// resolved from the store at execution time (never persisted in the
	// run snapshot); ModelID in the snapshot points back to the record.
	// Each run gets its own AI cache so identical-looking contexts from
	// different runs (same user, different strategy/model) can never
	// share cached decisions.
	client := NewAIClientFromSnapshot(m.resolveModelSnapshot(req))
	runCache := NewAICache(2000)
	decider := m.deciderFor(cfg, req.StrategyConfig, client, runCache)
	if decider == nil {
		m.finish(runID, StatusFailed, "decider init failed", nil)
		return
	}
	// Attach the decision persistence hook when the decider is the kernel runner.
	if runner, ok := decider.(*KernelRunner); ok {
		// Decision persistence hook (batch flush every 200 entries).
		var decisionBuf []store.BacktestDecisionLog
		var decisionMu sync.Mutex
		flushDecisions := func(force bool) {
			if m.st.GormDB() == nil {
				return
			}
			decisionMu.Lock()
			defer decisionMu.Unlock()
			if len(decisionBuf) == 0 || (!force && len(decisionBuf) < 200) {
				return
			}
			if err := m.st.Backtest().AppendDecisionLogs(decisionBuf); err != nil {
				logger.Warnf("⚠️ [backtest %s] decision log append failed: %v", runID, err)
			}
			decisionBuf = decisionBuf[:0]
		}
		runner.SetDecisionHook(func(t time.Time, ctxJSON, decisionJSON string) {
			decisionMu.Lock()
			decisionBuf = append(decisionBuf, store.BacktestDecisionLog{
				RunID: runID, Time: t, ContextJSON: ctxJSON, DecisionJSON: decisionJSON,
			})
			decisionMu.Unlock()
			if len(decisionBuf) >= 200 {
				flushDecisions(false)
			}
		})
		defer flushDecisions(true)
	}

	eng, err := NewEngine(cfg, feed, decider, WithTrace(func(e TraceEntry) {
		m.appendTrace(runID, e)
	}))
	if err != nil {
		m.finish(runID, StatusFailed, fmt.Sprintf("engine init failed: %v", err), nil)
		return
	}

	m.registry.Update(runID, func(rec *RunRecord) { rec.Status = StatusRunning })
	logger.Infof("🚀 [backtest %s] run started: %d symbols, %s, %s -> %s",
		runID, len(cfg.Symbols), cfg.Timeframe,
		cfg.StartTime.Format("2006-01-02 15:04"), cfg.EndTime.Format("2006-01-02 15:04"))

	result, err := eng.Run(engineCtx)

	if err != nil {
		m.mu.Lock()
		paused := m.pauseByRun[runID]
		m.mu.Unlock()
		if paused {
			m.finish(runID, StatusPaused, "paused by user", nil)
		} else if engineCtx.Err() == context.DeadlineExceeded {
			m.finish(runID, StatusFailed, fmt.Sprintf("run exceeded %s timeout", m.timeout), nil)
		} else if engineCtx.Err() == context.Canceled {
			m.finish(runID, StatusCancelled, "cancelled by user", nil)
		} else {
			m.finish(runID, StatusFailed, err.Error(), nil)
		}
		return
	}

	metrics := result.Metrics(cfg.InitialBalance)
	m.persistResult(runID, cfg, result, metrics)
	m.finish(runID, StatusCompleted, "", &metrics)
}

func (m *RunManager) deciderFor(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) Decider {
	m.mu.Lock()
	db := m.decider
	m.mu.Unlock()
	if db == nil {
		return nil
	}
	return db(cfg, strCfg, client, cache)
}

// persistResult writes equity, trades and metrics for a finished run.
// The equity curve is already capped at MaxEquityPoints by the engine.
func (m *RunManager) persistResult(runID string, cfg RunConfig, result *Result, metrics Metrics) {
	if m.st.GormDB() == nil {
		return
	}
	bs := m.st.Backtest()

	pts := make([]store.BacktestEquityPoint, 0, len(result.Equity))
	for _, p := range result.Equity {
		pts = append(pts, store.BacktestEquityPoint{RunID: runID, Time: p.Time, Balance: p.Balance, Equity: p.Equity})
	}
	if err := bs.AppendEquityPoints(pts); err != nil {
		logger.Warnf("⚠️ [backtest %s] equity persist failed: %v", runID, err)
	}

	trades := make([]store.BacktestTradeEvent, 0, len(result.Trades))
	for _, t := range result.Trades {
		trades = append(trades, store.BacktestTradeEvent{
			RunID: runID, Time: t.Time, Symbol: t.Symbol, Side: t.Side, Position: t.Position,
			Price: t.Price, Size: t.Size, Fee: t.Fee, PnL: t.PnL,
		})
	}
	if err := bs.AppendTrades(trades); err != nil {
		logger.Warnf("⚠️ [backtest %s] trades persist failed: %v", runID, err)
	}

	// Update run metadata with final metrics.
	run, err := bs.GetRun(cfg.UserID, runID)
	if err != nil {
		run, _ = bs.GetRunAnyUser(runID)
	}
	if run != nil {
		run.Status = StatusCompleted
		run.FinalBalance = metrics.FinalBalance
		run.TotalReturnPct = metrics.TotalReturnPct
		run.MaxDrawdownPct = metrics.MaxDrawdownPct
		run.SharpeRatio = metrics.SharpeRatio
		run.WinRate = metrics.WinRate
		run.ProfitFactor = metrics.ProfitFactor
		run.TradeCount = metrics.TradeCount
		run.EndedAt = time.Now()
		_ = bs.UpdateRun(run)
	}
}

// finish marks a run terminal in the registry and the store.
func (m *RunManager) finish(runID, status, errMsg string, metrics *Metrics) {
	m.registry.Update(runID, func(rec *RunRecord) {
		rec.Status = status
		rec.EndedAt = time.Now()
		rec.Err = errMsg
	})
	// Bound per-run trace memory once the run is terminal: keep the last
	// 1000 entries for the trace API, drop the rest.
	m.traceMu.Lock()
	if buf := m.traces[runID]; len(buf) > 1000 {
		m.traces[runID] = append(buf[:0], buf[len(buf)-1000:]...)
	}
	m.traceMu.Unlock()
	if m.st.GormDB() == nil {
		return
	}
	run, err := m.st.Backtest().GetRunAnyUser(runID)
	if err != nil {
		return
	}
	run.Status = status
	run.Error = errMsg
	run.EndedAt = time.Now()
	if metrics != nil {
		run.FinalBalance = metrics.FinalBalance
		run.TotalReturnPct = metrics.TotalReturnPct
		run.MaxDrawdownPct = metrics.MaxDrawdownPct
		run.SharpeRatio = metrics.SharpeRatio
		run.WinRate = metrics.WinRate
		run.ProfitFactor = metrics.ProfitFactor
		run.TradeCount = metrics.TradeCount
	}
	_ = m.st.Backtest().UpdateRun(run)
	if errMsg != "" {
		logger.Infof("🏁 [backtest %s] %s (%s)", runID, status, errMsg)
	} else {
		logger.Infof("🏁 [backtest %s] %s", runID, status)
	}
}

// resolveModelSnapshot fills the API key from the user's stored AI
// model when the snapshot carries a ModelID. Keys are never persisted
// in BacktestRun.ModelConfigJSON (§7: no secrets in the DB).
func (m *RunManager) resolveModelSnapshot(req StartRequest) ModelConfigSnapshot {
	snap := req.ModelSnapshot
	if snap.ModelID == "" {
		return snap
	}
	if m.st.GormDB() == nil {
		return snap
	}
	model, err := m.st.AIModel().Get(req.UserID, snap.ModelID)
	if err != nil {
		logger.Warnf("⚠️ [backtest] model %s not found at run start: %v", snap.ModelID, err)
		return snap
	}
	snap.APIKey = string(model.APIKey)
	return snap
}

// appendTrace buffers a trace line per run (capped at 5000 entries,
// oldest dropped). Traces are diagnostics only and are not persisted.
func (m *RunManager) appendTrace(runID string, e TraceEntry) {
	m.traceMu.Lock()
	defer m.traceMu.Unlock()
	buf := m.traces[runID]
	if len(buf) >= 5000 {
		buf = append(buf[:0], buf[1:]...)
	}
	m.traces[runID] = append(buf, e)
}

// Trace returns the buffered trace for a run, newest-first capped at
// limit (default 100, max 5000).
func (m *RunManager) Trace(runID string, limit int) []TraceEntry {
	if limit <= 0 || limit > 5000 {
		limit = 100
	}
	m.traceMu.Lock()
	buf := m.traces[runID]
	m.traceMu.Unlock()
	out := make([]TraceEntry, 0, len(buf))
	for i := len(buf) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, buf[i])
	}
	return out
}

func (m *RunManager) cleanup(runID string, engineCancel context.CancelFunc) {
	engineCancel()
	m.mu.Lock()
	delete(m.cancelByRun, runID)
	delete(m.runCancel, runID)
	delete(m.pauseByRun, runID)
	m.mu.Unlock()
	m.lock.Release(runID)
}

// Pause stops a running run and marks it paused (partial results are
// discarded; Resume restarts execution of the same configuration).
func (m *RunManager) Pause(userID, runID string) error {
	return m.setPause(userID, runID, true)
}

// Resume restarts a paused run from the beginning of its configuration.
func (m *RunManager) Resume(userID, runID string) error {
	if m.st.GormDB() == nil {
		return fmt.Errorf("store not available")
	}
	run, err := m.st.Backtest().GetRun(userID, runID)
	if err != nil {
		return fmt.Errorf("run not found")
	}
	if run.Status != StatusPaused {
		return fmt.Errorf("run is not paused (status: %s)", run.Status)
	}
	req := runToStartRequest(run)
	if req == nil {
		return fmt.Errorf("run config incomplete")
	}
	newID, err := m.Start(*req)
	if err != nil {
		return err
	}
	_ = m.st.Backtest().DeleteRun(userID, runID)
	logger.Infof("↻ [backtest] resumed %s as new run %s", runID, newID)
	return nil
}

// Stop cancels a running run and marks it cancelled.
func (m *RunManager) Stop(userID, runID string) error {
	m.mu.Lock()
	ec, ok := m.runCancel[runID]
	u := m.registry.Get(runID)
	m.mu.Unlock()
	if !ok {
		// Not running: mark cancelled if it is pending.
		if u != nil && u.UserID == userID && u.Status == StatusPending {
			m.finish(runID, StatusCancelled, "cancelled by user", nil)
			return nil
		}
		return fmt.Errorf("run is not running")
	}
	if u == nil || u.UserID != userID {
		return fmt.Errorf("run not found")
	}
	ec()
	return nil
}

// Delete removes a run and all its result rows.
func (m *RunManager) Delete(userID, runID string) error {
	m.mu.Lock()
	ec, ok := m.runCancel[runID]
	m.mu.Unlock()
	if ok {
		ec() // stop it first
	}
	m.traceMu.Lock()
	delete(m.traces, runID)
	m.traceMu.Unlock()
	if m.st.GormDB() == nil {
		return nil
	}
	return m.st.Backtest().DeleteRun(userID, runID)
}

func (m *RunManager) setPause(userID, runID string, paused bool) error {
	m.mu.Lock()
	ec, ok := m.runCancel[runID]
	rec := m.registry.Get(runID)
	m.mu.Unlock()
	if !ok || rec == nil || rec.UserID != userID {
		return fmt.Errorf("run is not running")
	}
	m.mu.Lock()
	m.pauseByRun[runID] = paused
	m.mu.Unlock()
	ec()
	return nil
}

// Status returns the current run status (registry first, store fallback).
func (m *RunManager) Status(userID, runID string) (*RunRecord, error) {
	if rec := m.registry.Get(runID); rec != nil && rec.UserID == userID {
		return rec, nil
	}
	if m.st.GormDB() == nil {
		return nil, fmt.Errorf("run not found")
	}
	run, err := m.st.Backtest().GetRun(userID, runID)
	if err != nil {
		return nil, fmt.Errorf("run not found")
	}
	return &RunRecord{
		ID:        run.ID,
		UserID:    run.UserID,
		Status:    run.Status,
		StartedAt: run.StartedAt,
		EndedAt:   run.EndedAt,
		Err:       run.Error,
	}, nil
}

// List lists the user's runs with pagination + optional status filter.
func (m *RunManager) List(userID string, page, pageSize int, status string) ([]store.BacktestRun, int64, error) {
	if m.st.GormDB() == nil {
		return nil, 0, fmt.Errorf("store not available")
	}
	return m.st.Backtest().ListRuns(userID, page, pageSize, status)
}

// Store exposes the underlying store (for API data endpoints).
func (m *RunManager) Store() *store.Store { return m.st }

// StartRetention launches the periodic retention sweep (§4: completed
// runs kept 30 days). Idempotent; stops when the manager stops.
func (m *RunManager) StartRetention() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-m.retentionStop:
				return
			case <-ticker.C:
				m.sweepRetention()
			}
		}
	}()
	m.sweepRetention()
}

func (m *RunManager) sweepRetention() {
	if m.st.GormDB() == nil {
		return
	}
	runs, err := m.st.Backtest().ListRetainedRuns(RetentionDays)
	if err != nil {
		logger.Warnf("⚠️ retention sweep failed: %v", err)
		return
	}
	var retained []RetainedRun
	for _, r := range runs {
		retained = append(retained, RetainedRun{ID: r.ID, Status: r.Status, EndedAt: r.EndedAt})
	}
	expired := CleanupExpired(retained, RetentionDays*24*time.Hour, time.Now())
	for _, id := range expired {
		if err := m.st.Backtest().DeleteRunData(id); err != nil {
			logger.Warnf("⚠️ retention delete %s failed: %v", id, err)
			continue
		}
		logger.Infof("🧹 [backtest] retention removed run %s", id)
	}
}

// runToStartRequest rebuilds a StartRequest from a stored run.
func runToStartRequest(run *store.BacktestRun) *StartRequest {
	symbols, err := store.UnmarshalSymbols(run.Symbols)
	if err != nil || len(symbols) == 0 {
		return nil
	}
	modelSnap, err := UnmarshalModelSnapshot(run.ModelConfigJSON)
	if err != nil {
		return nil
	}
	req := &StartRequest{
		UserID:             run.UserID,
		Name:               run.Name,
		Symbols:            symbols,
		Timeframe:          run.Timeframe,
		StartTime:          run.StartTime,
		EndTime:            run.EndTime,
		InitialBalance:     run.InitialBalance,
		ExchangeType:       run.ExchangeType,
		FeeMaker:           run.FeeMaker, // already resolved (snapshot)
		FeeTaker:           run.FeeTaker,
		StrategyConfigJSON: run.StrategyConfigJSON,
		ModelConfigJSON:    run.ModelConfigJSON,
		ModelSnapshot:      modelSnap,
	}
	// Restore the strategy config from the stored snapshot.
	var cfg store.StrategyConfig
	if err := json.Unmarshal([]byte(run.StrategyConfigJSON), &cfg); err == nil {
		req.StrategyConfig = &cfg
	}
	return req
}
