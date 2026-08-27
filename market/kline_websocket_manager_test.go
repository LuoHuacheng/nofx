package market

import (
	"testing"
	"time"
)

func TestKlineWebSocketManager_InitialState(t *testing.T) {
	manager := NewKlineWebSocketManager(true)
	if manager == nil || manager.maxStreamsPerConn != 500 || !manager.testnet {
		t.Fatalf("unexpected manager: %+v", manager)
	}
	if len(manager.connections) != 0 || len(manager.subscriptions) != 0 {
		t.Fatal("manager should start without connections or subscriptions")
	}
}

func TestKlineWebSocketManager_ConnectionCapacity(t *testing.T) {
	manager := NewKlineWebSocketManager(false)
	manager.connections = []*BinanceWebSocketClient{
		{},
		{},
	}
	manager.maxStreamsPerConn = 2
	manager.subscriptions["BTCUSDT@kline_1m"] = 0
	manager.subscriptions["ETHUSDT@kline_1m"] = 0
	if got := manager.findAvailableConnection(); got != 1 {
		t.Fatalf("got connection index %d, want 1", got)
	}
	manager.subscriptions["BNBUSDT@kline_1m"] = 1
	manager.subscriptions["ADAUSDT@kline_1m"] = 1
	if got := manager.findAvailableConnection(); got != -1 {
		t.Fatalf("got connection index %d, want -1 when full", got)
	}
}

func TestKlineWebSocketManager_Status(t *testing.T) {
	manager := NewKlineWebSocketManager(false)
	manager.activeSymbols["BTCUSDT"] = true
	manager.subscriptions["BTCUSDT@kline_1m"] = 0
	manager.lastUpdateTime["BTCUSDT@kline_1m"] = time.Now()
	status := manager.GetStatus()
	if status["active_symbols"] != 1 || status["subscriptions"] != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestEqualStringSlices(t *testing.T) {
	if !equalStringSlices([]string{"1m", "4h"}, []string{"1m", "4h"}) {
		t.Fatal("equal slices reported unequal")
	}
	if equalStringSlices([]string{"1m"}, []string{"4h"}) {
		t.Fatal("different slices reported equal")
	}
	if !equalStringSlices(nil, []string{}) {
		t.Fatal("nil and empty slices should both represent no timeframes")
	}
}
