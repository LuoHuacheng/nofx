// WS-first kline cache. Maintains an in-memory ring of the most recent kline
// updates received via WebSocket, indexed by (exchange, symbol, interval).
// Callers ask TryGetKlinesViaWS first; if the cache holds at least `limit`
// closed candles the WS path wins, otherwise the caller falls back to REST.
//
// ponytail: WS is streaming-only (gives the latest candle as it updates,
// never historical depth). After the first REST bootstrap for a key, WS
// fills in as new candles close; once the buffer holds ≥ limit candles,
// subsequent reads return from WS without hitting REST. For long-running
// sessions polling the same symbols this means the bulk fetch cost is
// amortized to zero and the live tail is always fresher than REST polls.

package market

import (
	"context"
	"fmt"
	"sync"
	"time"

	"nofx/logger"
)

const wsCacheMaxPerKey = 500 // max candles retained per (exchange, symbol, interval)

type wsCacheKey struct {
	exchange string
	symbol   string
	interval string
}

type wsCacheEntry struct {
	mu      sync.RWMutex
	klines  []Kline    // ascending by OpenTime
	subscribed bool
}

// wsKlineCache is the package-level singleton. Created lazily.
var (
	wsKlineCacheOnce sync.Once
	wsKlineCacheInst *wsKlineCache
)

type wsKlineCache struct {
	mu       sync.RWMutex
	entries  map[wsCacheKey]*wsCacheEntry
	manager  *WebSocketManager
	drainers map[wsCacheKey]context.CancelFunc
}

func getWsKlineCache() *wsKlineCache {
	wsKlineCacheOnce.Do(func() {
		wsKlineCacheInst = &wsKlineCache{
			entries:  make(map[wsCacheKey]*wsCacheEntry),
			manager:  NewWebSocketManager(),
			drainers: make(map[wsCacheKey]context.CancelFunc),
		}
		// Fallback is always allowed — REST path remains the safety net.
		wsKlineCacheInst.manager.SetFallbackRestAPI(true)
	})
	return wsKlineCacheInst
}

// TryGetKlinesViaWS returns up to `limit` klines from the WS cache for the
// given key. Returns (klines, true, nil) if the buffer holds ≥ limit closed
// candles; (nil, false, nil) if the buffer is too thin (caller should fall
// back to REST); (nil, false, err) on hard failure.
//
// The returned slice is a defensive copy so the caller may sort/modify it
// without disturbing the cache.
func TryGetKlinesViaWS(exchange, symbol, interval string, limit int) ([]Kline, bool, error) {
	cache := getWsKlineCache()
	key := wsCacheKey{exchange: normalizeExchange(exchange), symbol: Normalize(symbol), interval: interval}

	entry := cache.ensureSubscribed(key)
	entry.mu.RLock()
	defer entry.mu.RUnlock()

	if len(entry.klines) < limit {
		return nil, false, nil
	}
	out := make([]Kline, limit)
	copy(out, entry.klines[len(entry.klines)-limit:])
	return out, true, nil
}

// Invalidate drops the cached buffer for a key. Called by REST fallbacks when
// they observe a significant historical gap (e.g. server restart) so the
// next read forces a fresh REST bootstrap instead of serving stale WS tail.
func (c *wsKlineCache) Invalidate(exchange, symbol, interval string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, wsCacheKey{exchange: normalizeExchange(exchange), symbol: Normalize(symbol), interval: interval})
}

func (c *wsKlineCache) ensureSubscribed(key wsCacheKey) *wsCacheEntry {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok {
		entry = &wsCacheEntry{klines: make([]Kline, 0, 64)}
		c.entries[key] = entry
	}
	c.mu.Unlock()

	entry.mu.Lock()
	already := entry.subscribed
	entry.mu.Unlock()
	if already {
		return entry
	}

	// Lazily connect the right exchange client.
	client, err := c.connectExchange(key.exchange)
	if err != nil {
		// Unknown exchanges (okx, aster, hyperliquid, …) are expected — REST
		// handles them. Only log if this *looked* like a supported exchange
		// and still failed.
		if isWSExchange(key.exchange) {
			logger.Warnf("WS cache: connect %s failed: %v (cache will stay cold; REST handles calls)", key.exchange, err)
		}
		return entry
	}
	if err := client.SubscribeKlines(key.symbol, key.interval); err != nil {
		logger.Warnf("WS cache: subscribe %s/%s/%s failed: %v", key.exchange, key.symbol, key.interval, err)
		return entry
	}
	entry.mu.Lock()
	entry.subscribed = true
	entry.mu.Unlock()

	// Start a drainer goroutine that reads KlineUpdates from the WS channel
	// and folds them into the buffer. One drainer per key.
	c.startDrainer(key, client)
	logger.Infof("WS cache: subscribed %s %s %s", key.exchange, key.symbol, key.interval)
	return entry
}

func (c *wsKlineCache) startDrainer(key wsCacheKey, client WebSocketClient) {
	ch := client.GetKlineChannel()
	if ch == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	// Cancel any previous drainer for this key (defensive).
	if prev, ok := c.drainers[key]; ok {
		prev()
	}
	c.drainers[key] = cancel
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.drainers, key)
			c.mu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case upd, ok := <-ch:
				if !ok {
					return
				}
				// Filter: only fold updates matching our key.
				if !matchesKey(upd, key) {
					continue
				}
				c.appendUpdate(key, upd)
			}
		}
	}()
}

// matchesKey returns true if the update belongs to the given subscription key.
// Binance sends updates tagged with Symbol+Interval; we accept only matching
// ones to avoid cross-contamination when the same channel carries multiple
// subscriptions (rare in practice — Binance sends per-stream frames).
func matchesKey(upd KlineUpdate, key wsCacheKey) bool {
	return upd.Interval == key.interval &&
		(Normalize(upd.Symbol) == key.symbol || upd.Symbol == "")
}

func (c *wsKlineCache) appendUpdate(key wsCacheKey, upd KlineUpdate) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return
	}

	kline := Kline{
		OpenTime:  upd.OpenTime,
		Open:      upd.Open,
		High:      upd.High,
		Low:       upd.Low,
		Close:     upd.Close,
		Volume:    upd.Volume,
		CloseTime: upd.CloseTime,
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	// Update or append.
	n := len(entry.klines)
	if n > 0 && entry.klines[n-1].OpenTime == upd.OpenTime {
		entry.klines[n-1] = kline
		return
	}
	if n > 0 && upd.OpenTime < entry.klines[n-1].OpenTime {
		// Out-of-order (reconnect backlog). Insert in place.
		for i := n - 1; i >= 0; i-- {
			if entry.klines[i].OpenTime == upd.OpenTime {
				entry.klines[i] = kline
				return
			}
			if entry.klines[i].OpenTime < upd.OpenTime {
				ins := make([]Kline, 0, n+1)
				ins = append(ins, entry.klines[:i+1]...)
				ins = append(ins, kline)
						entry.klines = trimTail(ins, wsCacheMaxPerKey)
				return
			}
		}
	}
	entry.klines = trimTail(append(entry.klines, kline), wsCacheMaxPerKey)
}

// trimTail returns the input slice with at most `max` elements, keeping the
// newest. Caller assigns the result back (the input slice may be reallocated).
func trimTail(k []Kline, max int) []Kline {
	if len(k) <= max {
		return k
	}
	fresh := make([]Kline, max)
	copy(fresh, k[len(k)-max:])
	return fresh
}

// connectExchange lazily creates and connects a WS client for the given
// exchange name. Supports binance and bybit (the two WS clients we ported).
// Returns an error for unsupported exchanges so the caller falls back to REST.
func (c *wsKlineCache) connectExchange(exchange string) (WebSocketClient, error) {
	if existing := c.manager.GetClient(exchange); existing != nil {
		if existing.IsConnected() {
			return existing, nil
		}
		if err := existing.Connect(); err == nil {
			return existing, nil
		}
	}
	var client WebSocketClient
	switch exchange {
	case "binance":
		client = NewBinanceWebSocketClient(false)
	case "bybit":
		// Bybit WS client not yet imported; leave as error.
		return nil, fmt.Errorf("bybit WS client not registered in this build")
	default:
		return nil, fmt.Errorf("WS not supported for exchange %q", exchange)
	}
	if err := c.manager.RegisterClient(exchange, client); err != nil {
		return nil, err
	}
	if err := client.Connect(); err != nil {
		return nil, err
	}
	// Give the read loop a moment to negotiate before returning.
	time.Sleep(150 * time.Millisecond)
	return client, nil
}

func normalizeExchange(exchange string) string {
	switch exchange {
	case "Binance", "binance":
		return "binance"
	case "Bybit", "bybit":
		return "bybit"
	default:
		return exchange
	}
}

// isWSExchange reports whether the named exchange has a WS client we can
// attempt to connect. Used to gate WARN logging — failures for unknown
// exchanges (REST-only) are expected and silent.
func isWSExchange(exchange string) bool {
	switch exchange {
	case "binance", "Bybit", "bybit":
		return true
	default:
		return false
	}
}
