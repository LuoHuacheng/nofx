package nofxos

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"nofx/mcp"
	"nofx/mcp/payment"
)

func TestPaidAI500NeverRetriesHTTPOrParsingFailures(t *testing.T) {
	for _, status := range []int{402, 429, 502, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			key, _ := crypto.GenerateKey()
			rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Method string
					Params []json.RawMessage
				}
				json.NewDecoder(r.Body).Decode(&q)
				result := "0x2105"
				if q.Method == "eth_call" {
					result = "0xf4240"
					if !strings.Contains(string(q.Params[0]), "0x70a08231") {
						t.Error("unexpected contract call")
					}
				} else if q.Method != "eth_chainId" {
					t.Error("non-read RPC")
				}
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%q}`, result)
			}))
			defer rpc.Close()
			t.Setenv("NOFX_X402_RECEIPT_RPC_URL", rpc.URL)
			quote, _ := json.Marshal(payment.X402v2PaymentRequired{X402Version: 2, Accepts: []payment.X402AcceptOption{{Scheme: "exact", Network: payment.BaseNetwork, Asset: payment.BaseUSDCContract, PayTo: "0x1111111111111111111111111111111111111111", Amount: "2000", MaxTimeoutSeconds: 300}}})
			var paid, unpaid atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Payment-Signature") == "" {
					unpaid.Add(1)
					w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString(quote))
					w.WriteHeader(402)
					return
				}
				paid.Add(1)
				w.WriteHeader(status)
				fmt.Fprint(w, `broken JSON after an attempted payment`)
			}))
			defer srv.Close()
			pc, err := NewClaw402DataClient(srv.URL, fmt.Sprintf("%x", crypto.FromECDSA(key)), mcp.NewNoopLogger())
			if err != nil {
				t.Fatal(err)
			}
			c := NewClient("", "")
			c.SetClaw402(pc)
			if _, err = c.GetAI500List(); err == nil {
				t.Fatal("bad paid response accepted")
			}
			if paid.Load() != 1 || unpaid.Load() != 1 {
				t.Fatalf("paid=%d unpaid=%d; outer layer retried payment", paid.Load(), unpaid.Load())
			}
		})
	}
}

func TestAutomaticClientNeverFallsBackToOrigin(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	for _, key := range []string{"", "not-a-key"} {
		c := ResolveClient(key)
		if c.gatewayErr == nil {
			t.Fatal("missing/invalid wallet permits a direct origin fallback")
		}
		if _, err := c.doRequest("/api/ai500/list"); err == nil {
			t.Fatal("misconfigured client made request")
		}
	}
}

func TestLegacyConstructorsCannotBypassPaidGateway(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"success":true}`)
	}))
	defer origin.Close()
	for _, key := range []string{"", "legacy-credential-fixture"} {
		client := NewClient(origin.URL, key)
		client.SetConfig(origin.URL, key)
		if _, err := client.doRequest("/api/ai500/list"); err == nil || !strings.Contains(err.Error(), "direct origin access is disabled") {
			t.Fatal("legacy constructor allowed unsigned origin access", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("unpaid origin was contacted")
	}
	if DefaultAuthKey != "" {
		t.Fatal("a built-in data credential is present")
	}
}

func TestLegacyAuthRemovalPreservesFiltersAndNormalizesKey(t *testing.T) {
	key, _ := crypto.GenerateKey()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/v1/nofx/ai500/list" || r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("duration") != "1h" || r.URL.Query().Has("auth") {
			t.Errorf("bad sanitized query %s", r.URL.String())
		}
		w.WriteHeader(400)
	}))
	defer srv.Close()
	c, err := NewClaw402DataClient(srv.URL, " 0X"+fmt.Sprintf("%x", crypto.FromECDSA(key))+" ", mcp.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.DoRequest("/api/ai500/list?auth=legacy&limit=50&duration=1h"); err == nil {
		t.Fatal("expected mock error")
	}
	if requests.Load() != 1 {
		t.Fatal("unexpected retry")
	}
}
