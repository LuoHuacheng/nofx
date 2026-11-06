package binance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"nofx/logger"
	"nofx/trader/execution"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

const futuresBrokerID = "KzrpZaP9"
const futuresBrokerPrefix = "x-" + futuresBrokerID

// getBrOrderID generates unique order ID (for futures contracts)
// Format: x-{BR_ID}{TIMESTAMP}{RANDOM}
// Futures limit is 32 characters, use this limit consistently
// Uses nanosecond timestamp + random number to ensure global uniqueness (collision probability < 10^-20)
func getBrOrderID() string {
	brID := futuresBrokerID // Shared by automatic and approved planning orders

	// Calculate available space: 32 - len("x-KzrpZaP9") = 32 - 11 = 21 characters
	// Allocation: 13-digit timestamp + 8-digit random = 21 characters (perfect utilization)
	timestamp := time.Now().UnixNano() % 10000000000000 // 13-digit nanosecond timestamp

	// Generate 4-byte random number (8 hex digits)
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	randomHex := hex.EncodeToString(randomBytes)

	// Format: x-KzrpZaP9{13-digit timestamp}{8-digit random}
	// Example: x-KzrpZaP91234567890123abcdef12 (exactly 31 characters)
	orderID := fmt.Sprintf("x-%s%d%s", brID, timestamp, randomHex)

	// Ensure not exceeding 32-character limit (theoretically exactly 31 characters)
	if len(orderID) > 32 {
		orderID = orderID[:32]
	}

	return orderID
}

// FuturesTrader Binance futures trader
type FuturesTrader struct {
	client *futures.Client

	// managedEnvironment is set only by NewManagedAccountTrader; the legacy
	// constructors leave it empty and keep their existing endpoints.
	managedEnvironment execution.Environment

	// managedStreamURL is the user-data socket pinned by the environment. Empty
	// for legacy traders, which never stream.
	managedStreamURL string

	// dualSide is the account position mode: true = dual-side (Hedge Mode),
	// false = one-way mode. Set at init; order positionSide adapts to it.
	dualSide atomic.Bool

	// Balance cache
	cachedBalance     map[string]interface{}
	balanceCacheTime  time.Time
	balanceCacheMutex sync.RWMutex

	// Position cache
	cachedPositions     []map[string]interface{}
	positionsCacheTime  time.Time
	positionsCacheMutex sync.RWMutex

	// Cache validity period (15 seconds)
	cacheDuration time.Duration
}

// NewFuturesTrader creates futures trader. The optional final argument routes
// all REST trading calls to the Binance Futures testnet
// (https://testnet.binancefuture.com). It deliberately does NOT touch the SDK
// package-level futures.UseTestnet flag — that is process-global and would
// switch every user's client, not just this one.
func NewFuturesTrader(apiKey, secretKey string, userId string, testnet ...bool) *FuturesTrader {
	trader := NewExecutionTrader(apiKey, secretKey, userId)
	if len(testnet) > 0 && testnet[0] {
		trader.client.SetApiEndpoint(futures.BaseApiTestnetUrl)
		logger.Infof("🏦 Binance Futures trader using TESTNET (%s)", futures.BaseApiTestnetUrl)
	}
	client := trader.client
	syncBinanceServerTime(client)

	// Set dual-side position mode (Hedge Mode)
	// This is required because the code uses PositionSide (LONG/SHORT)
	if err := trader.setDualSidePosition(); err != nil {
		logger.Infof("⚠️ Failed to set dual-side position mode: %v (ignore this warning if already in dual-side mode)", err)
	}

	// Query the actual position mode so order positionSide adapts to it:
	// dual-side mode requires LONG/SHORT, one-way mode rejects them (-4061)
	// and requires BOTH. Defaults to dual-side (existing behavior) if the
	// query fails.
	trader.dualSide.Store(true)
	if pm, err := client.NewGetPositionModeService().Do(context.Background()); err != nil {
		logger.Infof("⚠️ Failed to query position mode: %v (defaulting to dual-side)", err)
	} else {
		trader.dualSide.Store(pm.DualSidePosition)
		if pm.DualSidePosition {
			logger.Infof("  ✓ Account position mode: dual-side (Hedge Mode)")
		} else {
			logger.Infof("  ✓ Account position mode: one-way (orders will use positionSide BOTH)")
		}
	}

	return trader
}

// positionSide returns the PositionSide for a long/short order, adapted to the
// account's position mode: LONG/SHORT in dual-side (Hedge) mode, BOTH in
// one-way mode (LONG/SHORT is rejected there with -4061).
func (t *FuturesTrader) positionSide(long bool) futures.PositionSideType {
	if !t.dualSide.Load() {
		return futures.PositionSideTypeBoth
	}
	if long {
		return futures.PositionSideTypeLong
	}
	return futures.PositionSideTypeShort
}

// setDualSidePosition sets dual-side position mode (called during initialization)
func (t *FuturesTrader) setDualSidePosition() error {
	// Try to set dual-side position mode
	err := t.client.NewChangePositionModeService().
		DualSide(true). // true = dual-side position (Hedge Mode)
		Do(context.Background())

	if err != nil {
		// If error message contains "No need to change", it means already in dual-side position mode
		if strings.Contains(err.Error(), "No need to change position side") {
			logger.Infof("  ✓ Account is already in dual-side position mode (Hedge Mode)")
			return nil
		}
		// Other errors are returned (but won't interrupt initialization in the caller)
		return err
	}

	logger.Infof("  ✓ Account has been switched to dual-side position mode (Hedge Mode)")
	logger.Infof("  ℹ️  Dual-side position mode allows holding both long and short positions simultaneously")
	return nil
}

// syncBinanceServerTime syncs Binance server time to ensure request timestamps are valid
func syncBinanceServerTime(client *futures.Client) {
	serverTime, err := client.NewServerTimeService().Do(context.Background())
	if err != nil {
		logger.Infof("⚠️ Failed to sync Binance server time: %v", err)
		return
	}

	now := time.Now().UnixMilli()
	offset := now - serverTime
	client.TimeOffset = offset
	logger.Infof("⏱ Binance server time synced, offset %dms", offset)
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) >= len(substr) && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// calculatePrecision calculates precision from stepSize
func calculatePrecision(stepSize string) int {
	// Remove trailing zeros
	stepSize = trimTrailingZeros(stepSize)

	// Find decimal point
	dotIndex := -1
	for i := 0; i < len(stepSize); i++ {
		if stepSize[i] == '.' {
			dotIndex = i
			break
		}
	}

	// If no decimal point or decimal point is at the end, precision is 0
	if dotIndex == -1 || dotIndex == len(stepSize)-1 {
		return 0
	}

	// Return number of digits after decimal point
	return len(stepSize) - dotIndex - 1
}

// trimTrailingZeros removes trailing zeros
func trimTrailingZeros(s string) string {
	// If no decimal point, return directly
	if !stringContains(s, ".") {
		return s
	}

	// Iterate backwards to remove trailing zeros
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}

	// If last character is decimal point, remove it too
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}

	return s
}
