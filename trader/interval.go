package trader

import (
	"math"
	"time"
)

const (
	defaultOrderSyncInterval = 30 * time.Second
	minOrderSyncInterval     = 5 * time.Second
	maxOrderSyncInterval     = 15 * time.Minute
	minAdaptiveScanInterval  = 30 * time.Second
)

func resolveOrderSyncInterval(configured time.Duration) time.Duration {
	if configured <= 0 {
		return defaultOrderSyncInterval
	}
	if configured < minOrderSyncInterval {
		return minOrderSyncInterval
	}
	if configured > maxOrderSyncInterval {
		return maxOrderSyncInterval
	}
	return configured
}

// adaptiveScanInterval applies the conservative nofxplus policy to a base
// interval. Volatility is the mean absolute move as a fraction (0.02 = 2%).
func adaptiveScanInterval(base time.Duration, openPositions int, volatility float64) time.Duration {
	if base <= 0 {
		base = 3 * time.Minute
	}
	if volatility > 0.02 {
		return minAdaptiveScanInterval
	}
	if volatility > 0.01 || openPositions > 0 {
		if half := base / 2; half >= minAdaptiveScanInterval {
			return half
		}
		return minAdaptiveScanInterval
	}
	return base
}

// calculateAdaptiveScanInterval derives a bounded interval from current open
// positions and recent closed-position movement. It only runs when the feature
// flag is enabled; missing exchange/store data returns the base interval.
func (at *AutoTrader) calculateAdaptiveScanInterval() time.Duration {
	if at == nil {
		return 3 * time.Minute
	}
	openPositions := 0
	if at.trader != nil {
		if positions, err := at.trader.GetPositions(); err == nil {
			openPositions = len(positions)
		}
	}

	volatility := 0.0
	if at.store != nil && at.id != "" {
		if positions, err := at.store.Position().GetClosedPositions(at.id, 10); err == nil && len(positions) > 0 {
			var total float64
			var samples int
			for _, position := range positions {
				if position == nil || position.EntryPrice <= 0 || position.ExitPrice <= 0 {
					continue
				}
				total += math.Abs((position.ExitPrice - position.EntryPrice) / position.EntryPrice)
				samples++
			}
			if samples > 0 {
				volatility = total / float64(samples)
			}
		}
	}
	return adaptiveScanInterval(at.config.ScanInterval, openPositions, volatility)
}
