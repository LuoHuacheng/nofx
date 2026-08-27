package binance

import (
	"testing"

	"github.com/adshao/go-binance/v2/futures"
)

// TestNewFuturesTraderTestnetRouting verifies the testnet flag routes the
// client to the Binance Futures testnet endpoint WITHOUT touching the
// process-global futures.UseTestnet flag (which would hijack every other
// user's client).
func TestNewFuturesTraderTestnetRouting(t *testing.T) {
	// Pin the global flag to the mainnet value before and after: the
	// constructor must never mutate it.
	futures.UseTestnet = false
	defer func() { futures.UseTestnet = false }()

	mainTrader := NewFuturesTrader("k", "s", "u")
	if mainTrader.client.BaseURL != futures.BaseApiMainUrl {
		t.Fatalf("default client BaseURL = %q, want mainnet %q", mainTrader.client.BaseURL, futures.BaseApiMainUrl)
	}

	testTrader := NewFuturesTrader("k", "s", "u", true)
	if testTrader.client.BaseURL != futures.BaseApiTestnetUrl {
		t.Fatalf("testnet client BaseURL = %q, want %q", testTrader.client.BaseURL, futures.BaseApiTestnetUrl)
	}

	// The global flag must stay mainnet — per-user routing only.
	if futures.UseTestnet {
		t.Fatal("NewFuturesTrader must not flip the process-global futures.UseTestnet flag")
	}

	// Explicit false keeps mainnet.
	explicitMain := NewFuturesTrader("k", "s", "u", false)
	if explicitMain.client.BaseURL != futures.BaseApiMainUrl {
		t.Fatalf("explicit mainnet BaseURL = %q", explicitMain.client.BaseURL)
	}
}
