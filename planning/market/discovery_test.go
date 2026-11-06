package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestBinanceDiscoveryRanksNotionalAndKeepsListedStockContracts(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.URL.Path == "/fapi/v1/exchangeInfo" {
			fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"PERPETUAL","underlyingType":"COIN"},{"symbol":"TSLAUSDT","baseAsset":"TSLA","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"TRADIFI_PERPETUAL","underlyingType":"EQUITY"},{"symbol":"OLDUSDT","baseAsset":"OLD","quoteAsset":"USDT","marginAsset":"USDT","status":"SETTLING","contractType":"PERPETUAL"},{"symbol":"GHOSTUSDT","baseAsset":"GHOST","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"PERPETUAL"}]}`)
			return
		}
		fmt.Fprint(w, `[{"symbol":"BTCUSDT","quoteVolume":"1000000000","lastPrice":"80000"},{"symbol":"TSLAUSDT","quoteVolume":"2000000000","lastPrice":"350"},{"symbol":"OLDUSDT","quoteVolume":"9999999999"},{"symbol":"GHOSTUSDT","quoteVolume":"NaN"}]`)
	}))
	defer server.Close()
	c := New()
	c.BinanceFutures = server.URL
	c.HTTP = server.Client()
	for i := 0; i < 2; i++ {
		list, err := c.Markets(context.Background(), "binance")
		if err != nil || len(list.Markets) != 2 || list.Markets[0].Symbol != "TSLAUSDT" || list.Markets[0].Category != "stocks" || !list.Markets[0].ExecutionSupported {
			t.Fatalf("invalid market ranking: %+v %v", list, err)
		}
	}
	if reads.Load() != 2 {
		t.Fatal("cache did not prevent repeated exchange reads", reads.Load())
	}
}

func TestHyperliquidDiscoveryPreservesDexIdentityAndReportsPartialCoverage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q map[string]string
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("invalid request")
			return
		}
		switch q["type"] {
		case "perpDexs":
			fmt.Fprint(w, `[null,{"name":"xyz"},{"name":"cash"},{"name":"broken"}]`)
		case "perpCategories":
			fmt.Fprint(w, `[["xyz:GOOGL","stocks"],["cash:TSLA","stocks"]]`)
		case "metaAndAssetCtxs":
			switch q["dex"] {
			case "":
				fmt.Fprint(w, `[{"universe":[{"name":"BTC"}]},[{"dayNtlVlm":"10000000","markPx":"80000"}]]`)
			case "xyz":
				fmt.Fprint(w, `[{"collateralToken":0,"universe":[{"name":"xyz:GOOGL"},{"name":"xyz:OLD","isDelisted":true}]},[{"dayNtlVlm":"20000000","markPx":"350"},{"dayNtlVlm":"99999999"}]]`)
			case "cash":
				fmt.Fprint(w, `[{"collateralToken":0,"universe":[{"name":"cash:TSLA"}]},[{"dayNtlVlm":"30000000","markPx":"400"}]]`)
			default:
				http.Error(w, "unavailable", 503)
			}
		default:
			t.Error("unexpected endpoint", q)
		}
	}))
	defer server.Close()
	c := New()
	c.Hyperliquid = server.URL
	c.HTTP = server.Client()
	list, err := c.Markets(context.Background(), "hyperliquid")
	if err != nil || len(list.Markets) != 3 || len(list.Warnings) != 1 {
		t.Fatalf("partial coverage hidden: %+v %v", list, err)
	}
	if list.Markets[0].Symbol != "cash:TSLA" || list.Markets[0].ExecutionSupported || list.Markets[1].Symbol != "xyz:GOOGL" || !list.Markets[1].ExecutionSupported || list.Markets[1].Category != "stocks" {
		t.Fatal(list.Markets)
	}
}

// Opt-in public data check; this client has no credentials or order API.
func TestPublicDiscoveryLiveSmoke(t *testing.T) {
	if os.Getenv("NOFX_PUBLIC_MARKET_SMOKE") != "1" {
		t.Skip("set NOFX_PUBLIC_MARKET_SMOKE=1 for public venue reads")
	}
	c := New()
	for _, venue := range []string{"binance", "hyperliquid"} {
		list, err := c.Markets(context.Background(), venue)
		if err != nil {
			t.Fatal(venue, err)
		}
		stocks := 0
		for _, m := range list.Markets {
			if m.Category == "stocks" {
				stocks++
			}
		}
		if len(list.Markets) == 0 || stocks == 0 {
			t.Fatalf("missing market/stock discovery for %s", venue)
		}
		t.Logf("%s: %d markets, %d stock contracts, %d coverage warnings; top %s", venue, len(list.Markets), stocks, len(list.Warnings), list.Markets[0].Symbol)
		if len(list.Warnings) > 0 {
			t.Log(list.Warnings)
		}
	}
}
