package backtest

import (
	"context"
	"fmt"
	"time"
)

// Engine runs a bar-on-close backtest over a Feed, driven by a Decider.
//
// Loop (design §2.3): the feed iterates 1-minute buckets over all
// symbols (outer join). Each bucket where at least one symbol produced
// a new bar advances the backtest cycle: funding settles at 8h
// boundaries, stop-loss/take-profit and liquidation are evaluated at
// the just-closed bars, the Decider is invoked once, and all orders
// fill at the current bar closes (BarCloseExecution, no slippage).
// Equity is sampled every EquitySampleInterval buckets and always on
// trade events.
type Engine struct {
	cfg       RunConfig
	feed      Feed
	account   *BacktestAccount
	equity    *EquityCurve
	exec      ExecutionModel
	decider   Decider
	traceLog  TraceLogger
	trace     []TraceEntry
	timeframe time.Duration
}

// NewEngine validates the run configuration, resolves fee fallbacks and
// builds the engine. feed must already contain the run symbols.
func NewEngine(cfg RunConfig, feed Feed, decider Decider, opts ...EngineOption) (*Engine, error) {
	if decider == nil {
		return nil, fmt.Errorf("decider is required")
	}
	if feed == nil {
		return nil, fmt.Errorf("feed is required")
	}
	cfg.Symbols = UniqueSymbols(cfg.Symbols)
	if err := ValidateRunConfig(cfg, nil); err != nil {
		return nil, err
	}
	tf, err := ParseTimeframe(cfg.Timeframe)
	if err != nil {
		return nil, err
	}
	feeTaker := cfg.FeeTaker
	if feeTaker <= 0 {
		feeTaker = DefaultTakerFee(cfg.ExchangeType)
	}
	feeMaker := cfg.FeeMaker
	if feeMaker <= 0 {
		feeMaker = DefaultMakerFee
	}

	o := Options{Execution: BarCloseExecution{}}
	for _, opt := range opts {
		opt(&o)
	}
	if o.Execution == nil {
		o.Execution = BarCloseExecution{}
	}

	return &Engine{
		cfg:       cfg,
		feed:      feed,
		account:   NewAccount(cfg.InitialBalance, feeMaker, feeTaker),
		equity:    NewEquityCurve(TruncateToMinute(cfg.StartTime), cfg.InitialBalance),
		exec:      o.Execution,
		decider:   decider,
		traceLog:  o.Trace,
		timeframe: tf,
	}, nil
}

// Account exposes the simulated account (for result inspection in tests).
func (e *Engine) Account() *BacktestAccount { return e.account }

// Run executes the backtest until the feed is exhausted or ctx is
// cancelled. It is safe to call once per engine.
func (e *Engine) Run(ctx context.Context) (*Result, error) {
	e.trace = nil
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		t, newBars, ok := e.feed.Next()
		if !ok {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if t.After(e.cfg.EndTime) {
			break
		}

		// Mark-to-market on every bucket where a symbol printed a bar.
		for _, sym := range newBars {
			e.account.UpdateMark(sym, e.feed.LastPrice(sym))
		}

		// Funding settles at 8h boundaries (perp positions only).
		if t.Unix()%int64(FundingInterval.Seconds()) == 0 {
			e.settleFunding(t)
		}

		// Stop-loss / take-profit against the just-closed bars.
		e.evaluateStops(t, newBars)

		// Liquidation after mark updates.
		for _, tr := range e.account.CheckLiquidation(t) {
			e.emitTrace(t, "liquidation", tr.Symbol,
				fmt.Sprintf("position force-closed at %.4f (pnl %.4f)", tr.Price, tr.PnL), "")
			e.equity.Force()
		}

		// Decision cycle: any new bar advances the loop (§2.3), and the
		// decision fills at the current bar closes.
		if len(newBars) > 0 {
			e.decisionCycle(t)
		}

		// Equity sampling (every 5 buckets; forced after trades).
		e.equity.Sample(t, e.account.Balance(), e.account.Equity())
	}

	// Final sample so the curve ends at the last equity value.
	last := e.feedLastTime()
	if last.IsZero() {
		last = TruncateToMinute(e.cfg.EndTime)
	}
	e.equity.Sample(last, e.account.Balance(), e.account.Equity())

	return &Result{
		Trades: SortTradesByTime(e.account.Trades()),
		Equity: e.equity.Points(),
		Trace:  e.trace,
	}, nil
}

// feedLastTime returns the last bucket time processed (feed cursor).
func (e *Engine) feedLastTime() time.Time {
	// MemoryFeed advances its internal bucket; the simplest stable value
	// is the last equity sample time.
	return e.equity.Last().Time
}

// settleFunding applies the current funding rate to every open position.
// A symbol without funding data logs a funding_missing trace once per
// settlement.
func (e *Engine) settleFunding(t time.Time) {
	for _, sym := range e.feed.Symbols() {
		if !e.account.HasPosition(sym) {
			continue
		}
		rate := e.feed.FundingRate(sym, t)
		if rate == 0 && !e.hasFundingData(sym) {
			e.emitTrace(t, "funding_missing", sym,
				"no historical funding rate; funding treated as 0 (§2.2)", "")
			continue
		}
		e.account.ApplyFunding(sym, rate, t)
	}
}

func (e *Engine) hasFundingData(sym string) bool {
	// A feed reports funding presence by returning a non-zero rate at
	// some point; with a pure zero series there is nothing to settle.
	if rate := e.feed.FundingRate(sym, e.cfg.EndTime); rate != 0 {
		return true
	}
	return false
}

// evaluateStops tests stop-loss/take-profit against the bars that just
// closed. When a bar touches both levels, the stop-loss is assumed to
// trigger first (conservative).
func (e *Engine) evaluateStops(t time.Time, newBars []string) {
	for _, sym := range newBars {
		pos := e.account.PositionAt(sym)
		if pos == nil {
			continue
		}
		bar, ok := e.feed.LastBar(sym)
		if !ok {
			continue
		}
		fill := 0.0
		reason := ""
		if pos.Side == "long" {
			if pos.StopLoss > 0 && bar.Low <= pos.StopLoss {
				fill, reason = pos.StopLoss, "stop_loss"
			} else if pos.TakeProfit > 0 && bar.High >= pos.TakeProfit {
				fill, reason = pos.TakeProfit, "take_profit"
			}
		} else {
			if pos.StopLoss > 0 && bar.High >= pos.StopLoss {
				fill, reason = pos.StopLoss, "stop_loss"
			} else if pos.TakeProfit > 0 && bar.Low <= pos.TakeProfit {
				fill, reason = pos.TakeProfit, "take_profit"
			}
		}
		if fill == 0 {
			continue
		}
		if tr, err := e.account.Close(sym, fill, t); err == nil {
			e.emitTrace(t, "fill", sym, fmt.Sprintf("%s exit at %.4f (pnl %.4f)", reason, fill, tr.PnL), "")
			e.equity.Force()
		}
	}
}

// decisionCycle builds the decision context, invokes the Decider and
// executes the resulting orders at the current bar closes.
func (e *Engine) decisionCycle(t time.Time) {
	ctx := &DecisionContext{
		Time:    t,
		Account: e.account,
		Prices:  make(map[string]float64, len(e.feed.Symbols())),
		Feed:    e.feed,
		Config:  e.cfg,
	}
	for _, sym := range e.feed.Symbols() {
		ctx.Prices[sym] = e.feed.LastPrice(sym)
	}

	decisions := e.decider.Decide(ctx)
	for _, d := range decisions {
		e.execute(d, t)
	}
}

// execute applies one decision as a market order at the current bar
// close of the symbol.
func (e *Engine) execute(d Decision, t time.Time) {
	if d.Symbol == "" {
		return
	}
	bar, ok := e.feed.LastBar(d.Symbol)
	if !ok || bar.Close <= 0 {
		e.emitTrace(t, "info", d.Symbol, fmt.Sprintf("no bar to fill decision %q at %s", d.Action, t.Format(time.RFC3339)), "")
		return
	}
	price := e.exec.FillPrice(bar)

	switch d.Action {
	case "open_long", "open_short":
		side := "long"
		if d.Action == "open_short" {
			side = "short"
		}
		if err := e.account.Open(d.Symbol, side, d.PositionSizeUSD, price, d.Leverage, t); err != nil {
			e.emitTrace(t, "info", d.Symbol, fmt.Sprintf("open %s rejected: %v", side, err), "")
			return
		}
		if d.StopLoss > 0 || d.TakeProfit > 0 {
			e.account.SetProtection(d.Symbol, d.StopLoss, d.TakeProfit)
		}
		pos := e.account.PositionAt(d.Symbol)
		e.emitTrace(t, "fill", d.Symbol, fmt.Sprintf("%s at %.4f (size %.6f, lev %dx)", d.Action, price, pos.Qty, d.Leverage), "")
		e.equity.Force()
	case "close_long", "close_short":
		if !e.account.HasPosition(d.Symbol) {
			e.emitTrace(t, "info", d.Symbol, "close ignored: no open position", "")
			return
		}
		if tr, err := e.account.Close(d.Symbol, price, t); err == nil {
			e.emitTrace(t, "fill", d.Symbol, fmt.Sprintf("close at %.4f (pnl %.4f)", price, tr.PnL), "")
			e.equity.Force()
		}
	case "hold", "wait":
		// no-op
	default:
		e.emitTrace(t, "info", d.Symbol, fmt.Sprintf("unknown action %q ignored", d.Action), "")
	}
}

// emitTrace records a trace line when a logger is installed.
func (e *Engine) emitTrace(t time.Time, kind, symbol, message, data string) {
	entry := TraceEntry{Time: t, Kind: kind, Symbol: symbol, Message: message, Data: data}
	e.trace = append(e.trace, entry)
	if e.traceLog != nil {
		e.traceLog(entry)
	}
}
