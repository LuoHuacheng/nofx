// Package backtest implements a self-contained historical backtest
// subsystem for the NOFX trading assistant (P3).
//
// The core (account, equity, metrics, data feed, engine) is fully
// self-contained: it has no dependency on the live trading stack. AI
// decisions are injected through the Decider interface; the kernel
// adapter lives in runner.go (P3.3) and reuses the current kernel
// StrategyEngine instead of duplicating nofx/decision.
package backtest

import "time"

// Run status values stored in BacktestRun.Status.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Resource guardrails (§4 of the design doc).
const (
	// MaxSymbolsPerRun caps the number of symbols per backtest.
	MaxSymbolsPerRun = 20
	// MaxRunSpan caps the backtest time window (90 days).
	MaxRunSpan = 90 * 24 * time.Hour
	// MinTimeframe is the smallest supported bar period (1 minute).
	MinTimeframe = "1m"
	// MaxEquityPoints caps persisted equity samples (500k).
	MaxEquityPoints = 500000
	// RunTimeout is the wall-clock limit for a single run (30 min).
	RunTimeout = 30 * time.Minute
	// RetentionDays is how long completed run results are kept (30 days).
	RetentionDays = 30
	// MaxRunningPerUser is the per-user concurrent running-run limit.
	MaxRunningPerUser = 2
	// EquitySampleInterval: equity is sampled every N time buckets,
	// and always on trade events (§P3.2).
	EquitySampleInterval = 5
	// FundingInterval is the perp funding settlement interval (8h).
	FundingInterval = 8 * time.Hour
)

// Default exchange fee fallbacks (§2.2). When the user has no fee
// configuration, taker fees default per exchange type.
const (
	DefaultOKXTakerFee     = 0.0005
	DefaultBinanceTakerFee = 0.001
	DefaultMakerFee        = 0.0002
)

// DefaultTakerFee returns the taker fee fallback for an exchange type.
func DefaultTakerFee(exchangeType string) float64 {
	switch exchangeType {
	case "okx":
		return DefaultOKXTakerFee
	default:
		// binance, bybit, gate, kucoin, hyperliquid and anything unknown.
		return DefaultBinanceTakerFee
	}
}
