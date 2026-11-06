package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"nofx/internal/permit2"
	"nofx/mcp"
)

func TestPaidFailuresNeverCreateAnotherAuthorization(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, status := range []int{402, 429, 500, 502, 503, 307, 200, 0} {
			t.Run(fmt.Sprintf("stream=%t/status=%d", stream, status), func(t *testing.T) {
				var signs, paid, unpaid atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Payment-Signature") == "" {
						unpaid.Add(1)
						w.Header().Set("Payment-Required", "quote")
						w.WriteHeader(402)
						return
					}
					paid.Add(1)
					w.Header().Set("Payment-Required", "quote")
					if status == 0 {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					if status == 307 {
						w.Header().Set("Location", "/redirected")
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte("ok"))
				}))
				defer srv.Close()
				sign := func(string) (string, error) { return fmt.Sprint("authorization-", signs.Add(1)), nil }
				build := func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }
				var err error
				if stream {
					var r *http.Response
					r, err = DoX402RequestStream(context.Background(), srv.Client(), build, sign, "test", mcp.NewNoopLogger())
					if r != nil {
						r.Body.Close()
					}
				} else {
					_, err = DoX402Request(context.Background(), srv.Client(), build, sign, "test", mcp.NewNoopLogger())
				}
				if signs.Load() != 1 || paid.Load() != 1 || unpaid.Load() != 1 {
					t.Fatalf("signs=%d paid=%d unpaid=%d", signs.Load(), paid.Load(), unpaid.Load())
				}
				if status == 200 {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var unknown *PaymentOutcomeUnknownError
				if !errors.As(err, &unknown) {
					t.Fatalf("expected non-retryable unknown payment outcome, got %v", err)
				}
				c := &mcp.Client{Cfg: mcp.DefaultConfig()}
				if c.IsRetryableError(fmt.Errorf("wrapped: %w", err)) {
					t.Fatal("outer client retries paid failure")
				}
			})
		}
	}
}

func TestUptoSignsPermit2NotEIP3009(t *testing.T) {
	key, _ := crypto.GenerateKey()
	opt := X402AcceptOption{Scheme: "upto", Network: BaseNetwork, Asset: BaseUSDCContract, PayTo: "0x1111111111111111111111111111111111111111", Amount: "1000000", MaxTimeoutSeconds: 900, Extra: map[string]string{"name": "USD Coin", "version": "2", "facilitatorAddress": "0x2222222222222222222222222222222222222222"}}
	encoded, err := SignX402Payment(key, crypto.PubkeyToAddress(key.PublicKey).Hex(), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := base64.StdEncoding.DecodeString(encoded)
	var p struct {
		Payload permit2.Payload `json:"payload"`
	}
	if err = json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\"authorization\"") {
		t.Fatal("upto used EIP-3009")
	}
	if err = permit2.Verify(8453, p.Payload); err != nil {
		t.Fatal(err)
	}
	p.Payload.Authorization.Permitted.Amount = "100000"
	if err = permit2.Verify(8453, p.Payload); err == nil {
		t.Fatal("signed ceiling tampering accepted")
	}
	delete(opt.Extra, "facilitatorAddress")
	if _, err = SignX402Payment(key, crypto.PubkeyToAddress(key.PublicKey).Hex(), opt, nil); err == nil {
		t.Fatal("legacy upto challenge accepted")
	}
}

func TestInitialDiscoveryRejectsSignedBuilder(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer srv.Close()
	_, err := DoX402Request(context.Background(), srv.Client(), func() (*http.Request, error) {
		r, _ := http.NewRequest("GET", srv.URL, nil)
		r.Header.Set("X-Payment", "already-signed")
		return r, nil
	}, nil, "test", mcp.NewNoopLogger())
	if err == nil || calls != 0 {
		t.Fatal("signed builder sent as retryable discovery")
	}
}

func TestPaidTruncatedBodyIsNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Payment-Signature") == "" {
			w.Header().Set("Payment-Required", "quote")
			w.WriteHeader(402)
			return
		}
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	_, err := DoX402Request(context.Background(), srv.Client(), func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }, func(string) (string, error) {
		return base64.StdEncoding.EncodeToString([]byte(`{"accepted":{"scheme":"exact"}}`)), nil
	}, "test", mcp.NewNoopLogger())
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v", err)
	}
	if (&mcp.Client{Cfg: mcp.DefaultConfig()}).IsRetryableError(err) {
		t.Fatal("truncated paid body retried")
	}
}

func TestPaid402DoesNotSleepOrRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Payment-Required", "quote")
		w.WriteHeader(402)
	}))
	defer srv.Close()
	_, err := DoX402Request(ctx, srv.Client(), func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }, func(string) (string, error) { return "signed", nil }, "test", mcp.NewNoopLogger())
	var unknown *PaymentOutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("got %v", err)
	}
}
