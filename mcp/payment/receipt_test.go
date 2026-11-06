package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"nofx/mcp"
)

func signedUsageFixture(t *testing.T) (string, uptoEnvelope) {
	t.Helper()
	key, _ := crypto.GenerateKey()
	opt := X402AcceptOption{Scheme: "upto", Network: BaseNetwork, Asset: BaseUSDCContract, Amount: "1000000", PayTo: "0x1111111111111111111111111111111111111111", MaxTimeoutSeconds: 660, Extra: map[string]string{"facilitatorAddress": "0x2222222222222222222222222222222222222222"}}
	signed, err := SignX402Payment(key, crypto.PubkeyToAddress(key.PublicKey).Hex(), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(signed)
	var p uptoEnvelope
	json.Unmarshal(raw, &p)
	return signed, p
}
func TestFakeLocalSettlementRejectedWithoutReplay(t *testing.T) {
	signed, p := signedUsageFixture(t)
	paid := 0
	signs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Payment-Signature") == "" {
			w.Header().Set("Payment-Required", "quote")
			w.WriteHeader(402)
			return
		}
		paid++
		b, _ := json.Marshal(settlementReport{Success: true, Transaction: "local:fake", Network: BaseNetwork, Payer: p.Payload.Authorization.From})
		w.Header().Set("Payment-Response", base64.StdEncoding.EncodeToString(b))
		w.Header().Set("X-Claw402-Settled-Amount", "55000")
		w.Header().Set("X-Nofx-Receipt-Verified", "true")
		_, _ = w.Write([]byte(`{"content":"must not be delivered"}`))
	}))
	defer srv.Close()
	result, err := DoX402Request(context.Background(), srv.Client(), func() (*http.Request, error) { return http.NewRequest("POST", srv.URL, nil) }, func(string) (string, error) { signs++; return signed, nil }, "test", mcp.NewNoopLogger())
	var unknown *PaymentOutcomeUnknownError
	if !errors.As(err, &unknown) || len(result) != 0 || paid != 1 || signs != 1 {
		t.Fatalf("result=%s error=%v paid=%d signs=%d", result, err, paid, signs)
	}
}

func TestUsageSSERequiresSettlementBeforeContent(t *testing.T) {
	signed, p := signedUsageFixture(t)
	for _, valid := range []bool{false, true} {
		body := ": keepalive\n\n"
		if valid {
			// Protocol permits zero settlement without a transaction. Positive amounts
			// are independently checked by permit2.VerifySettlement tests.
			b, _ := json.Marshal(settlementReport{Type: "x402.settlement", Success: true, Network: BaseNetwork, Payer: p.Payload.Authorization.From, Amount: "0"})
			body += "event: x402.settlement\ndata: " + string(b) + "\n\n"
		}
		body += "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"
		req, _ := http.NewRequest("POST", "https://example.invalid", nil)
		resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
		if err := guardUsageResponse(resp, signed); err != nil {
			t.Fatal(err)
		}
		chunks := 0
		text, _, err := mcp.ParseSSEStream(resp.Body, func(string) { chunks++ }, nil)
		if valid {
			if err != nil || text != "hello" || chunks != 1 {
				t.Fatalf("valid stream failed: %s %v", text, err)
			}
			c := &mcp.Client{}
			captureSettledUSD(c, resp.Header)
			if !c.LastCallSettlementVerified || c.LastCallSettledUSD != 0 {
				t.Fatal("zero receipt not represented faithfully")
			}
		} else if err == nil || text != "" || chunks != 0 {
			t.Fatal("unverified stream reached decision consumer")
		}
	}
}

func TestUsageEstimatesAndBareHeadersAreNotActualCharges(t *testing.T) {
	c := &mcp.Client{LastCallSettledUSD: 1, LastCallSettlementVerified: true, LastCallUsage: &mcp.TokenUsage{PromptTokens: 1000, CompletionTokens: 500}}
	h := http.Header{}
	h.Set(X402SettledUSDHeader, "0.001")
	captureSettledUSD(c, h)
	client := &Claw402Client{Client: c}
	if cost, ok := client.LastCallCostUSD(); ok || cost != 0 {
		t.Fatal("unverified header/usage counted as settlement")
	}
	h.Set("X-Nofx-Receipt-Verified", "true")
	for _, value := range []string{"NaN", "+Inf", "-1", "nope"} {
		h.Set(X402SettledUSDHeader, value)
		captureSettledUSD(c, h)
		if c.LastCallSettlementVerified {
			t.Fatal("invalid cost accepted")
		}
	}
}

func TestJSONReceiptAmountAndHeaderCompatibility(t *testing.T) {
	signed, envelope := signedUsageFixture(t)
	for _, legacy := range []bool{false, true} {
		for _, amountHeader := range []string{"", "0", "1"} {
			report := settlementReport{Success: true, Network: BaseNetwork, Payer: envelope.Payload.Authorization.From, Amount: "0"}
			raw, _ := json.Marshal(report)
			h := http.Header{}
			header := "Payment-Response"
			if legacy {
				header = "X-Payment-Response"
			}
			h.Set(header, base64.StdEncoding.EncodeToString(raw))
			if amountHeader != "" {
				h.Set("X-Claw402-Settled-Amount", amountHeader)
			}
			req, _ := http.NewRequest("POST", "https://fixture.invalid", nil)
			resp := &http.Response{Header: h, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}
			err := guardUsageResponse(resp, signed)
			if amountHeader == "1" {
				if err == nil {
					t.Fatal("conflicting payment amounts accepted")
				}
				continue
			}
			if err != nil || h.Get("X-Nofx-Receipt-Verified") != "true" {
				t.Fatalf("legacy=%v amount=%q error=%v", legacy, amountHeader, err)
			}
		}
	}
}
func TestUnknownPaymentDiagnosticDoesNotExposeCause(t *testing.T) {
	for _, v := range []struct{ cause, code string }{{"missing payment receipt https://secret.invalid/key", "receipt_missing"}, {"missing or invalid settled amount", "settled_amount_missing"}, {"receipt RPC chain mismatch or unavailable", "receipt_rpc_unavailable"}, {"arbitrary private credential", "verification_failed"}, {"claw402 paid request returned status 502; private credential", "gateway_http_502"}, {"usage stream missing settlement report", "settlement_stream_invalid"}} {
		err := &PaymentOutcomeUnknownError{Cause: errors.New(v.cause)}
		if err.ReasonCode() != v.code || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "credential") {
			t.Fatal(err)
		}
	}
}

func TestPaidFailureMetadataNeverEchoesRemoteMessages(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":{"code":"invalid_function_parameters","message":"secret key and private prompt"}}`, " validation:invalid_function_parameters"},
		{`{"error":{"type":"invalid_request_error","message":"secret"}}`, " validation:invalid_request_error"},
		{`{"error":{"code":"private_secret","message":"secret"}}`, ""},
		{`{"error":"secret"}`, ""},
		{`{"error":"upstream_request_rejected","code":"invalid_function_parameters","message":"secret"}`, " validation:invalid_function_parameters"},
		{`{"error":"upstream_request_rejected","message":"Invalid schema: required field missing. private prompt"}`, " hint:schema_required"},
		{`{"error":{"message":"stream must be true, private prompt"}}`, " hint:stream_required"},
	} {
		if got := paidFailureMetadata([]byte(tc.body)); got != tc.want {
			t.Fatalf("metadata: %q", got)
		}
	}
}
