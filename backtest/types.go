package backtest

import (
	"time"
)

// Bar is a single OHLCV bar. Time is the bar close time (millisecond
// aligned, like market.Kline.OpenTime semantics used by the feed).
type Bar struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// FundingRate is a funding rate sample for a symbol at Time.
type FundingRate struct {
	Time time.Time
	Rate float64
}

// OIDataPoint is an open-interest sample for a symbol at Time.
type OIDataPoint struct {
	Time    time.Time
	Latest  float64
	Average float64
}

// Feed iterates klines for all participating symbols on a 1-minute
// global time bucket (outer join over symbols, §2.3). A symbol without
// a new bar in a bucket keeps its previous close (last-known-price
// fill), so position equity can be computed continuously.
type Feed interface {
	// Symbols returns the participating symbols in stable order.
	Symbols() []string
	// Next advances the feed to the next 1-minute bucket. It returns the
	// bucket time, the symbols that produced a new bar inside it, and
	// ok=false once the data is exhausted.
	Next() (t time.Time, newBars []string, ok bool)
	// LastPrice returns the most recent known close for symbol, or 0 if
	// the symbol has produced no bar yet.
	LastPrice(symbol string) float64
	// LastBar returns the most recently consumed bar for symbol (used to
	// fill orders and test stop-loss/take-profit against the bar).
	LastBar(symbol string) (Bar, bool)
	// History returns all bars for symbol whose close time is <= t,
	// oldest first (used to build the AI kline window).
	History(symbol string, t time.Time) []Bar
	// FundingRate returns the most recent funding rate for symbol at or
	// before t (0 if none — engine logs a funding_missing trace).
	FundingRate(symbol string, t time.Time) float64
	// OIData returns the most recent open-interest sample for symbol at
	// or before t (nil if none).
	OIData(symbol string, t time.Time) *OIDataPoint
}

// ExecutionModel abstracts order filling. This release only ships
// BarCloseExecution (§2.1); TickExecution / SlippageExecution can be
// added later without touching the engine.
type ExecutionModel interface {
	// FillPrice returns the fill price for an order given the bar the
	// order is executed against.
	FillPrice(bar Bar) float64
}

// BarCloseExecution fills every order at the bar close price. No
// intra-bar path, no slippage, no partial fills.
type BarCloseExecution struct{}

// FillPrice implements ExecutionModel.
func (BarCloseExecution) FillPrice(bar Bar) float64 { return bar.Close }

// Decision is a single trading decision produced by the Decider. It is
// intentionally decoupled from kernel.Decision; runner.go (P3.3)
// converts kernel decisions into this shape.
type Decision struct {
	Symbol          string
	Action          string // open_long | open_short | close_long | close_short | partial_close | adjust_stop_loss | hold | wait
	Leverage        int
	PositionSizeUSD float64
	StopLoss        float64
	TakeProfit      float64
	ClosePct        float64 // partial_close: fraction of the open position to close, (0,1]
}

// DecisionContext is the state visible to a Decider at a decision
// point. Prices holds each symbol's last known close.
type DecisionContext struct {
	Time    time.Time
	Account *BacktestAccount
	Prices  map[string]float64
	Feed    Feed
	Config  RunConfig
}

// Decider produces decisions at each decision point. Tests inject a
// fixed Decider; runner.go injects the kernel adapter.
type Decider interface {
	Decide(ctx *DecisionContext) []Decision
}

// DeciderFunc adapts a plain function to Decider.
type DeciderFunc func(ctx *DecisionContext) []Decision

// Decide implements Decider.
func (f DeciderFunc) Decide(ctx *DecisionContext) []Decision { return f(ctx) }

// RunConfig is the user-facing configuration of a backtest run. Fee
// fields of 0 mean "use exchange default fallbacks".
type RunConfig struct {
	UserID             string
	Name               string
	Symbols            []string
	Timeframe          string // bar period / decision cadence, e.g. "5m"
	StartTime          time.Time
	EndTime            time.Time
	InitialBalance     float64
	FeeMaker           float64
	FeeTaker           float64
	ExchangeType       string // used only for fee fallbacks
	StrategyConfigJSON string // strategy config snapshot (for kernel/runner)
	ModelConfigJSON    string // model config snapshot (for kernel/runner)
}

// BacktestTrade is one filled trade (mirrors BacktestTradeEvent).
type BacktestTrade struct {
	Time     time.Time
	Symbol   string
	Side     string // buy/sell
	Position string // open/close
	Price    float64
	Size     float64
	Fee      float64
	PnL      float64 // meaningful on close
}

// TraceEntry is a structured log line for the trace API.
type TraceEntry struct {
	Time    time.Time
	Kind    string // decision | fill | liquidation | funding_missing | info
	Symbol  string
	Message string
	Data    string // optional JSON payload
}

// TraceLogger receives engine trace lines (nil-safe).
type TraceLogger func(entry TraceEntry)

// Options configures the engine.
type Options struct {
	Execution ExecutionModel
	Trace     TraceLogger
}

// EngineOption mutates Options.
type EngineOption func(*Options)

// WithExecution overrides the execution model (default BarCloseExecution).
func WithExecution(exec ExecutionModel) EngineOption {
	return func(o *Options) { o.Execution = exec }
}

// WithTrace installs a trace logger.
func WithTrace(tl TraceLogger) EngineOption {
	return func(o *Options) { o.Trace = tl }
}

// Result is the outcome of a completed run.
type Result struct {
	Trades []BacktestTrade
	Equity []EquityPoint
	Trace  []TraceEntry
}

// Metrics computed from a Result (see metrics.go).
func (r *Result) Metrics(initialBalance float64) Metrics {
	return ComputeMetrics(r.Equity, r.Trades, initialBalance)
}
