package bitget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nofx/trader/execution"
	"strings"
	"testing"
)

type executionTransport func(*http.Request) (*http.Response, error)

func (f executionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func bitgetFixture(t *testing.T, mode, positions string, handle func(*http.Request) string) *BitgetTrader {
	t.Helper()
	client := NewAccountTrader("key", "secret", "pass")
	if !client.accountOnly {
		t.Fatal("unsafe account client")
	}
	client.httpClient = &http.Client{Transport: executionTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("ACCESS-SIGN") == "" || r.Header.Get("ACCESS-PASSPHRASE") != "pass" {
			t.Fatal("unsigned request")
		}
		if strings.Contains(r.URL.Path, "/order/") && r.Header.Get("X-CHANNEL-API-CODE") != "7fygt" {
			t.Fatal("broker routing lost")
		}
		data := ""
		switch r.URL.Path {
		case bitgetContractsPath:
			data = `[{"symbol":"BTCUSDT","quoteCoin":"USDT","symbolType":"perpetual","symbolStatus":"normal","minTradeNum":"0.0001","sizeMultiplier":"0.0001","pricePlace":"1","priceEndStep":"1","maxMarketOrderQty":"220","maxOrderQty":"1200"}]`
		case "/api/v2/mix/account/account":
			data = `{"marginCoin":"USDT","accountEquity":"320","available":"280","crossedMaxAvailable":"280","marginMode":"crossed","posMode":"` + mode + `","assetMode":"single"}`
		case bitgetPositionPath:
			data = positions
		case "/api/v2/common/trade-rate":
			data = `{"takerFeeRate":"0.0006"}`
		default:
			if handle != nil {
				data = handle(r)
			}
			if data == "" && (r.URL.Path == bitgetPendingPath || r.URL.Path == "/api/v2/mix/order/orders-plan-pending") {
				data = `{"entrustedList":[]}`
			}
		}
		if data == "" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":"00000","data":` + data + `}`)), Header: make(http.Header)}, nil
	})}
	return client
}
func bitgetIntent() execution.Order {
	return execution.Order{ClientID: strings.Repeat("b", 32), Role: "entry", Type: "market", Symbol: "BTCUSDT", Side: "short", Price: "80000", Quantity: "0.01"}
}
func TestExecutionReadOnly(t *testing.T) {
	client := bitgetFixture(t, "hedge_mode", `[{"symbol":"BTCUSDT","total":"0.01","holdSide":"long","marginMode":"crossed"}]`, nil)
	a, e := client.AccountSnapshot(context.Background(), "BTCUSDT")
	if e != nil || a.Equity != "320" || a.Available != "280" || a.FeeBPS != "6" || a.Position != "0.01" {
		t.Fatalf("bad snapshot %+v %v", a, e)
	}
	if client.PrepareExecutionLeverage(context.Background(), "BTCUSDT", 5) == nil {
		t.Fatal("changed leverage with live position")
	}
}
func TestExecutionCloseAndTPSLDirections(t *testing.T) {
	for _, mode := range []string{"one_way_mode", "hedge_mode"} {
		for _, role := range []string{"entry", "sl", "tp"} {
			t.Run(mode+role, func(t *testing.T) {
				o := bitgetIntent()
				o.Role = role
				o.ReduceOnly = role == "entry"
				if role != "entry" {
					o.Side = "long"
					o.Type = ""
				}
				posts := 0
				client := bitgetFixture(t, mode, `[{"symbol":"BTCUSDT","total":"0.01","holdSide":"long","marginMode":"crossed"}]`, func(r *http.Request) string {
					posts++
					var b map[string]any
					json.NewDecoder(r.Body).Decode(&b)
					if b["size"] != "0.01" || b["clientOid"] != o.ClientID {
						t.Fatalf("bad size/id %+v", b)
					}
					if role == "entry" {
						if r.URL.Path != bitgetOrderPath || b["orderType"] != "market" {
							t.Fatal("wrong close endpoint")
						}
						if mode == "hedge_mode" {
							if b["side"] != "buy" || b["tradeSide"] != "close" {
								t.Fatalf("would open a hedge position %+v", b)
							}
						} else if b["side"] != "sell" || b["reduceOnly"] != "YES" {
							t.Fatalf("would reverse position %+v", b)
						}
					} else {
						hold := "buy"
						if mode == "hedge_mode" {
							hold = "long"
						}
						kind := "loss_plan"
						if role == "tp" {
							kind = "profit_plan"
						}
						if r.URL.Path != "/api/v2/mix/order/place-tpsl-order" || b["holdSide"] != hold || b["planType"] != kind || b["triggerType"] != "mark_price" || b["executePrice"] != "0" {
							t.Fatalf("incorrect protection %+v", b)
						}
					}
					return `{"orderId":"9","clientOid":"` + o.ClientID + `"}`
				})
				r, e := client.SubmitOrder(context.Background(), o)
				if e != nil || posts != 1 || r.Status != "NEW" || r.Filled != "0" {
					t.Fatalf("bad result %+v %v", r, e)
				}
			})
		}
	}
}
func TestExecutionUnknownLookupNeverResubmits(t *testing.T) {
	o := bitgetIntent()
	client := bitgetFixture(t, "one_way_mode", `[]`, func(r *http.Request) string {
		if r.Method != "GET" {
			t.Fatal("retried submission")
		}
		return `{}`
	})
	if _, e := client.LookupOrder(context.Background(), o); e == nil {
		t.Fatal("missing order interpreted as filled")
	}
	o.Quantity = "0.00015"
	if _, e := client.SubmitOrder(context.Background(), o); e == nil {
		t.Fatal("rounded quantity")
	}
}
func TestExecutionProtectionStateAndIdentity(t *testing.T) {
	o := bitgetIntent()
	o.Role = "sl"
	o.Side = "long"
	o.Type = ""
	client := bitgetFixture(t, "one_way_mode", `[]`, func(r *http.Request) string {
		if r.URL.Path != "/api/v2/mix/order/orders-plan-pending" {
			t.Fatal("unexpected retry")
		}
		return `{"entrustedList":[{"orderId":"9","clientOid":"` + o.ClientID + `","symbol":"btcusdt","size":"0.01","side":"sell","posMode":"one_way_mode","posSide":"long","planType":"loss_plan","triggerType":"mark_price","triggerPrice":"80000","planStatus":"executed"}]}`
	})
	r, e := client.LookupOrder(context.Background(), o)
	if e != nil || r.Status != "TRIGGERED" || r.Filled != "0" {
		t.Fatalf("trigger is not fill evidence %+v %v", r, e)
	}
}

func TestExecutionLeverageAndLimitEntry(t *testing.T) {
	for _, mode := range []string{"one_way_mode", "hedge_mode"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			client := bitgetFixture(t, mode, `[]`, func(r *http.Request) string {
				if r.Method == "GET" {
					return ""
				}
				posts++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				if r.URL.Path == bitgetLeveragePath {
					if b["leverage"] != "5" {
						t.Fatalf("wrong leverage %+v", b)
					}
					return `{"crossMarginLeverage":"5"}`
				}
				if r.URL.Path != bitgetOrderPath || b["orderType"] != "limit" || b["price"] != "80000" || b["size"] != "0.01" || b["side"] != "sell" || b["reduceOnly"] != nil {
					t.Fatalf("wrong entry %+v", b)
				}
				if mode == "hedge_mode" && b["tradeSide"] != "open" {
					t.Fatal("entry became a close")
				}
				return `{"orderId":"9","clientOid":"` + strings.Repeat("b", 32) + `"}`
			})
			if e := client.PrepareExecutionLeverage(context.Background(), "BTCUSDT", 5); e != nil {
				t.Fatal(e)
			}
			o := bitgetIntent()
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

func TestExecutionCancellationFailureAndFilledReceipt(t *testing.T) {
	o := bitgetIntent()
	client := bitgetFixture(t, "hedge_mode", `[]`, func(r *http.Request) string {
		if r.Method == "GET" {
			return `{"orderId":"9","clientOid":"` + o.ClientID + `","symbol":"BTCUSDT","size":"0.01","posMode":"hedge_mode","posSide":"short","side":"sell","tradeSide":"open","orderType":"market","state":"filled","baseVolume":"0.01","priceAvg":"80000"}`
		}
		return `{"successList":[],"failureList":[{"orderId":"9"}]}`
	})
	r, e := client.LookupOrder(context.Background(), o)
	if e != nil || r.Status != "FILLED" || r.Filled != "0.01" {
		t.Fatalf("wrong receipt %+v %v", r, e)
	}
	r.Role = "sl"
	r.Type = ""
	if client.CancelExecutionOrder(context.Background(), r) == nil {
		t.Fatal("failed cancel reported as success")
	}
}

func TestPlanningBalancesAcceptExchangePrecision(t *testing.T) {
	client := bitgetFixture(t, "one_way_mode", `[]`, nil)
	original := client.httpClient.Transport
	client.httpClient.Transport = executionTransport(func(r *http.Request) (*http.Response, error) {
		response, err := original.RoundTrip(r)
		if err != nil || r.URL.Path != "/api/v2/mix/account/account" {
			return response, err
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		payload := strings.ReplaceAll(string(body), `"accountEquity":"320"`, `"accountEquity":"796.2634999999999999"`)
		payload = strings.ReplaceAll(payload, `"available":"280"`, `"available":"796.2634999999999999"`)
		payload = strings.ReplaceAll(payload, `"crossedMaxAvailable":"280"`, `"crossedMaxAvailable":"796.2634999999999999"`)
		response.Body = io.NopCloser(strings.NewReader(payload))
		return response, nil
	})
	snapshot, err := client.AccountSnapshot(context.Background(), "BTCUSDT")
	if err != nil || snapshot.Equity != "796.263499999999" || snapshot.Available != "796.263499999999" {
		t.Fatalf("balance precision blocked planning: %+v %v", snapshot, err)
	}
}

func TestProtectionReceiptOneWayPositionVocabulary(t *testing.T) {
	for _, held := range []string{"long", "short"} {
		for _, posSide := range []string{"long", "short", "buy", "sell", "net", "", "unknown"} {
			t.Run(held+"/"+posSide, func(t *testing.T) {
				o := bitgetIntent()
				o.Role, o.Type, o.Side, o.OrderID = "sl", "", held, "9"
				side := "sell"
				if held == "short" {
					side = "buy"
				}
				client := bitgetFixture(t, "one_way_mode", `[]`, func(r *http.Request) string {
					if r.Method != "GET" || r.URL.Query().Get("orderId") != "9" {
						t.Fatalf("must look up the known order without mutation: %s %s", r.Method, r.URL)
					}
					return `{"entrustedList":[{"orderId":"9","clientOid":"` + o.ClientID + `","symbol":"btcusdt","size":"0.01","side":"` + side + `","posMode":"one_way_mode","posSide":"` + posSide + `","planType":"loss_plan","triggerType":"mark_price","triggerPrice":"80000","planStatus":"cancelled"}]}`
				})
				got, err := client.LookupOrder(context.Background(), o)
				valid := posSide == held || posSide == "net"
				if valid && (err != nil || got.Status != "CANCELED") {
					t.Fatalf("valid one-way protection not reconciled: %+v %v", got, err)
				}
				if !valid && err == nil {
					t.Fatal("accepted an ambiguous or opposite protection direction")
				}
			})
		}
	}
}

func TestProtectionCancelledNetReceipt(t *testing.T) {
	o := bitgetIntent()
	o.Role, o.Type, o.Side, o.OrderID = "sl", "", "long", "1495603362477862912"
	o.Quantity, o.Price = "0.0127", "80740"
	for _, side := range []string{"sell", "buy", ""} {
		t.Run(side, func(t *testing.T) {
			client := bitgetFixture(t, "one_way_mode", `[]`, func(r *http.Request) string {
				if r.Method != "GET" {
					t.Fatal("receipt reconciliation must be read-only")
				}
				if r.URL.Path == "/api/v2/mix/order/orders-plan-pending" {
					return `{"entrustedList":[]}`
				}
				return `{"entrustedList":[{"orderId":"1495603362477862912","executeOrderId":"","executePrice":"","clientOid":"` + o.ClientID + `","planStatus":"cancelled","planType":"loss_plan","posMode":"one_way_mode","posSide":"net","side":"` + side + `","size":"0.0127","symbol":"BTCUSDT","tradeSide":"sell_single","triggerPrice":"80740","triggerType":"mark_price"}]}`
			})
			got, err := client.LookupOrder(context.Background(), o)
			if side == "sell" && (err != nil || got.Status != "CANCELED" || got.Filled != "0") {
				t.Fatalf("real cancelled net receipt was not reconciled: %+v %v", got, err)
			}
			if side != "sell" && err == nil {
				t.Fatal("net receipt without matching closing side accepted")
			}
		})
	}
}

func TestProtectionHistoryUsesKnownOrderIdentity(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			o := bitgetIntent()
			o.Role, o.Type, o.Side = "sl", "", "long"
			if known {
				o.OrderID = "9"
			}
			client := bitgetFixture(t, "one_way_mode", `[]`, func(r *http.Request) string {
				if r.Method != "GET" {
					t.Fatal("lookup must not submit or cancel")
				}
				if r.URL.Path == "/api/v2/mix/order/orders-plan-pending" {
					return `{"entrustedList":[]}`
				}
				return `{"entrustedList":[{"orderId":"9","clientOid":"","symbol":"btcusdt","size":"0.01","side":"sell","posMode":"one_way_mode","posSide":"net","planType":"loss_plan","triggerType":"mark_price","triggerPrice":"80000","planStatus":"cancelled"}]}`
			})
			got, err := client.LookupOrder(context.Background(), o)
			if known && (err != nil || got.OrderID != "9" || got.Status != "CANCELED") {
				t.Fatalf("known historical identity lost: %+v %v", got, err)
			}
			if !known && err == nil {
				t.Fatal("receipt without either matching identity was accepted")
			}
		})
	}
}

func TestProtectionTriggeredExecutionMustBeConfirmed(t *testing.T) {
	for _, state := range []string{"filled", "partially_filled", "live", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			o := bitgetIntent()
			o.Role, o.Type, o.Side, o.OrderID = "sl", "", "long", "9"
			filled := "0"
			if state == "filled" {
				filled = "0.01"
			} else if state == "partially_filled" {
				filled = "0.005"
			}
			client := bitgetFixture(t, "one_way_mode", `[]`, func(r *http.Request) string {
				if r.Method != "GET" {
					t.Fatal("lookup mutated the exchange")
				}
				if r.URL.Path == "/api/v2/mix/order/orders-plan-pending" {
					return `{"entrustedList":[]}`
				}
				if r.URL.Path == "/api/v2/mix/order/orders-plan-history" {
					return `{"entrustedList":[{"orderId":"9","executeOrderId":"10","clientOid":"","symbol":"btcusdt","size":"0.01","side":"sell","posMode":"one_way_mode","posSide":"net","planType":"loss_plan","triggerType":"mark_price","triggerPrice":"80000","planStatus":"executed"}]}`
				}
				if r.URL.Path != "/api/v2/mix/order/detail" || r.URL.Query().Get("orderId") != "10" || r.URL.Query().Get("clientOid") != "" {
					t.Fatalf("must look up the linked execution: %s", r.URL)
				}
				return `{"orderId":"10","clientOid":"exchange-generated","symbol":"BTCUSDT","size":"0.01","side":"sell","posMode":"one_way_mode","posSide":"net","orderType":"market","reduceOnly":"YES","state":"` + state + `","baseVolume":"` + filled + `","priceAvg":"79999.5"}`
			})
			got, err := client.LookupOrder(context.Background(), o)
			want := map[string]string{"filled": "FILLED", "partially_filled": "PARTIALLY_FILLED", "live": "TRIGGERED", "cancelled": "CANCELED"}[state]
			if err != nil || got.OrderID != "9" || got.Status != want || got.Filled != execution.Decimal(filled) {
				t.Fatalf("incorrect trigger-to-fill transition: %+v %v", got, err)
			}
		})
	}
}

func TestProtectionRejectsMismatchedExecutionEvidence(t *testing.T) {
	o := bitgetIntent()
	o.Role, o.Type, o.Side, o.OrderID = "sl", "", "long", "9"
	for _, mismatch := range []string{"orderId", "symbol", "size", "side", "reduceOnly", "baseVolume"} {
		t.Run(mismatch, func(t *testing.T) {
			client := bitgetFixture(t, "one_way_mode", `[]`, func(r *http.Request) string {
				if r.Method != "GET" {
					t.Fatal("lookup mutated exchange state")
				}
				if r.URL.Path == "/api/v2/mix/order/orders-plan-pending" {
					return `{"entrustedList":[{"orderId":"9","executeOrderId":"10","clientOid":"` + o.ClientID + `","symbol":"BTCUSDT","size":"0.01","side":"sell","posMode":"one_way_mode","posSide":"net","planType":"loss_plan","triggerType":"mark_price","triggerPrice":"80000","planStatus":"executed"}]}`
				}
				fill := map[string]string{"orderId": "10", "symbol": "BTCUSDT", "size": "0.01", "side": "sell", "posMode": "one_way_mode", "posSide": "net", "orderType": "market", "reduceOnly": "YES", "state": "filled", "baseVolume": "0.01", "priceAvg": "79999.5"}
				fill[mismatch] = map[string]string{"orderId": "11", "symbol": "ETHUSDT", "size": "0.02", "side": "buy", "reduceOnly": "NO", "baseVolume": "0.02"}[mismatch]
				encoded, _ := json.Marshal(fill)
				return string(encoded)
			})
			if _, err := client.LookupOrder(context.Background(), o); err == nil {
				t.Fatalf("accepted mismatched execution %s", mismatch)
			}
		})
	}
}

func TestReceiptIdentityRejectsConflicts(t *testing.T) {
	o := bitgetIntent()
	o.OrderID = "9"
	for _, receipt := range []executionReceipt{
		{OrderId: "10", ClientOid: o.ClientID},
		{OrderId: "9", ClientOid: "someone-else"},
		{ClientOid: o.ClientID},
	} {
		if bitgetReceiptIdentity(o, receipt) {
			t.Fatalf("conflicting receipt identity accepted: %+v", receipt)
		}
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
