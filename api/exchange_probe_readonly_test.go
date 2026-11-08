package api

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"nofx/crypto"
	"nofx/store"
)

type probeReadOnlyTransport struct {
	t        *testing.T
	writes   int
	requests int
}

func (p *probeReadOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.requests++
	if r.URL.Host != "testnet.binancefuture.com" {
		p.t.Errorf("sandbox probe contacted %s", r.URL.Host)
	}
	if r.Method != http.MethodGet {
		p.writes++
	}
	body := `{"code":200,"msg":"success"}`
	switch r.URL.Path {
	case "/fapi/v1/time":
		body = `{"serverTime":` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `}`
	case "/fapi/v1/positionSide/dual":
		body = `{"dualSidePosition":false}`
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestBinanceSandboxAccountProbeNeverWritesSettings(t *testing.T) {
	transport := &probeReadOnlyTransport{t: t}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original })
	cfg := &store.Exchange{ExchangeType: "binance", Testnet: true, APIKey: crypto.EncryptedString("fixture-key"), SecretKey: crypto.EncryptedString("fixture-secret")}
	if _, err := buildExchangeProbeTrader(cfg, "alice"); err != nil {
		t.Fatal(err)
	}
	if transport.requests == 0 {
		t.Fatal("probe bypassed the fixture transport")
	}
	if transport.writes != 0 {
		t.Fatalf("reading account status sent %d account setting writes", transport.writes)
	}
}
