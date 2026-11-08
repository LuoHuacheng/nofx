package kernel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"nofx/provider/localpool"
	"nofx/provider/nofxos"
	"nofx/store"
)

func TestAI500LocalFallbackRequiresExplicitOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		fallback     bool
	}{
		{"existing config", "{}", false},
		{"disabled", "{\"use_local_pool_fallback\":false}", false},
		{"enabled", "{\"use_local_pool_fallback\":true}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var localRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
			defer upstream.Close()
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				localRequests.Add(1)
				_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","priceChangePercent":"2","lastPrice":"100000","quoteVolume":"100000000","count":1000}]`))
			}))
			defer local.Close()
			cfg := &store.StrategyConfig{}
			if err := json.Unmarshal([]byte(tc.config), &cfg.CoinSource); err != nil {
				t.Fatal(err)
			}
			engine := &StrategyEngine{config: cfg, nofxosClient: nofxos.NewClient(upstream.URL, ""), localPool: localpool.NewPoolProvider(local.Client(), local.URL)}
			coins, err := engine.getAI500Coins(1)
			if !tc.fallback {
				if err == nil || len(coins) != 0 || localRequests.Load() != 0 {
					t.Fatalf("unrequested source switch: coins=%+v calls=%d error=%v", coins, localRequests.Load(), err)
				}
				return
			}
			if err != nil || len(coins) != 1 || coins[0].Symbol != "BTCUSDT" || len(coins[0].Sources) != 1 || coins[0].Sources[0] != "local_pool" || localRequests.Load() != 1 {
				t.Fatalf("explicit fallback failed: coins=%+v calls=%d error=%v", coins, localRequests.Load(), err)
			}
		})
	}
}
