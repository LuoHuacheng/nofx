package payment

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
)

func TestClaw402RequiresAllowanceAndNeverApprovesOrDowngrades(t *testing.T) {
	key, _ := crypto.GenerateKey()
	client := &Claw402Client{privateKey: key}
	var allowed atomic.Bool
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		result := "0x0"
		switch request.Method {
		case "eth_chainId":
			result = "0x2105"
		case "eth_call":
			if len(request.Params) == 2 && strings.Contains(string(request.Params[0]), "0x70a08231") {
				result = "0xf4240"
				break
			}
			if len(request.Params) != 2 || !strings.Contains(string(request.Params[0]), "0xdd62ed3e") {
				t.Error("not an allowance read")
			}
			if allowed.Load() {
				result = "0xf4240"
			}
		default:
			t.Errorf("unexpected method (must not approve): %s", request.Method)
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%q}`, result)
	}))
	defer rpc.Close()
	t.Setenv("NOFX_X402_RECEIPT_RPC_URL", rpc.URL)
	opt := X402AcceptOption{Scheme: "upto", Network: BaseNetwork, Asset: BaseUSDCContract, Amount: "1000000", PayTo: "0x1111111111111111111111111111111111111111", MaxTimeoutSeconds: 660, Extra: map[string]string{"facilitatorAddress": "0x2222222222222222222222222222222222222222"}}
	header := func() string {
		raw, _ := json.Marshal(X402v2PaymentRequired{X402Version: 2, Accepts: []X402AcceptOption{opt}})
		return base64.StdEncoding.EncodeToString(raw)
	}
	if signed, err := client.signPayment(header()); err == nil || signed != "" {
		t.Fatal("signed without allowance")
	}
	allowed.Store(true)
	if signed, err := client.signPayment(header()); err != nil || signed == "" {
		t.Fatalf("valid allowance failed: %v", err)
	}
	opt.Scheme = "exact"
	if signed, err := client.signPayment(header()); err == nil || signed != "" {
		t.Fatal("downgraded to fixed price")
	}
}
