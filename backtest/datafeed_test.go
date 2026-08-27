package backtest

import (
	"testing"
	"time"
)

func mkBar(min int, close float64) Bar {
	return Bar{Time: time.Date(2026, 1, 1, 0, min, 0, 0, time.UTC), Open: close - 1, High: close + 1, Low: close - 2, Close: close, Volume: 100}
}

func TestMemoryFeedOuterJoin(t *testing.T) {
	end := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	bars := map[string][]Bar{
		// BTC: a 5m bar every 5 minutes
		"BTCUSDT": {mkBar(5, 100), mkBar(10, 101), mkBar(15, 102)},
		// ETH: only two bars, unsynchronized
		"ETHUSDT": {mkBar(7, 50), mkBar(14, 51)},
	}
	feed := NewMemoryFeed([]string{"BTCUSDT", "ETHUSDT"}, bars, end, nil, nil)

	// First bucket is the earliest bar (0:05).
	t0, nb, ok := feed.Next()
	if !ok || !t0.Equal(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)) {
		t.Fatalf("first bucket = %v (ok=%v), want 00:05", t0, ok)
	}
	if len(nb) != 1 || nb[0] != "BTCUSDT" {
		t.Fatalf("new bars at 00:05 = %v, want [BTCUSDT]", nb)
	}
	if feed.LastPrice("BTCUSDT") != 100 {
		t.Fatalf("BTC price = %v, want 100", feed.LastPrice("BTCUSDT"))
	}
	if feed.LastPrice("ETHUSDT") != 0 {
		t.Fatalf("ETH price before its first bar = %v, want 0 (no lookahead)", feed.LastPrice("ETHUSDT"))
	}

	// 00:06, 00:07 -> ETH bar
	_, _, ok = feed.Next()
	if !ok {
		t.Fatal("feed exhausted too early")
	}
	t2, nb2, ok := feed.Next()
	if !ok || len(nb2) != 1 || nb2[0] != "ETHUSDT" {
		t.Fatalf("bucket %v new bars = %v, want [ETHUSDT]", t2, nb2)
	}
	// ETH price should now be 50 (bar at 00:07 close)
	if feed.LastPrice("ETHUSDT") != 50 {
		t.Fatalf("ETH price = %v, want 50", feed.LastPrice("ETHUSDT"))
	}

	// Walk to 00:15: BTC bar at 00:15
	found := false
	for i := 0; i < 20; i++ {
		bt, nb, ok := feed.Next()
		if !ok {
			break
		}
		if bt.Equal(time.Date(2026, 1, 1, 0, 15, 0, 0, time.UTC)) {
			if len(nb) != 1 || nb[0] != "BTCUSDT" {
				t.Fatalf("00:15 new bars = %v", nb)
			}
			if feed.LastPrice("BTCUSDT") != 102 {
				t.Fatalf("BTC price = %v, want 102", feed.LastPrice("BTCUSDT"))
			}
			found = true
		}
	}
	if !found {
		t.Fatal("00:15 bucket not seen")
	}
}

func TestMemoryFeedHistoryAndFunding(t *testing.T) {
	end := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	bars := map[string][]Bar{
		"BTCUSDT": {mkBar(5, 100), mkBar(10, 101), mkBar(15, 102)},
	}
	funding := map[string][]FundingRate{
		"BTCUSDT": {{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Rate: 0.0001}},
	}
	feed := NewMemoryFeed([]string{"BTCUSDT"}, bars, end, funding, nil)

	hist := feed.History("BTCUSDT", time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC))
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2 (bars at 00:05 and 00:10)", len(hist))
	}
	if feed.FundingRate("BTCUSDT", time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)) != 0.0001 {
		t.Fatalf("funding = %v, want 0.0001", feed.FundingRate("BTCUSDT", end))
	}
	if feed.FundingRate("BTCUSDT", time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC)) != 0 {
		t.Fatal("funding before first sample should be 0")
	}
	// Exhaustion
	for i := 0; i < 100; i++ {
		if _, _, ok := feed.Next(); !ok {
			return
		}
	}
	t.Fatal("feed never exhausted")
}

func TestParseTimeframe(t *testing.T) {
	cases := map[string]int64{
		"1m": 60, "5m": 300, "1h": 3600, "1d": 86400,
	}
	for in, want := range cases {
		d, err := ParseTimeframe(in)
		if err != nil || d.Seconds() != float64(want) {
			t.Fatalf("ParseTimeframe(%q) = %v, %v", in, d, err)
		}
	}
	if _, err := ParseTimeframe("7m"); err == nil {
		t.Fatal("expected error for 7m")
	}
}

func TestValidateRunConfig(t *testing.T) {
	base := RunConfig{
		Symbols:        []string{"BTCUSDT"},
		Timeframe:      "5m",
		StartTime:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:        time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		InitialBalance: 1000,
	}
	if err := ValidateRunConfig(base, nil); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := base
	bad.EndTime = base.StartTime.Add(100 * 24 * time.Hour)
	if err := ValidateRunConfig(bad, nil); err == nil {
		t.Fatal("span over 90 days should be rejected")
	}
	bad2 := base
	bad2.Symbols = make([]string, MaxSymbolsPerRun+1)
	if err := ValidateRunConfig(bad2, nil); err == nil {
		t.Fatal("too many symbols should be rejected")
	}
	bad3 := base
	bad3.InitialBalance = 0
	if err := ValidateRunConfig(bad3, nil); err == nil {
		t.Fatal("zero balance should be rejected")
	}
}
