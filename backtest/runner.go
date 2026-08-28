package backtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/provider/vergex"
	"nofx/store"
)

// StrategyConfigSnapshot is the JSON snapshot of the strategy config
// stored in BacktestRun.StrategyConfigJSON. It round-trips through
// store.StrategyConfig (which has custom JSON marshaling).
type StrategyConfigSnapshot = store.StrategyConfig

// KernelRunner adapts the current kernel StrategyEngine to the backtest
// Decider interface (P3.3). It builds the kernel.Context from the
// backtest state at each decision point, pre-fills MarketDataMap so the
// kernel skips live market fetching, and short-circuits the vergex / OI
// ranking network paths with non-nil empty maps.
type KernelRunner struct {
	engine     *kernel.StrategyEngine
	client     mcp.AIClient
	cache      *AICache
	cfg        RunConfig
	strCfg     *store.StrategyConfig
	timeframe  string
	tf         time.Duration
	klineCount int
	callCount  int

	// onDecision, when set, receives every decision cycle (context +
	// decisions JSON) for persistence (BacktestDecisionLog).
	onDecision func(t time.Time, contextJSON, decisionsJSON string)
}

// NewKernelRunner builds the kernel engine from the strategy config and
// prepares the run-time kline overrides (single timeframe = run bar
// period). The config is cloned so the user's stored strategy is never
// mutated.
func NewKernelRunner(cfg RunConfig, strCfg *store.StrategyConfig, client mcp.AIClient, cache *AICache) (*KernelRunner, error) {
	if strCfg == nil {
		return nil, fmt.Errorf("strategy config is required")
	}
	if client == nil {
		return nil, fmt.Errorf("AI client is required")
	}
	clone := *strCfg

	tf, err := ParseTimeframe(cfg.Timeframe)
	if err != nil {
		return nil, err
	}

	// The backtest feed only carries bars at the run timeframe; align the
	// strategy's kline window so the prompt matches the available data.	clone.Indicators.Klines.PrimaryTimeframe = cfg.Timeframe
	clone.Indicators.Klines.SelectedTimeframes = []string{cfg.Timeframe}
	clone.Indicators.Klines.EnableMultiTimeframe = false
	klineCount := clone.Indicators.Klines.PrimaryCount
	if klineCount < 10 {
		klineCount = 10
	}
	if klineCount > 30 {
		klineCount = 30
	}
	clone.Indicators.Klines.PrimaryCount = klineCount

	engine := kernel.NewStrategyEngine(&clone, "")
	if engine == nil {
		return nil, fmt.Errorf("failed to create strategy engine")
	}

	if cache == nil {
		cache = NewAICache(1000)
	}
	return &KernelRunner{
		engine:     engine,
		client:     client,
		cache:      cache,
		cfg:        cfg,
		strCfg:     &clone,
		timeframe:  cfg.Timeframe,
		tf:         tf,
		klineCount: klineCount,
	}, nil
}

// SetDecisionHook installs the decision persistence callback.
func (r *KernelRunner) SetDecisionHook(fn func(t time.Time, contextJSON, decisionsJSON string)) {
	r.onDecision = fn
}

// Decide implements backtest.Decider (§2.3: one call per bar event).
func (r *KernelRunner) Decide(dc *DecisionContext) []Decision {
	r.callCount++
	ctx, ok := r.buildContext(dc)
	if !ok {
		return nil
	}

	// Deterministic cache key over everything that can change the decision.
	klineDigest := r.klineDigest(dc)
	key := CacheKey(r.cfg.UserID+"/"+r.timeframe, dc.Prices, positionStrings(dc.Account), dc.Account.Equity(), klineDigest)
	if payload := r.cache.Get(key); payload != nil {
		r.emitDecisionLog(dc.Time, ctx, payload)
		return decodeDecisions(payload)
	}

	fd, err := kernel.GetFullDecisionWithStrategy(ctx, r.client, r.engine, "")
	if err != nil {
		// The kernel guards (token estimate, parsing) abort this cycle; the
		// run continues with no new orders from this decision.
		logger.Warnf("⚠️ [backtest %s] decision failed at %s: %v", r.cfg.Name, dc.Time.Format(time.RFC3339), err)
		return nil
	}
	if fd == nil {
		return nil
	}
	payload, err := json.Marshal(fd.Decisions)
	if err != nil {
		return nil
	}
	r.cache.Set(key, payload)
	r.emitDecisionLog(dc.Time, ctx, payload)
	return decodeDecisions(payload)
}

// buildContext constructs the kernel.Context at a decision point.
// Returns ok=false when no symbol has any data yet (nothing to decide).
func (r *KernelRunner) buildContext(dc *DecisionContext) (*kernel.Context, bool) {
	equity := dc.Account.Equity()
	balance := dc.Account.Balance()
	unrealized := dc.Account.UnrealizedPnL()
	marginUsed := dc.Account.MarginUsed()
	totalEquity := equity
	if totalEquity <= 0 {
		totalEquity = 1e-9 // avoid div-by-zero in prompt formatting
	}

	marketData := make(map[string]*market.Data, len(dc.Feed.Symbols()))
	candidates := make([]kernel.CandidateCoin, 0, len(dc.Feed.Symbols()))
	for _, sym := range dc.Feed.Symbols() {
		bars := dc.Feed.History(sym, dc.Time)
		if len(bars) == 0 {
			continue // no data at this point; pruneCandidateCoins drops it
		}
		// Trim to the trailing kline window so the prompt and the indicator
		// series match the live GetWithTimeframes(symbol, tfs, primary,
		// klineCount) semantics; without this the full run history would
		// enter the prompt and indicators would be O(n²) per cycle.
		if len(bars) > r.klineCount {
			bars = bars[len(bars)-r.klineCount:]
		}
		klines := toKlines(bars, r.tf)
		data, err := market.BuildDataFromKlines(sym, r.timeframe, klines, nil)
		if err != nil {
			continue
		}
		data.FundingRate = dc.Feed.FundingRate(sym, dc.Time)
		if oi := dc.Feed.OIData(sym, dc.Time); oi != nil {
			data.OpenInterest = &market.OIData{Latest: oi.Latest, Average: oi.Average}
		}
		marketData[sym] = data
		candidates = append(candidates, kernel.CandidateCoin{Symbol: sym})
	}
	if len(marketData) == 0 {
		return nil, false
	}

	positions := make([]kernel.PositionInfo, 0, len(dc.Account.Positions()))
	for _, p := range dc.Account.Positions() {
		pnl := p.UnrealizedPnL(p.MarkPrice)
		pnlPct := 0.0
		if p.MarginUsed() > 0 {
			pnlPct = pnl / p.MarginUsed() * 100
		}
		positions = append(positions, kernel.PositionInfo{
			Symbol:           p.Symbol,
			Side:             p.Side,
			EntryPrice:       p.EntryPrice,
			MarkPrice:        p.MarkPrice,
			Quantity:         p.Qty,
			Leverage:         p.Leverage,
			UnrealizedPnL:    pnl,
			UnrealizedPnLPct: pnlPct,
			LiquidationPrice: p.LiqPrice,
			MarginUsed:       p.MarginUsed(),
			UpdateTime:       dc.Time.UnixMilli(),
		})
	}

	risk := r.strCfg.RiskControl
	ctx := &kernel.Context{
		CurrentTime:     dc.Time.UTC().Format("2006-01-02 15:04:05 UTC"),
		RuntimeMinutes:  int(dc.Time.Sub(r.cfg.StartTime).Minutes()),
		CallCount:       r.callCount,
		BTCETHLeverage:  risk.BTCETHMaxLeverage,
		AltcoinLeverage: risk.AltcoinMaxLeverage,
		Account: kernel.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: balance,
			UnrealizedPnL:    unrealized,
			TotalPnL:         equity - r.cfg.InitialBalance,
			TotalPnLPct:      pct(equity, r.cfg.InitialBalance),
			MarginUsed:       marginUsed,
			MarginUsedPct:    pct(marginUsed, totalEquity),
			PositionCount:    len(positions),
		},
		Positions:      positions,
		CandidateCoins: candidates,
		MarketDataMap:  marketData,
		Timeframes:     []string{r.timeframe},
		// Non-nil empty maps short-circuit the engine's live fetches.
		OITopDataMap:  make(map[string]*kernel.OITopData),
		VergexDataMap: make(map[string]*vergex.MarketAnalysis),
	}
	return ctx, true
}

// klineDigest hashes the kline windows used for this decision so the
// cache key changes whenever the price history changes.
func (r *KernelRunner) klineDigest(dc *DecisionContext) string {
	h := sha256.New()
	for _, sym := range dc.Feed.Symbols() {
		bars := dc.Feed.History(sym, dc.Time)
		// Superset of the prompt window (klineCount + 8): the digest changes
		// slightly before the window slides, which only costs an occasional
		// extra cache miss, never a stale hit. Bars are consumed monotonically.
		n := len(bars)
		if n > r.klineCount+8 {
			n = r.klineCount + 8
		}
		start := len(bars) - n
		if start < 0 {
			start = 0
		}
		h.Write([]byte(sym))
		for _, b := range bars[start:] {
			h.Write([]byte(formatFloat(b.Close)))
			h.Write([]byte{','})
		}
		h.Write([]byte{';'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// emitDecisionLog records the decision cycle for persistence.
func (r *KernelRunner) emitDecisionLog(t time.Time, ctx *kernel.Context, decisionsJSON []byte) {
	if r.onDecision == nil {
		return
	}
	summary := struct {
		Time       string                 `json:"time"`
		Prices     map[string]float64     `json:"prices"`
		Equity     float64                `json:"equity"`
		Balance    float64                `json:"balance"`
		Positions  []kernel.PositionInfo  `json:"positions"`
		Candidates []kernel.CandidateCoin `json:"candidates"`
		MarketData map[string]float64     `json:"market_data"`
	}{
		Time:       t.UTC().Format(time.RFC3339),
		Equity:     ctx.Account.TotalEquity,
		Balance:    ctx.Account.AvailableBalance,
		Positions:  ctx.Positions,
		Candidates: ctx.CandidateCoins,
		Prices:     make(map[string]float64),
		MarketData: make(map[string]float64),
	}
	for sym, data := range ctx.MarketDataMap {
		summary.Prices[sym] = data.CurrentPrice
		summary.MarketData[sym] = data.CurrentPrice
	}
	b, _ := json.Marshal(summary)
	r.onDecision(t, string(b), string(decisionsJSON))
}

// ============================================================================
// Conversions
// ============================================================================

// toKlines converts backtest bars to market.Kline (OpenTime = bar open).
func toKlines(bars []Bar, tf time.Duration) []market.Kline {
	out := make([]market.Kline, len(bars))
	for i, b := range bars {
		out[i] = market.Kline{
			OpenTime: b.Time.Add(-tf).UnixMilli(),
			Open:     b.Open,
			High:     b.High,
			Low:      b.Low,
			Close:    b.Close,
			Volume:   b.Volume,
		}
	}
	return out
}

// decodeDecisions converts a serialized kernel decision list into
// backtest decisions (unknown actions pass through; the engine ignores
// them safely).
func decodeDecisions(payload []byte) []Decision {
	var kernelDecisions []kernel.Decision
	if err := json.Unmarshal(payload, &kernelDecisions); err != nil {
		return nil
	}
	out := make([]Decision, 0, len(kernelDecisions))
	for _, d := range kernelDecisions {
		if d.Symbol == "" {
			continue
		}
		out = append(out, Decision{
			Symbol:          d.Symbol,
			Action:          d.Action,
			Leverage:        d.Leverage,
			PositionSizeUSD: d.PositionSizeUSD,
			StopLoss:        d.StopLoss,
			TakeProfit:      d.TakeProfit,
			ClosePct:        d.ClosePct,
		})
	}
	return out
}

// positionStrings renders open positions for the cache key.
func positionStrings(acct *BacktestAccount) []string {
	pos := acct.Positions()
	out := make([]string, 0, len(pos))
	for _, p := range pos {
		out = append(out, fmt.Sprintf("%s:%s:%.6f:%.6f:%d", p.Symbol, p.Side, p.Qty, p.EntryPrice, p.Leverage))
	}
	return out
}

func pct(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return part / total * 100
}
