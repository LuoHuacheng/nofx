package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountOrdersKeepTriggersAndExactDex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req["type"] != "frontendOpenOrders" || req["user"] != "owner" {
			t.Error("unexpected account read", req)
		}
		coin := "BTC"
		if req["dex"] == "xyz" {
			coin = "xyz:GOOGL"
		}
		fmt.Fprintf(w, `[{"coin":%q,"oid":1,"side":"B","sz":"0.00162","limitPx":"84200","triggerPx":"84200","orderType":"Stop Market","reduceOnly":true,"isTrigger":true},{"coin":%q,"oid":2,"side":"B","sz":"0.00162","limitPx":"79000","triggerPx":"79000","orderType":"Take Profit Market","reduceOnly":true,"isTrigger":true},{"coin":"ETH","oid":3,"side":"A","sz":"1","limitPx":"3000","triggerPx":"0","orderType":"Limit"}]`, coin, coin)
	}))
	defer server.Close()
	b := &HyperliquidTrader{ctx: context.Background(), apiURL: server.URL, walletAddr: "owner"}
	for _, symbol := range []string{"BTCUSDT", "xyz:GOOGLUSDT"} {
		orders, err := b.GetOpenOrders(symbol)
		if err != nil || len(orders) != 2 {
			t.Fatalf("%s: %v %v", symbol, orders, err)
		}
		if orders[0].Type != "STOP" || orders[1].Type != "TAKE_PROFIT" || orders[0].PositionSide != "SHORT" || orders[0].StopPrice != 84200 || orders[1].StopPrice != 79000 {
			t.Fatal(orders)
		}
	}
}
