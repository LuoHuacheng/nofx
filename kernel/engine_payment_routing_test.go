package kernel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"nofx/store"
)

func TestStrategyDataCannotFallBackToOriginWithoutFeeWallet(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	var originRequests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		w.WriteHeader(400)
	}))
	defer origin.Close()
	for _, key := range []string{"", " ", "invalid-key"} {
		cfg := store.GetDefaultStrategyConfig("en")
		cfg.Indicators.NofxOSAPIKey = "legacy-key-must-not-enable-fallback"
		engine := NewStrategyEngine(&cfg, key)
		// Make any accidental origin fallback observable without external access.
		engine.nofxosClient.SetConfig(origin.URL, "legacy-key")
		_, err := engine.nofxosClient.GetAI500List()
		if err == nil || !strings.Contains(err.Error(), "Claw402") {
			t.Fatalf("missing paid-data configuration error: %v", err)
		}
	}
	if originRequests.Load() != 0 {
		t.Fatalf("strategy bypassed payment: %d origin requests", originRequests.Load())
	}
}

func TestStrategyDataUsesConfiguredGateway(t *testing.T) {
	key, _ := crypto.GenerateKey()
	for _, useEnv := range []bool{false, true} {
		t.Run(fmt.Sprint(useEnv), func(t *testing.T) {
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/api/v1/nofx/ai500/list" || r.URL.Query().Has("auth") {
					t.Errorf("bad gateway route: %s", r.URL)
				}
				w.WriteHeader(400) // No signature, payment or external request.
			}))
			defer gateway.Close()
			t.Setenv("CLAW402_URL", gateway.URL)
			hexKey := fmt.Sprintf("%x", crypto.FromECDSA(key))
			t.Setenv("CLAW402_WALLET_KEY", "")
			argument := hexKey
			if useEnv {
				t.Setenv("CLAW402_WALLET_KEY", hexKey)
				argument = ""
			}
			cfg := store.GetDefaultStrategyConfig("en")
			engine := NewStrategyEngine(&cfg, argument)
			if _, err := engine.nofxosClient.GetAI500List(); err == nil {
				t.Fatal("expected mock rejection")
			}
			if requests.Load() != 1 {
				t.Fatalf("gateway requests = %d", requests.Load())
			}
		})
	}
}
