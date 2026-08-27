package backtest

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"sync"
)

// AICache deduplicates identical decision contexts (§2.3 / P3.3): the
// decision loop fires once per bar event, but a context whose klines,
// positions and account are unchanged since the last call produces the
// same decision without paying for an LLM call. The key is a hash of
// the full serialized context.
type AICache struct {
	mu      sync.Mutex
	entries map[string][]byte // key -> serialized decision payload
	max     int
}

// NewAICache creates a cache holding at most max entries.
func NewAICache(max int) *AICache {
	if max <= 0 {
		max = 1000
	}
	return &AICache{entries: make(map[string][]byte), max: max}
}

// CacheKey computes the decision-cache key for a context. It must
// capture everything that can change the decision: prices, positions,
// account equity/balance, kline windows and the call identity.
func CacheKey(tag string, prices map[string]float64, positions []string, accountEquity float64, klineDigest string) string {
	h := sha256.New()
	h.Write([]byte(tag))
	for _, sym := range sortedKeys(prices) {
		h.Write([]byte(sym))
		h.Write([]byte{'='})
		h.Write([]byte(formatFloat(prices[sym])))
		h.Write([]byte{';'})
	}
	for _, p := range positions {
		h.Write([]byte(p))
		h.Write([]byte{';'})
	}
	h.Write([]byte(formatFloat(accountEquity)))
	h.Write([]byte{':'})
	h.Write([]byte(klineDigest))
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns the cached payload for key (nil if absent).
func (c *AICache) Get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[key]
}

// Set stores a payload under key.
func (c *AICache) Set(key string, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		// Simple eviction: drop arbitrary entries once full.
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = payload
}

// Size returns the number of cached entries.
func (c *AICache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// formatFloat renders a float deterministically (no exponent noise).
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 6, 64)
}
