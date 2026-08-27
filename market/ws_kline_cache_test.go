package market

import (
	"testing"
	"time"
)

// Test that the cache returns (nil, false, nil) when the buffer is too thin
// — the caller is expected to fall back to REST in that case.
func TestTryGetKlinesViaWS_BufferTooThinReturnsFalse(t *testing.T) {
	// Use a fresh key; the cache should have no entries → false.
	exchange := "fake-exchange-no-ws-client-" + t.Name()
	symbol := "BTCUSDT"
	interval := "1m"

	klines, ok, err := TryGetKlinesViaWS(exchange, symbol, interval, 50)
	if err != nil {
		t.Fatalf("expected nil err when buffer missing, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false when cache empty")
	}
	if len(klines) != 0 {
		t.Fatal("expected empty klines slice")
	}
}

// Test that an entry that has accumulated enough closed candles returns
// (klines, true, nil) with a defensive copy (mutating the returned slice
// must not disturb the cache).
func TestTryGetKlinesViaWS_EnoughCandles(t *testing.T) {
	cache := getWsKlineCache()
	key := wsCacheKey{exchange: "test-exch", symbol: "BTCUSDT", interval: "1m"}

	entry := &wsCacheEntry{}
	cache.mu.Lock()
	cache.entries[key] = entry
	cache.mu.Unlock()

	entry.mu.Lock()
	now := time.Now().UnixMilli()
	for i := 0; i < 60; i++ {
		entry.klines = append(entry.klines, Kline{
			OpenTime: now - int64((60-i)*60_000),
			Open:     float64(100 + i),
			Close:    float64(101 + i),
			Volume:   10,
		})
	}
	entry.subscribed = true
	entry.mu.Unlock()

	klines, ok, err := TryGetKlinesViaWS("test-exch", "BTCUSDT", "1m", 50)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true when buffer has 60 candles and limit 50")
	}
	if len(klines) != 50 {
		t.Fatalf("expected 50 klines, got %d", len(klines))
	}
	// i=0 is the oldest (now - 60 minutes), i=59 is the newest (now - 1 minute).
	// Last 50 = i=10..59, so first of last 50 = now - 50*60_000.
	if klines[0].OpenTime != now-50*60_000 {
		t.Fatalf("expected tail-aligned slice; got first OpenTime=%d want %d", klines[0].OpenTime, now-50*60_000)
	}

	// Mutate the returned slice — cache must not be affected.
	klines[0].Close = 999.99
	entry.mu.RLock()
	if entry.klines[len(entry.klines)-50].Close == 999.99 {
		t.Fatal("defensive copy failed: cache was mutated through returned slice")
	}
	entry.mu.RUnlock()
}

func TestAppendUpdate_DedupSameOpenTime(t *testing.T) {
	cache := getWsKlineCache()
	key := wsCacheKey{exchange: "ex", symbol: "ETHUSDT", interval: "1m"}
	entry := &wsCacheEntry{}
	cache.mu.Lock()
	cache.entries[key] = entry
	cache.mu.Unlock()

	ot := time.Now().UnixMilli()
	// Initial append.
	cache.appendUpdate(key, KlineUpdate{OpenTime: ot, Close: 1.0})
	cache.appendUpdate(key, KlineUpdate{OpenTime: ot, Close: 2.0})
	cache.appendUpdate(key, KlineUpdate{OpenTime: ot + 60_000, Close: 3.0})

	entry.mu.RLock()
	defer entry.mu.RUnlock()
	if len(entry.klines) != 2 {
		t.Fatalf("expected 2 klines after dedup, got %d", len(entry.klines))
	}
	if entry.klines[0].Close != 2.0 {
		t.Fatalf("dedup should keep latest (close=2.0), got %v", entry.klines[0].Close)
	}
}

func TestTrimTail(t *testing.T) {
	k := make([]Kline, 600)
	for i := range k {
		k[i].OpenTime = int64(i)
	}
	k = trimTail(k, 500)
	if len(k) != 500 {
		t.Fatalf("expected len 500, got %d", len(k))
	}
	if k[0].OpenTime != 100 || k[499].OpenTime != 599 {
		t.Fatalf("trim kept wrong window: first=%d last=%d", k[0].OpenTime, k[499].OpenTime)
	}
}

func TestNormalizeExchange(t *testing.T) {
	cases := map[string]string{
		"binance": "binance",
		"Binance": "binance",
		"bybit":   "bybit",
		"Bybit":   "bybit",
		"okx":     "okx",
	}
	for in, want := range cases {
		if got := normalizeExchange(in); got != want {
			t.Errorf("normalizeExchange(%q)=%q, want %q", in, got, want)
		}
	}
}
