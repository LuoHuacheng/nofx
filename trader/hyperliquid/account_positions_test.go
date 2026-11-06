package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	hl "github.com/sonirico/go-hyperliquid"
)

func TestAccountPositionsNeverReturnPartialMainnetAccount(t *testing.T) {
	for _, failedXYZ := range []bool{false, true} {
		t.Run(fmt.Sprint(failedXYZ), func(t *testing.T) {
			xyzReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request["type"] != "clearinghouseState" || request["user"] != "owner" {
					t.Errorf("unexpected account request: %v", request)
				}
				if request["dex"] == "xyz" {
					xyzReads++
					if failedXYZ {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					fmt.Fprint(w, `{"assetPositions":[{"position":{"coin":"xyz:GOOGL","szi":"2","entryPx":"100","positionValue":"220","unrealizedPnl":"20","leverage":{"value":5}}}]}`)
					return
				}
				fmt.Fprint(w, `{"assetPositions":[{"position":{"coin":"BTC","szi":"-1","entryPx":"80000","positionValue":"82000","unrealizedPnl":"-2000","leverage":{"value":5}}}]}`)
			}))
			defer server.Close()
			key, err := crypto.GenerateKey()
			if err != nil {
				t.Fatal(err)
			}
			meta := &hl.Meta{Universe: []hl.AssetInfo{{Name: "BTC", SzDecimals: 5, MaxLeverage: 50}}}
			exchange := hl.NewExchange(context.Background(), key, server.URL, meta, "", "owner", &hl.SpotMeta{}, nil, hl.ExchangeOptClientOptions(hl.ClientOptHTTPClient(server.Client())))
			trader := &HyperliquidTrader{ctx: context.Background(), apiURL: server.URL, walletAddr: "owner", exchange: exchange}
			positions, err := trader.GetPositions()
			if xyzReads != 1 {
				t.Fatalf("xyz reads: %d", xyzReads)
			}
			if failedXYZ {
				if err == nil || !strings.Contains(err.Error(), "account positions incomplete") || positions != nil {
					t.Fatalf("partial account accepted: %v %v", positions, err)
				}
			} else if err != nil || len(positions) != 2 || positions[0]["symbol"] != "BTCUSDT" || positions[1]["symbol"] != "xyz:GOOGL" {
				t.Fatalf("complete account missing positions: %v %v", positions, err)
			}
		})
	}
}
