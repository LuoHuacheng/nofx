package config

import (
	"os"
	"strconv"
	"time"
)

const (
	defaultOrderSyncInterval = 30 * time.Second
	minOrderSyncInterval     = 5 * time.Second
	maxOrderSyncInterval     = 15 * time.Minute
)

func parseOrderSyncInterval(raw string) time.Duration {
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultOrderSyncInterval
	}
	interval := time.Duration(seconds) * time.Second
	if interval < minOrderSyncInterval {
		return minOrderSyncInterval
	}
	if interval > maxOrderSyncInterval {
		return maxOrderSyncInterval
	}
	return interval
}

// OrderSyncInterval returns the configured order synchronization interval.
func OrderSyncInterval() time.Duration {
	return parseOrderSyncInterval(os.Getenv("ORDER_SYNC_INTERVAL_SECONDS"))
}
