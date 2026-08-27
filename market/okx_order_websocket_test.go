package market

import "testing"

func TestOKXOrderWebSocket_RequiresCredentials(t *testing.T) {
	client := NewOKXOrderWebSocket(false, "", "", "")
	if err := client.Connect(); err == nil {
		t.Fatal("expected missing credentials error")
	}
}

func TestOKXOrderWebSocket_MapsOrderUpdate(t *testing.T) {
	client := NewOKXOrderWebSocket(false, "key", "secret", "pass")
	update := OKXOrderUpdate{}
	update.Arg.Channel = "orders"
	update.Data = append(update.Data, struct {
		InstID    string  `json:"instId"`
		OrdID     string  `json:"ordId"`
		ClOrdID   string  `json:"clOrdId"`
		Tag       string  `json:"tag"`
		Price     float64 `json:"px,string"`
		Sz        float64 `json:"sz,string"`
		OrdType   string  `json:"ordType"`
		Side      string  `json:"side"`
		PosSide   string  `json:"posSide"`
		Status    string  `json:"state"`
		LeavesSz  float64 `json:"leavesSz,string"`
		AccFillSz float64 `json:"accFillSz,string"`
		AvgPx     float64 `json:"avgPx,string"`
		CumEx     float64 `json:"cumEx,string"`
		Fee       float64 `json:"fee,string"`
		FeeCcy    string  `json:"feeCcy"`
		RebateCcy string  `json:"rebateCcy"`
		CtType    string  `json:"ctType"`
		CTime     int64   `json:"cTime,string"`
		UTime     int64   `json:"uTime,string"`
		TpSz      float64 `json:"tpSz,string"`
		SlSz      float64 `json:"slSz,string"`
		TpPx      float64 `json:"tpPx,string"`
		SlPx      float64 `json:"slPx,string"`
		TriggerPx float64 `json:"triggerPx,string"`
		OrdPx     float64 `json:"ordPx,string"`
	}{InstID: "BTC-USDT-SWAP", OrdID: "1", ClOrdID: "client-1", Side: "buy", PosSide: "long", OrdType: "market", Sz: 2, AccFillSz: 1.5, AvgPx: 101, CumEx: 151.5, Status: "partially_filled"})

	client.processOrderUpdate(update)
	got := <-client.GetOrderUpdateChannel()
	if got.Symbol != "BTC-USDT-SWAP" || got.OrderID != "1" || got.ExecutedQuantity != 1.5 || got.AveragePrice != 101 {
		t.Fatalf("unexpected mapped update: %+v", got)
	}
}

func TestOKXOrderWebSocket_RecreatesStopChannelAfterDisconnect(t *testing.T) {
	client := NewOKXOrderWebSocket(false, "", "", "")
	oldStop := client.stopCh
	close(oldStop)
	if err := client.Connect(); err == nil {
		t.Fatal("expected credential error")
	}
	if client.stopCh == oldStop {
		t.Fatal("Connect reused a closed stop channel")
	}
}
func TestOKXOrderWebSocket_URL(t *testing.T) {
	if got := NewOKXOrderWebSocket(false, "k", "s", "p").url; got != "wss://ws.okx.com:8443/ws/v5/private" {
		t.Fatalf("mainnet URL=%q", got)
	}
	if got := NewOKXOrderWebSocket(true, "k", "s", "p").url; got != "wss://wspap.okx.com:8443/ws/v5/private" {
		t.Fatalf("testnet URL=%q", got)
	}
}
