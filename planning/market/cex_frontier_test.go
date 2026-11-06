package market

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"nofx/planning/plugins"
	"testing"
	"time"
)

func TestBitgetMarketUsesNativeDataAndPagination(t *testing.T) {
	now := time.Now().Unix() / 3600 * 3600
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Fatal("public data mutated")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/mix/market/contracts":
			fmt.Fprint(w, `{"code":"00000","data":[{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","symbolStatus":"normal","symbolType":"perpetual","sizeMultiplier":"0.0001","minTradeNum":"0.0001","minTradeUSDT":"5","pricePlace":"1","priceEndStep":"1"}]}`)
		case "/api/v2/mix/market/merge-depth":
			fmt.Fprintf(w, `{"code":"00000","data":{"bids":[[80000,1],[79999,2]],"asks":[[80000.1,1],[80001,2]],"ts":"%d"}}`, time.Now().UnixMilli())
		case "/api/v2/mix/market/history-candles":
			if r.URL.Query().Get("granularity") != "1H" || r.URL.Query().Get("limit") != "200" {
				t.Fatal("invalid candle mapping")
			}
			fmt.Fprintf(w, `{"code":"00000","data":[["%d","80000","81000","79000","80500","10","805000"]]}`, (now-7200)*1000)
		case "/api/v2/mix/market/tickers":
			fmt.Fprint(w, `{"code":"00000","data":[{"symbol":"BTCUSDT","lastPr":"80000","usdtVolume":"123456789"},{"symbol":"UNKNOWNUSDT","lastPr":"1","usdtVolume":"9999999999"}]}`)
		case "/api/v2/mix/market/current-fund-rate":
			fmt.Fprint(w, `{"code":"00000","data":[{"symbol":"BTCUSDT","fundingRate":"0.0001","fundingRateInterval":"8"}]}`)
		default:
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
	}))
	defer server.Close()
	c := New()
	c.Bitget = server.URL
	s, e := c.Snapshot(context.Background(), "bitget", "BTCUSDT")
	if e != nil || s.Tick != "0.100000000000" || len(s.Asks) != 2 || s.Ask != "80000.1" || s.Step != "0.0001" {
		t.Fatalf("snapshot %+v %v", s, e)
	}
	bars, e := c.Candles(context.Background(), CandleQuery{Venue: "bitget", Symbol: "BTCUSDT", Interval: "1h", To: now - 3600, Limit: 300})
	if e != nil || len(bars) != 1 || bars[0].Volume != "10" {
		t.Fatalf("candles %+v %v", bars, e)
	}
	markets, e := c.Markets(context.Background(), "bitget")
	if e != nil || len(markets.Markets) != 1 || markets.Markets[0].Volume24H != 123456789 {
		t.Fatalf("markets %+v %v", markets, e)
	}
	funding, e := c.PluginData(context.Background(), "funding", plugins.Request{Venue: "bitget", Symbol: "BTCUSDT"})
	if e != nil || len(funding.Metrics) != 1 {
		t.Fatalf("funding %+v %v", funding, e)
	}
}
func TestOKXDepthConvertsEveryContractLevel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v5/public/instruments" {
			fmt.Fprint(w, `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","state":"live","instType":"SWAP","ctType":"linear","ctVal":"0.01","ctValCcy":"BTC","ctMult":"1","settleCcy":"USDT","tickSz":"0.1","lotSz":"0.01","minSz":"0.01"}]}`)
			return
		}
		if r.URL.Query().Get("sz") != "100" {
			t.Fatal("insufficient depth")
		}
		fmt.Fprintf(w, `{"code":"0","data":[{"bids":[["80000","100","0","1"],["79999","200","0","1"]],"asks":[["80000.1","100","0","1"],["80001","200","0","1"]],"ts":"%d"}]}`, time.Now().UnixMilli())
	}))
	defer server.Close()
	c := New()
	c.OKX = server.URL
	s, e := c.Snapshot(context.Background(), "okx", "BTC-USDT-SWAP")
	if e != nil || len(s.Bids) != 2 || s.Bids[1].Quantity != "2.000000000000" {
		t.Fatalf("wrong base book %+v %v", s, e)
	}
}
