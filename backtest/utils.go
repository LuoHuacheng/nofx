package backtest

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ParseTimeframe converts a timeframe string ("1m", "5m", "1h", "1d")
// into a duration. Returns an error for unsupported values.
func ParseTimeframe(tf string) (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(tf)) {
	case "1m":
		return time.Minute, nil
	case "3m":
		return 3 * time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "10m":
		return 10 * time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "30m":
		return 30 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "2h":
		return 2 * time.Hour, nil
	case "4h":
		return 4 * time.Hour, nil
	case "6h":
		return 6 * time.Hour, nil
	case "8h":
		return 8 * time.Hour, nil
	case "12h":
		return 12 * time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	case "1w":
		return 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported timeframe: %q", tf)
	}
}

// ValidateRunConfig checks a RunConfig against the resource guardrails
// (§4): symbol count, time span, timeframe, initial balance and bar
// availability per symbol.
func ValidateRunConfig(cfg RunConfig, feedsBySymbol map[string][]Bar) error {
	if len(cfg.Symbols) == 0 {
		return fmt.Errorf("at least one symbol is required")
	}
	if len(cfg.Symbols) > MaxSymbolsPerRun {
		return fmt.Errorf("too many symbols: %d (max %d)", len(cfg.Symbols), MaxSymbolsPerRun)
	}
	if cfg.StartTime.IsZero() || cfg.EndTime.IsZero() {
		return fmt.Errorf("start and end time are required")
	}
	if !cfg.EndTime.After(cfg.StartTime) {
		return fmt.Errorf("end time must be after start time")
	}
	if cfg.EndTime.Sub(cfg.StartTime) > MaxRunSpan {
		return fmt.Errorf("backtest span %.0f days exceeds the %d-day limit",
			cfg.EndTime.Sub(cfg.StartTime).Hours()/24, int(MaxRunSpan.Hours()/24))
	}
	if cfg.InitialBalance <= 0 {
		return fmt.Errorf("initial balance must be positive")
	}
	tf, err := ParseTimeframe(cfg.Timeframe)
	if err != nil {
		return err
	}
	if tf < time.Minute {
		return fmt.Errorf("minimum timeframe is 1m")
	}
	seen := make(map[string]bool)
	for _, sym := range cfg.Symbols {
		if seen[sym] {
			return fmt.Errorf("duplicate symbol: %s", sym)
		}
		seen[sym] = true
		if feedsBySymbol != nil {
			if len(feedsBySymbol[sym]) == 0 {
				return fmt.Errorf("no kline data for symbol %s", sym)
			}
		}
	}
	return nil
}

// TruncateToMinute floors a time to the 1-minute bucket boundary.
func TruncateToMinute(t time.Time) time.Time {
	return t.UTC().Truncate(time.Minute)
}

// SortedBars returns bars sorted by time ascending.
func SortedBars(bars []Bar) []Bar {
	out := make([]Bar, len(bars))
	copy(out, bars)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// UniqueSymbols de-duplicates and stabilizes a symbol list.
func UniqueSymbols(symbols []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range symbols {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
