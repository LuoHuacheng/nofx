package okx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"nofx/trader/execution"
	"strings"
	"testing"
)

type executionTransport func(*http.Request) (*http.Response, error)

func (f executionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const okxInstrumentFixture = `[{"instId":"BTC-USDT-SWAP","instType":"SWAP","state":"live","ctType":"linear","settleCcy":"USDT","ctValCcy":"BTC","ctVal":"0.01","ctMult":"1","lotSz":"0.01","minSz":"0.01","tickSz":"0.1","maxMktSz":"10000","maxLmtSz":"10000"}]`

func okxFixture(t *testing.T, mode, positions string, handle func(*http.Request) string) *OKXTrader {
	t.Helper()
	client := NewAccountTrader("key", "secret", "pass")
	if !client.accountOnly {
		t.Fatal("unsafe account client")
	}
	client.httpClient = &http.Client{Transport: executionTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("OK-ACCESS-PASSPHRASE") != "pass" || r.Header.Get("OK-ACCESS-SIGN") == "" {
			t.Fatal("unsigned exchange request")
		}
		data := ""
		switch r.URL.Path {
		case okxInstrumentsPath:
			data = okxInstrumentFixture
		case okxAccountConfigPath:
			data = `[{"posMode":"` + mode + `","acctLv":"2"}]`
		case okxPositionPath:
			data = positions
		case okxAccountPath:
			data = `[{"details":[{"ccy":"USDT","eq":"320","availEq":"280","availBal":"270","upl":"2"}]}]`
		case "/api/v5/account/trade-fee":
			if r.URL.Query().Get("instId") != "" || r.URL.Query().Get("instFamily") != "BTC-USDT" {
				t.Fatal("SWAP fees require instFamily, not spot instId")
			}
			data = `[{"takerU":"-0.0005"}]`
		case okxPendingOrdersPath, okxAlgoPendingPath:
			data = `[]`
		default:
			if handle != nil {
				data = handle(r)
			}
		}
		if data == "" {
			t.Fatalf("unexpected endpoint %s %s", r.Method, r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":"0","data":` + data + `}`)), Header: make(http.Header)}, nil
	})}
	return client
}
func okxIntent() execution.Order {
	return execution.Order{ClientID: strings.Repeat("a", 32), Role: "entry", Symbol: "BTC-USDT-SWAP", Side: "long", Type: "market", Price: "80000", Quantity: "0.0123"}
}
func TestExecutionReadOnlyAndBaseQuantity(t *testing.T) {
	trader := okxFixture(t, "net_mode", `[{"instId":"BTC-USDT-SWAP","pos":"-1.23","posSide":"net","mgnMode":"cross","avgPx":"80000","markPx":"81000","upl":"-12.3","lever":"5","liqPx":"90000"}]`, nil)
	a, e := trader.AccountSnapshot(context.Background(), "BTC-USDT-SWAP")
	if e != nil || a.Equity != "320" || a.Available != "280" || a.FeeBPS != "5" || a.Position != "0.0123" {
		t.Fatalf("snapshot: %+v %v", a, e)
	}
	rows, e := trader.GetPositions()
	if e != nil || len(rows) != 1 || rows[0]["side"] != "short" || rows[0]["positionAmt"] != 0.0123 {
		t.Fatalf("position: %+v %v", rows, e)
	}
	if trader.PrepareExecutionLeverage(context.Background(), "BTC-USDT-SWAP", 5) == nil {
		t.Fatal("changed leverage with an open position")
	}
}
func TestExecutionSubmitCloseAndProtection(t *testing.T) {
	for _, mode := range []string{"net_mode", "long_short_mode"} {
		for _, role := range []string{"entry", "sl", "tp"} {
			t.Run(mode+role, func(t *testing.T) {
				o := okxIntent()
				o.Role = role
				o.ReduceOnly = role == "entry"
				o.Side = "long"
				posSide := "short"
				pos := "1.23"
				if role != "entry" {
					posSide = "long"
					o.Type = ""
				}
				if mode == "net_mode" {
					posSide = "net"
					if role == "entry" {
						pos = "-1.23"
					}
				}
				posts := 0
				client := okxFixture(t, mode, `[{"instId":"BTC-USDT-SWAP","pos":"`+pos+`","posSide":"`+posSide+`","mgnMode":"isolated"}]`, func(r *http.Request) string {
					posts++
					var b map[string]any
					if json.NewDecoder(r.Body).Decode(&b) != nil {
						t.Fatal("invalid body")
					}
					if b["sz"] != "1.23" || b["tag"] != okxTag || b["tdMode"] != "isolated" {
						t.Fatalf("wrong contracts/routing: %+v", b)
					}
					if mode == "net_mode" && b["reduceOnly"] != true {
						t.Fatal("missing reduceOnly")
					}
					if mode == "long_short_mode" && b["posSide"] != posSide {
						t.Fatal("wrong hedge leg")
					}
					if role == "entry" {
						if r.URL.Path != okxOrderPath || b["side"] != "buy" || b["ordType"] != "market" || b["clOrdId"] != o.ClientID {
							t.Fatalf("wrong close: %+v", b)
						}
						return `[{"ordId":"9","clOrdId":"` + o.ClientID + `","sCode":"0"}]`
					}
					if r.URL.Path != okxAlgoOrderPath || b["side"] != "sell" || b[role+"TriggerPxType"] != "mark" || b[role+"OrdPx"] != "-1" {
						t.Fatalf("wrong protection: %+v", b)
					}
					return `[{"algoId":"9","algoClOrdId":"` + o.ClientID + `","sCode":"0"}]`
				})
				r, e := client.SubmitOrder(context.Background(), o)
				if e != nil || posts != 1 || r.OrderID != "9" || r.Status != "NEW" {
					t.Fatalf("submit %+v %v", r, e)
				}
			})
		}
	}
}
func TestExecutionRejectsRoundingAndReceiptMismatch(t *testing.T) {
	o := okxIntent()
	client := okxFixture(t, "net_mode", `[]`, func(r *http.Request) string {
		if r.Method != "GET" {
			t.Fatal("unexpected mutation")
		}
		return `[{"ordId":"9","clOrdId":"wrong","instId":"BTC-USDT-SWAP"}]`
	})
	o.Quantity = "0.012345"
	if _, e := client.SubmitOrder(context.Background(), o); e == nil {
		t.Fatal("rounded contract size")
	}
	o = okxIntent()
	if _, e := client.LookupOrder(context.Background(), o); e == nil {
		t.Fatal("accepted wrong receipt")
	}
}
func TestExecutionFilledReceiptUsesBaseUnits(t *testing.T) {
	o := okxIntent()
	client := okxFixture(t, "net_mode", `[]`, func(r *http.Request) string {
		return `[{"ordId":"9","clOrdId":"` + o.ClientID + `","instId":"BTC-USDT-SWAP","side":"buy","posSide":"net","ordType":"market","sz":"1.23","accFillSz":"1.23","avgPx":"80000","state":"filled"}]`
	})
	r, e := client.LookupOrder(context.Background(), o)
	if e != nil || r.Filled != "0.0123" || r.Status != "FILLED" {
		t.Fatalf("incorrect fill %+v %v", r, e)
	}
}

func TestExecutionLeverageAndLimitEntry(t *testing.T) {
	for _, mode := range []string{"net_mode", "long_short_mode"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			client := okxFixture(t, mode, `[]`, func(r *http.Request) string {
				posts++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				if r.URL.Path == okxLeveragePath {
					if b["lever"] != "5" || b["mgnMode"] != "cross" {
						t.Fatalf("incorrect leverage: %+v", b)
					}
					return `[{"lever":"5"}]`
				}
				if r.URL.Path != okxOrderPath || b["ordType"] != "limit" || b["px"] != "80000" || b["sz"] != "1.23" || b["side"] != "buy" || b["reduceOnly"] != nil {
					t.Fatalf("incorrect entry: %+v", b)
				}
				return `[{"ordId":"9","clOrdId":"` + strings.Repeat("a", 32) + `","sCode":"0"}]`
			})
			if e := client.PrepareExecutionLeverage(context.Background(), "BTC-USDT-SWAP", 5); e != nil {
				t.Fatal(e)
			}
			o := okxIntent()
			o.Type = "limit"
			if _, e := client.SubmitOrder(context.Background(), o); e != nil {
				t.Fatal(e)
			}
			if posts != 2 {
				t.Fatalf("unexpected mutations %d", posts)
			}
		})
	}
}

func TestExecutionProtectionReconciliationAndCancellation(t *testing.T) {
	o := okxIntent()
	o.Role = "sl"
	o.Type = ""
	client := okxFixture(t, "net_mode", `[]`, func(r *http.Request) string {
		if r.Method == "GET" {
			if r.URL.Path != okxAlgoOrderPath || r.URL.Query().Get("algoClOrdId") != o.ClientID {
				t.Fatal("wrong protection lookup")
			}
			return `[{"algoId":"9","algoClOrdId":"` + o.ClientID + `","instId":"BTC-USDT-SWAP","side":"sell","posSide":"net","reduceOnly":true,"ordType":"conditional","sz":"1.23","slTriggerPx":"80000","slTriggerPxType":"mark","slOrdPx":"-1","state":"effective"}]`
		}
		if r.URL.Path != okxCancelAlgoPath {
			t.Fatal("wrong cancel endpoint")
		}
		return `[{"algoId":"9","sCode":"51400"}]`
	})
	r, e := client.LookupOrder(context.Background(), o)
	if e != nil || r.Status != "TRIGGERED" || r.Filled != "0" {
		t.Fatalf("trigger is not fill evidence %+v %v", r, e)
	}
	if client.CancelExecutionOrder(context.Background(), r) == nil {
		t.Fatal("failed cancel reported as success")
	}
}

func TestPlanningBalancesAcceptExchangePrecision(t *testing.T) {
	client := okxFixture(t, "net_mode", `[]`, nil)
	original := client.httpClient.Transport
	client.httpClient.Transport = executionTransport(func(r *http.Request) (*http.Response, error) {
		response, err := original.RoundTrip(r)
		if err != nil || r.URL.Path != okxAccountPath {
			return response, err
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		payload := strings.ReplaceAll(string(body), `"eq":"320"`, `"eq":"796.2634999999999999"`)
		payload = strings.ReplaceAll(payload, `"availEq":"280"`, `"availEq":"796.2634999999999999"`)
		response.Body = io.NopCloser(strings.NewReader(payload))
		return response, nil
	})
	snapshot, err := client.AccountSnapshot(context.Background(), "BTC-USDT-SWAP")
	if err != nil || snapshot.Equity != "796.263499999999" || snapshot.Available != "796.263499999999" {
		t.Fatalf("balance precision blocked planning: %+v %v", snapshot, err)
	}
}

func TestSignedRequestRejectsRedirects(t *testing.T) {
	client := NewAccountTrader("test-key", "test-secret", "test-passphrase")
	requests := 0
	client.httpClient.Transport = executionTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests != 1 {
			t.Fatal("signed credentials forwarded to a redirect")
		}
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://untrusted.example/collect"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	if _, err := client.doRequestContext(context.Background(), http.MethodGet, "/api/account", nil); err == nil {
		t.Fatal("redirect accepted as a valid exchange response")
	}
	if requests != 1 {
		t.Fatalf("unexpected request count: %d", requests)
	}
}
