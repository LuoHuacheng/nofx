package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"nofx/mcp"
)

func TestQuotedBalancePreventsPaidSubmission(t *testing.T) {
	for _, scheme := range []string{"exact", "upto"} {
		for _, mode := range []string{"empty", "one-micro-short", "enough", "wrong-chain", "rpc-error", "malformed-balance"} {
			t.Run(scheme+"/"+mode, func(t *testing.T) {
				key, _ := crypto.GenerateKey()
				var signedRequests atomic.Int32
				rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var q struct {
						Method string
						Params []json.RawMessage
					}
					json.NewDecoder(r.Body).Decode(&q)
					result := "0x2105"
					if q.Method == "eth_chainId" && mode == "wrong-chain" {
						result = "0x1"
					}
					if q.Method == "eth_call" {
						result = "0xf4240"
						if strings.Contains(string(q.Params[0]), "0x70a08231") {
							switch mode {
							case "empty":
								result = "0x0"
							case "one-micro-short":
								result = "0x7cf"
							case "malformed-balance":
								result = "0xinvalid"
							}
						} else if !strings.Contains(string(q.Params[0]), "0xdd62ed3e") {
							t.Error("unexpected contract write/read")
						}
					} else if q.Method != "eth_chainId" {
						t.Errorf("unexpected RPC %s", q.Method)
					}
					if mode == "rpc-error" {
						w.WriteHeader(503)
						return
					}
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%q}`, result)
				}))
				defer rpc.Close()
				t.Setenv("NOFX_X402_RECEIPT_RPC_URL", rpc.URL)
				option := X402AcceptOption{Scheme: scheme, Network: BaseNetwork, Asset: BaseUSDCContract, PayTo: "0x1111111111111111111111111111111111111111", Amount: "2000", MaxTimeoutSeconds: 660, Extra: map[string]string{"facilitatorAddress": "0x2222222222222222222222222222222222222222"}}
				// Another network may be listed first; do not sign or reject the wrong rail.
				quote, _ := json.Marshal(X402v2PaymentRequired{X402Version: 2, Accepts: []X402AcceptOption{{Scheme: "exact", Network: "solana:test"}, option}})
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Payment-Signature") == "" {
						w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString(quote))
						w.WriteHeader(402)
						return
					}
					signedRequests.Add(1)
					raw, _ := X402DecodeHeader(r.Header.Get("Payment-Signature"))
					if scheme == "upto" {
						var envelope uptoEnvelope
						json.Unmarshal(raw, &envelope)
						if strings.HasPrefix(envelope.Payload.Authorization.Nonce, "0x") {
							t.Error("hex Permit2 nonce")
						}
						if !reflect.DeepEqual(envelope.Accepted, option) {
							t.Error("signed payment changed the gateway quote")
						}
					} else if !strings.Contains(string(raw), `"nonce":"0x`) {
						t.Error("exact nonce must remain hex")
					}
					w.Header().Set("X-Claw402-Payment-Stage", "verification_rejected")
					w.WriteHeader(402) // No live payment, even in the funded mock case.
				}))
				defer srv.Close()
				_, err := DoX402Request(context.Background(), srv.Client(), func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }, MakeClaw402SignFunc(key), "test", mcp.NewNoopLogger())
				if err == nil {
					t.Fatal("mock rejection accepted")
				}
				want := int32(0)
				if mode == "enough" {
					want = 1
				}
				if signedRequests.Load() != want {
					t.Fatalf("paid submissions %d, want %d", signedRequests.Load(), want)
				}
				var insufficient *ErrInsufficientFunds
				if (mode == "empty" || mode == "one-micro-short") && !errors.As(err, &insufficient) {
					t.Fatalf("missing actionable balance error: %v", err)
				}
				if mode == "enough" {
					var rejected *PaymentVerificationRejectedError
					if !errors.As(err, &rejected) {
						t.Fatalf("wrong error %v", err)
					}
				}
			})
		}
	}
}

func TestVerificationMarkerCannotHideReportedSettlement(t *testing.T) {
	for _, header := range []string{"Payment-Response", "X-Payment-Response"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Payment-Required", "quote")
			if r.Header.Get("Payment-Signature") != "" {
				w.Header().Set("X-Claw402-Payment-Stage", "verification_rejected")
				w.Header().Set(header, "reported-payment")
			}
			w.WriteHeader(402)
		}))
		_, err := DoX402Request(context.Background(), srv.Client(), func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }, func(string) (string, error) { return "signed", nil }, "test", mcp.NewNoopLogger())
		srv.Close()
		var unknown *PaymentOutcomeUnknownError
		if !errors.As(err, &unknown) {
			t.Fatalf("ambiguous settlement hidden: %v", err)
		}
	}
}

func TestUnknownModelDoesNotSilentlyChargeForAnotherModel(t *testing.T) {
	c := NewClaw402ClientWithOptions().(*Claw402Client)
	c.SetAPIKey(claw402TestPrivateKey, "", "glm-5.3")
	if c.resolveEndpoint() != "" {
		t.Fatal("unknown model silently mapped")
	}
	if _, err := c.Call("system", "user"); err == nil {
		t.Fatal("unsupported model called")
	}
	if _, err := c.CallWithRequestFull(&mcp.Request{}); err == nil {
		t.Fatal("unsupported model called")
	}
	for model, path := range map[string]string{
		"gpt-6-astra":       "/api/v1/ai/openai/chat/6",
		"gpt-6.1-sol":       "/api/v1/ai/openai/chat/6.1-sol",
		"gpt-6-sol":         "/api/v1/ai/openai/chat/6-sol",
		"gpt-6-luna":        "/api/v1/ai/openai/chat/6-luna",
		"gpt6":              "/api/v1/ai/openai/chat/6",
		"gpt-5.6-sol":       "/api/v1/ai/openai/chat/5.6",
		"claude-fable-5":    "/api/v1/ai/anthropic/messages/fable",
		"claude-opus-4-8":   "/api/v1/ai/anthropic/messages/opus",
		"deepseek-flash":    "/api/v1/ai/deepseek/v4-flash",
		"deepseek":          "/api/v1/ai/deepseek/v4-flash",
		"deepseek-reasoner": "/api/v1/ai/deepseek/v4-pro",
	} {
		c.SetAPIKey(claw402TestPrivateKey, "", model)
		if c.resolveEndpoint() != path {
			t.Fatalf("%s maps to %s", model, c.resolveEndpoint())
		}
		if strings.HasPrefix(model, "deepseek") {
			for _, body := range []map[string]any{c.BuildMCPRequestBody("system", "user"), c.BuildRequestBodyFromRequest(&mcp.Request{})} {
				if !reflect.DeepEqual(body["thinking"], map[string]any{"type": "disabled"}) {
					t.Fatalf("%s alias lost bounded-output thinking control", model)
				}
			}
		}
	}
}
