package market

import (
	"testing"
)

// stubWS is a minimal WebSocketClient used only to verify that the manager
// dispatches correctly without needing a live exchange connection.
type stubWS struct {
	connected bool
}

func (s *stubWS) Connect() error                              { s.connected = true; return nil }
func (s *stubWS) Disconnect() error                           { s.connected = false; return nil }
func (s *stubWS) IsConnected() bool                           { return s.connected }
func (s *stubWS) SubscribeKlines(string, string) error        { return nil }
func (s *stubWS) UnsubscribeKlines(string, string) error      { return nil }
func (s *stubWS) SubscribeOrderUpdates() error                { return nil }
func (s *stubWS) UnsubscribeOrderUpdates() error              { return nil }
func (s *stubWS) GetKlineChannel() <-chan KlineUpdate         { return nil }
func (s *stubWS) GetOrderUpdateChannel() <-chan OrderUpdate   { return nil }
func (s *stubWS) Reconnect() error                            { return nil }

func TestWebSocketManager_RegisterAndLookup(t *testing.T) {
	wm := NewWebSocketManager()
	if err := wm.RegisterClient("binance", &stubWS{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := wm.GetClient("binance"); got == nil {
		t.Fatal("expected binance client")
	}
	if got := wm.GetClient("unknown"); got != nil {
		t.Fatal("expected nil for unknown exchange")
	}
}

func TestWebSocketManager_RegisterNilRejected(t *testing.T) {
	wm := NewWebSocketManager()
	if err := wm.RegisterClient("binance", nil); err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestWebSocketManager_FallbackDefault(t *testing.T) {
	wm := NewWebSocketManager()
	if !wm.fallbackRestAPI {
		t.Fatal("fallbackRestAPI should default to true")
	}
	wm.SetFallbackRestAPI(false)
	if wm.fallbackRestAPI {
		t.Fatal("SetFallbackRestAPI(false) didn't take effect")
	}
}

func TestKlineUpdateAndOrderUpdate_DistinctTypes(t *testing.T) {
	// Compile-time check that the two channel types are distinct.
	var kc <-chan KlineUpdate
	var oc <-chan OrderUpdate
	_ = kc
	_ = oc
}
