package payment

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nofx/mcp"
)

// Discovery negotiates HTTP/2 and mutates the original transport's TLS ALPN
// list. The paid connection must advertise AND speak HTTP/1.1, without changing
// the original client or reusing/replaying the authorized request.
func TestPaidHTTPSAfterHTTP2Discovery(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", method, streaming), func(t *testing.T) {
				var unpaid, paid, signs atomic.Int32
				srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Payment-Signature") == "" {
						unpaid.Add(1)
						if r.ProtoMajor != 2 || r.TLS.NegotiatedProtocol != "h2" {
							t.Error("test must exercise real HTTP/2 discovery")
						}
						w.Header().Set("Payment-Required", "test-quote")
						w.WriteHeader(http.StatusPaymentRequired)
						return
					}
					paid.Add(1)
					if r.ProtoMajor != 1 || r.TLS.NegotiatedProtocol != "http/1.1" || !r.Close {
						t.Errorf("paid request did not use a one-shot HTTP/1.1 connection: %s %s close=%t", r.Proto, r.TLS.NegotiatedProtocol, r.Close)
					}
					if r.Method != method || r.Header.Get("X-Payment") != "test-authorization" {
						t.Error("paid request method/authorization changed")
					}
					_, _ = io.WriteString(w, "ok")
				}))
				srv.EnableHTTP2 = true
				srv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
				srv.StartTLS()
				defer srv.Close()
				client := srv.Client()
				original := client.Transport.(*http.Transport)
				original.ForceAttemptHTTP2 = true
				var beforeALPN []string
				sign := func(string) (string, error) {
					signs.Add(1)
					beforeALPN = append([]string(nil), original.TLSClientConfig.NextProtos...)
					return "test-authorization", nil // transport-only mock; never a live payment
				}
				build := func() (*http.Request, error) {
					return http.NewRequest(method, srv.URL, strings.NewReader("test body"))
				}
				var body []byte
				var err error
				if streaming {
					var resp *http.Response
					resp, err = DoX402RequestStream(context.Background(), client, build, sign, "test", mcp.NewNoopLogger())
					if resp != nil {
						body, err = io.ReadAll(resp.Body)
						resp.Body.Close()
					}
				} else {
					body, err = DoX402Request(context.Background(), client, build, sign, "test", mcp.NewNoopLogger())
				}
				if err != nil || string(body) != "ok" {
					t.Fatalf("HTTPS paid path failed: body=%q err=%v", body, err)
				}
				if unpaid.Load() != 1 || paid.Load() != 1 || signs.Load() != 1 {
					t.Fatalf("unpaid=%d paid=%d signs=%d", unpaid.Load(), paid.Load(), signs.Load())
				}
				if !reflect.DeepEqual(beforeALPN, original.TLSClientConfig.NextProtos) || original.DisableKeepAlives || !original.ForceAttemptHTTP2 {
					t.Fatal("paid transport mutated the shared discovery client")
				}
			})
		}
	}
}

func TestPaidHTTPSTransportKeepsTLSVerification(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	// An untrusted certificate must remain untrusted. Fixing ALPN must not
	// weaken certificate verification, even when the caller has no TLS config.
	c := singleUsePaidClient(&http.Client{Transport: &http.Transport{}, Timeout: time.Second})
	if resp, err := c.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("paid transport accepted an untrusted certificate")
	}
}

func TestPaidHTTPSFailuresRemainSingleAttempt(t *testing.T) {
	for _, status := range []int{0, 307, 402, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var paid, signs atomic.Int32
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Payment-Signature") == "" {
					w.Header().Set("Payment-Required", "quote")
					w.WriteHeader(402)
					return
				}
				paid.Add(1)
				if status == 0 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
			}))
			srv.EnableHTTP2 = true
			srv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
			srv.StartTLS()
			defer srv.Close()
			client := srv.Client()
			client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
			_, err := DoX402Request(context.Background(), client,
				func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) },
				func(string) (string, error) { signs.Add(1); return "test-authorization", nil }, "test", mcp.NewNoopLogger())
			var unknown *PaymentOutcomeUnknownError
			if !errors.As(err, &unknown) || unknown.Retryable() || paid.Load() != 1 || signs.Load() != 1 {
				t.Fatalf("err=%v paid=%d signs=%d", err, paid.Load(), signs.Load())
			}
		})
	}
}

func TestPaidHTTPSUptoReceipt(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			signed, envelope := signedUsageFixture(t)
			report, _ := json.Marshal(settlementReport{Type: "x402.settlement", Success: true, Network: BaseNetwork, Payer: envelope.Payload.Authorization.From, Amount: "0"})
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Payment-Signature") == "" {
					w.Header().Set("Payment-Required", "quote")
					w.WriteHeader(402)
					return
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: x402.settlement\ndata: %s\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n", report)
				} else {
					w.Header().Set("Payment-Response", base64.StdEncoding.EncodeToString(report))
					w.Header().Set("X-Claw402-Settled-Amount", "0")
					io.WriteString(w, `{"content":"hello"}`)
				}
			}))
			srv.EnableHTTP2 = true
			srv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
			srv.StartTLS()
			defer srv.Close()
			client := srv.Client()
			client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
			resp, err := DoX402RequestStream(context.Background(), client,
				func() (*http.Request, error) { return http.NewRequest("POST", srv.URL, nil) },
				func(string) (string, error) { return signed, nil }, "test", mcp.NewNoopLogger())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || !strings.Contains(string(body), "hello") || resp.Header.Get("X-Nofx-Receipt-Verified") != "true" {
				t.Fatalf("body=%s err=%v verified=%s", body, err, resp.Header.Get("X-Nofx-Receipt-Verified"))
			}
		})
	}
}

func TestPaymentOutcomeErrorDoesNotExposeTransportBytes(t *testing.T) {
	for _, cause := range []error{nil, errors.New(`Get "https://example.invalid?token=secret": malformed HTTP response "\x00\x04"`), io.ErrUnexpectedEOF} {
		err := &PaymentOutcomeUnknownError{Cause: cause}
		message := err.Error()
		if strings.Contains(message, "secret") || strings.Contains(message, `\x00`) || strings.Contains(message, "https://") || !strings.Contains(message, "outcome unknown") || !strings.Contains(message, "before retrying") {
			t.Fatalf("unsafe or misleading message: %s", message)
		}
		if err.Unwrap() != cause || err.Retryable() {
			t.Fatal("lost cause or non-retryable behavior")
		}
	}
}

// Optional deployment check: the exact paid transport, but only an unsigned
// GET /health. Never signs, buys data, invokes a model, or starts a trader.
func TestPaidTransportLiveUnsignedHealth(t *testing.T) {
	if os.Getenv("NOFX_LIVE_X402_HTTP_TEST") != "1" {
		t.Skip("opt-in unsigned live health probe")
	}
	original := http.DefaultTransport.(*http.Transport).Clone()
	defer original.CloseIdleConnections()
	client := &http.Client{Transport: original, Timeout: 15 * time.Second}
	for i := 0; i < 2; i++ {
		c := client
		if i == 1 {
			// Clone only after discovery has initialized and used HTTP/2.
			c = singleUsePaidClient(client)
		}
		resp, err := c.Get("https://claw402.ai/health")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || (i == 1 && resp.ProtoMajor != 1) {
			t.Fatalf("status=%d protocol=%s", resp.StatusCode, resp.Proto)
		}
		t.Logf("probe=%d status=%d protocol=%s ALPN=%s", i, resp.StatusCode, resp.Proto, resp.TLS.NegotiatedProtocol)
	}
}
