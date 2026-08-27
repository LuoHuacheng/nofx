package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDIYQuantProvider_FetchesPublicBinanceData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fapi/v1/ticker/24hr" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","priceChange":"2","priceChangePercent":"5","lastPrice":"102","highPrice":"105","lowPrice":"98","volume":"1000","quoteVolume":"20000000","count":100}`))
			return
		}
		if r.URL.Path == "/fapi/v1/openInterest" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","openInterest":"500"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	data, err := NewDIYQuantProvider(server.Client(), server.URL).GetDIYQuantData(context.Background(), "btcusdt")
	if err != nil {
		t.Fatalf("GetDIYQuantData: %v", err)
	}
	if data.Symbol != "BTCUSDT" || data.Price.Current != 102 || data.OI.Current != 500 {
		t.Fatalf("unexpected data: %+v", data)
	}
	if data.MarketSentiment != "strongly_bullish" {
		t.Fatalf("sentiment=%q, want strongly_bullish", data.MarketSentiment)
	}
}

func TestDIYQuantProvider_RejectsMalformedNumericData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fapi/v1/ticker/24hr" {
			_, _ = w.Write([]byte(`{"lastPrice":"not-a-number"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := NewDIYQuantProvider(server.Client(), server.URL).GetDIYQuantData(context.Background(), "BTCUSDT")
	if err == nil {
		t.Fatal("expected malformed numeric data to fail")
	}
}
