package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHyperdataTypedViewsAndFailureStates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	board := map[string]any{"market": map[string]any{"chain": "mainnet", "marketType": "hip3_perp", "marketId": "hip3_perp:0x88806a71d74ad0a510b350545c9ae490912f0888:xyz:GOOGL"}, "windowDays": 7, "limit": 100, "minNetPnl": "10000", "state": "ready", "windowStart": now.Add(-7 * 24 * time.Hour).Format(time.RFC3339), "asOf": now.Format(time.RFC3339), "coveredHours": 168, "expectedHours": 168, "qualifiedAddresses": 1, "items": []ProfitEntry{{Rank: 1, Address: "0x1111111111111111111111111111111111111111", Net: "12000", Realized: "12100", Fees: "200", Funding: "100", Volume: "30000"}}}
	var requestPath string
	var response any = board
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		if r.URL.Query().Get("chain") != "mainnet" {
			t.Error("wrong chain")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": response, "meta": map[string]any{"coverage": "partial", "asOfTime": time.Now().UTC().Format(time.RFC3339)}})
	}))
	defer server.Close()
	h := fixtureHyperdata(server)
	q := Request{Venue: "hyperliquid", Symbol: "xyz:GOOGL"}
	registry, err := Open("", h.Definitions()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Read(context.Background(), "hyperdata_profit", q); err == nil {
		t.Fatal("default-disabled reader ran")
	}
	if err = registry.SetEnabled("hyperdata_profit", true); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Read(context.Background(), "hyperdata_profit", q)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Visual.Entries) != 1 || r.Visual.Coverage != "partial" || !strings.Contains(requestPath, "/hip3_perp/xyz:GOOGL/") {
		t.Fatal("lost native identity or coverage")
	}
	if ModelContext(r).Visual != nil || len(ModelContext(r).Metrics) == 0 {
		t.Fatal("model must get bounded summary, not chart/address arrays")
	}
	rows := board["items"].([]ProfitEntry)
	rows[0].Fees = "201"
	board["items"] = rows
	if _, err = h.Read(context.Background(), "hyperdata_profit", q); err == nil {
		t.Fatal("unreconciled PnL accepted")
	}
	board["state"] = "building"
	board["items"] = []ProfitEntry{}
	board["coveredHours"] = 20
	r, err = h.Read(context.Background(), "hyperdata_profit", q)
	if err != nil || r.Visual.State != "building" || len(r.Visual.Entries) != 0 {
		t.Fatalf("building board misrepresented: %v", err)
	}
	board["windowDays"] = 30
	board["expectedHours"] = 720
	board["windowStart"] = now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	month := q
	month.Window = "30d"
	if _, err = h.Read(context.Background(), "hyperdata_profit", month); err != nil {
		t.Fatalf("30d window failed: %v", err)
	}
	if _, err = h.Read(context.Background(), "hyperdata_profit", q); err == nil {
		t.Fatal("wrong requested window accepted")
	}
	board["windowDays"] = 7
	board["expectedHours"] = 168
	board["windowStart"] = now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	board["market"] = map[string]any{"chain": "mainnet", "marketType": "hip3_perp", "marketId": "hip3_perp:0x88806a71d74ad0a510b350545c9ae490912f0888:abc:GOOGL"}
	if _, err = h.Read(context.Background(), "hyperdata_profit", q); err == nil {
		t.Fatal("different dex accepted")
	}
	response = map[string]any{"market": map[string]any{"chain": "mainnet", "marketType": "hip3_perp", "marketId": "xyz:GOOGL"}, "markPrice": 355, "costAddrs": 2, "liqAddrs": 1, "bins": []CostBin{{Px: 350, LongCost: 12, ShortLiq: 4}}, "cost": map[string]string{"state": "complete"}, "liquidation": map[string]string{"state": "partial"}}
	r, err = h.Read(context.Background(), "hyperdata_cost", q)
	if err != nil || r.Visual.Bins[0].LongCost != 12 {
		t.Fatalf("cost view failed %v", err)
	}
	response = map[string]any{"markPrice": "355", "positionsAsOf": time.Now().UTC().Format(time.RFC3339), "asOf": time.Now().UTC().Format(time.RFC3339), "coverage": "partial", "winBins": 2, "costBins": 2, "included": HeatTotal{1, "100"}, "total": HeatTotal{2, "200"}, "minRoundTrips": 20, "viewport": map[string]int{"WinMin": 0, "WinMax": 100, "CostMin": 60, "CostMax": 140}, "cells": []HeatCell{{Row: 0, Column: 1, Long: HeatTotal{1, "100"}, Short: HeatTotal{0, "0"}}}}
	r, err = h.Read(context.Background(), "hyperdata_winrate", q)
	if err != nil || len(r.Visual.Cells) != 1 {
		t.Fatalf("heatmap failed %v", err)
	}
	encoded, _ := json.Marshal(r.Visual)
	if !strings.Contains(string(encoded), `"win_min":0`) || r.Visual.Mark != 355 {
		t.Fatal("zero axis boundary or reference price omitted")
	}
	if _, err = h.Read(context.Background(), "hyperdata_cost", Request{Venue: "binance", Symbol: "GOOGLUSDT"}); err == nil {
		t.Fatal("cross-venue substitution")
	}
	if _, err = (Hyperdata{Offline: true}).Read(context.Background(), "hyperdata_cost", q); err == nil {
		t.Fatal("demo mixed real data")
	}
}
func TestHyperdataUpstreamUnavailable(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(503)
		w.Write([]byte(`{"error":{"code":"data_unavailable","message":"Position distribution is being prepared","details":{"reason":"map_building"}}}`))
	}))
	defer s.Close()
	_, err := (fixtureHyperdata(s)).Read(context.Background(), "hyperdata_winrate", Request{Venue: "hyperliquid", Symbol: "BTC"})
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Reason != "map_building" || upstream.RetryAfter != 0 {
		t.Fatal("upstream retry contract lost", err)
	}
	if err == nil || !strings.Contains(err.Error(), "data_unavailable") {
		t.Fatal("upstream failure lost")
	}
}

func fixtureHyperdata(s *httptest.Server) Hyperdata {
	return Hyperdata{Fetch: func(ctx context.Context, id string, q Request) ([]byte, error) {
		// The fixture transport exercises decoding only; production uses Claw402.
		req, err := http.NewRequestWithContext(ctx, "GET", s.URL+"/hip3_perp/"+q.Symbol+"/?chain=mainnet", nil)
		if err != nil {
			return nil, err
		}
		r, err := s.Client().Do(req)
		if err != nil {
			return nil, err
		}
		defer r.Body.Close()
		return io.ReadAll(r.Body)
	}}
}
func TestPaidProviderRequiredNoPublicFallback(t *testing.T) {
	if _, err := (Hyperdata{}).Read(context.Background(), "hyperdata_cost", Request{Venue: "hyperliquid", Symbol: "BTC"}); err == nil {
		t.Fatal("missing paid provider accepted")
	}
}
