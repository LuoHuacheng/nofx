package localpool

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const sampleTickers = `[
  {"symbol":"BTCUSDT","priceChangePercent":"2.5","lastPrice":"68000","quoteVolume":"2500000000","count":950000},
  {"symbol":"SHIBUSDT","priceChangePercent":"15.0","lastPrice":"0.00002","quoteVolume":"800000000","count":700000},
  {"symbol":"SOLUSDT","priceChangePercent":"-3.0","lastPrice":"150","quoteVolume":"600000000","count":300000},
  {"symbol":"PUMPUSDT","priceChangePercent":"8.0","lastPrice":"0.9","quoteVolume":"1000000","count":5000},
  {"symbol":"BTCUSDT_240630","priceChangePercent":"9.0","lastPrice":"1","quoteVolume":"500000000","count":100000}
]`

func TestGetTopRatedCoins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/ticker/24hr" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(sampleTickers))
	}))
	defer srv.Close()

	p := NewPoolProvider(nil, srv.URL)
	got, err := p.GetTopRatedCoins(2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Top2 by score: SHIB (huge momentum+volume) then BTC (liquidity king).
	want := []string{"SHIBUSDT", "BTCUSDT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// PUMPUSDT (low quote volume) and the non-perp contract must be filtered out.
	all, err := p.GetTopRatedCoins(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, sym := range all {
		if sym == "PUMPUSDT" || sym == "BTCUSDT_240630" {
			t.Fatalf("filtered symbol leaked into pool: %s", sym)
		}
	}
}

func TestRankDegenerateRange(t *testing.T) {
	// All identical values must not produce NaN/panic; single candidate returns itself.
	got := rankTickers([]ticker{
		{Symbol: "XUSDT", PriceChangePercent: "0", LastPrice: "1", QuoteVolume: "100000000", Count: 1000},
		{Symbol: "YUSDT", PriceChangePercent: "0", LastPrice: "1", QuoteVolume: "100000000", Count: 1000},
	}, 1)
	if len(got) != 1 || got[0].symbol != "XUSDT" {
		t.Fatalf("degenerate range: got %+v", got)
	}
}
