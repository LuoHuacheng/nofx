package okx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nofx/trader/execution"
	"strings"
	"testing"
)

// OKX demo trading uses the same fixed host (https://www.okx.com) as live and is
// selected by the x-simulated-trading: 1 header (checked 2026-11-06 against the
// repository's existing OKX client and https://www.okx.com/docs-v5/en/#overview-demo-trading-services;
// the docs were not fetched offline).
type okxManagedRoutes map[string]func(*http.Request) (int, string)

type okxManagedCalls struct {
	requests, simulated, plain int
	methods                    map[string]int
}

func okxManagedData(data string) string { return `{"code":"0","data":` + data + `}` }

func newManagedOKXFixture(t *testing.T, env execution.Environment, routes okxManagedRoutes) (*OKXTrader, *okxManagedCalls) {
	t.Helper()
	calls := &okxManagedCalls{methods: map[string]int{}}
	client, err := NewManagedAccountTrader(context.Background(), "test-key", "test-secret", "test-pass", env)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient = &http.Client{Transport: executionTransport(func(r *http.Request) (*http.Response, error) {
		calls.requests++
		calls.methods[r.Method]++
		if r.Header.Get("x-simulated-trading") == "1" {
			calls.simulated++
		} else {
			calls.plain++
		}
		handler, ok := routes[r.URL.Path]
		if !ok {
			t.Fatalf("unexpected OKX endpoint %s %s", r.Method, r.URL.String())
		}
		status, body := handler(r)
		if body == "" {
			t.Fatalf("empty OKX fixture for %s", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	return client, calls
}

func okxManagedFixture(mode, ctVal, lotSz, positions string) okxManagedRoutes {
	return okxManagedRoutes{
		okxAccountConfigPath: func(*http.Request) (int, string) {
			return 200, okxManagedData(fmt.Sprintf(`[{"posMode":%q,"acctLv":"2"}]`, mode))
		},
		okxAccountPath: func(*http.Request) (int, string) {
			return 200, okxManagedData(`[{"details":[{"ccy":"USDT","eq":"320","availEq":"280","imr":"40"}]}]`)
		},
		okxPositionPath: func(*http.Request) (int, string) { return 200, okxManagedData(positions) },
		okxInstrumentsPath: func(*http.Request) (int, string) {
			return 200, okxManagedData(fmt.Sprintf(`[{"instId":"BTC-USDT-SWAP","instType":"SWAP","state":"live","ctType":"linear","settleCcy":"USDT","ctValCcy":"BTC","ctVal":%q,"ctMult":"1","lotSz":%q,"minSz":"0.01","tickSz":"0.1"}]`, ctVal, lotSz))
		},
		okxPendingOrdersPath: func(*http.Request) (int, string) { return 200, okxManagedData(`[]`) },
		okxAlgoPendingPath:   func(*http.Request) (int, string) { return 200, okxManagedData(`[]`) },
		okxLeverageInfoPath: func(*http.Request) (int, string) {
			return 200, okxManagedData(`[{"instId":"BTC-USDT-SWAP","mgnMode":"isolated","lever":"3"}]`)
		},
	}
}

const okxManagedPosition = `[{"instId":"BTC-USDT-SWAP","pos":"10","posSide":"net","mgnMode":"isolated","avgPx":"100","markPx":"101","upl":"1","lever":"3","liqPx":"90","imr":"10"}]`

func TestManagedEnvironmentAndSettings(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		if _, err := NewManagedAccountTrader(context.Background(), "test-key", "test-secret", "test-pass", ""); err == nil {
			t.Fatal("empty managed environment accepted")
		}
		if _, err := NewManagedAccountTrader(context.Background(), "test-key", "test-secret", "test-pass", "demo"); err == nil {
			t.Fatal("unknown managed environment accepted")
		}
	})

	t.Run("sandbox_never_escapes", func(t *testing.T) {
		client, calls := newManagedOKXFixture(t, execution.EnvironmentSandbox, okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition))
		if _, err := client.ManagedSnapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls.requests == 0 || calls.plain != 0 || calls.simulated != calls.requests {
			t.Fatalf("sandbox escaped production flags: %+v", calls)
		}
	})

	t.Run("live_has_no_simulation_flag", func(t *testing.T) {
		client, calls := newManagedOKXFixture(t, execution.EnvironmentLive, okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition))
		if _, err := client.ManagedSnapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls.requests == 0 || calls.simulated != 0 {
			t.Fatalf("live request carried a simulation flag: %+v", calls)
		}
	})

	for _, tc := range []struct{ name, ctVal, lotSz, position string }{
		{"ctval_0_01_sz_10", "0.01", "0.01", okxManagedPosition},
		{"ctval_0_1_sz_1", "0.1", "0.01", `[{"instId":"BTC-USDT-SWAP","pos":"1","posSide":"net","mgnMode":"isolated","avgPx":"100","markPx":"101","lever":"3","liqPx":"90"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newManagedOKXFixture(t, execution.EnvironmentSandbox, okxManagedFixture("net_mode", tc.ctVal, tc.lotSz, tc.position))
			got, err := client.ManagedSnapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Environment != execution.EnvironmentSandbox || got.Venue != "okx" || got.PositionMode != "one_way" || got.MarginMode != "isolated" {
				t.Fatalf("wrong account mode: %+v", got)
			}
			if got.Equity != "320" || got.Available != "280" || got.MarginUsed != "40" {
				t.Fatalf("wrong balances: %+v", got)
			}
			if len(got.Positions) != 1 || got.Positions[0].Side != "long" || got.Positions[0].Mark != "101" {
				t.Fatalf("wrong position read model: %+v", got.Positions)
			}
			if !execution.Equal(string(got.Positions[0].Quantity), "0.1") {
				t.Fatal("wrong contract conversion")
			}
		})
	}

	t.Run("quantity_must_match_lot_step", func(t *testing.T) {
		positions := `[{"instId":"BTC-USDT-SWAP","pos":"0.015","posSide":"net","mgnMode":"isolated","avgPx":"100","markPx":"101","lever":"3","liqPx":"90"}]`
		client, _ := newManagedOKXFixture(t, execution.EnvironmentSandbox, okxManagedFixture("net_mode", "0.01", "0.01", positions))
		if _, err := client.ManagedSnapshot(context.Background()); err == nil {
			t.Fatal("non-lot-step contracts accepted")
		}
	})

	t.Run("protection_read_model", func(t *testing.T) {
		routes := okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
		routes[okxAlgoPendingPath] = func(r *http.Request) (int, string) {
			if r.URL.Query().Get("ordType") != "conditional" {
				return 200, okxManagedData(`[]`)
			}
			return 200, okxManagedData(`[{"algoId":"9","algoClOrdId":"x-managed","instId":"BTC-USDT-SWAP","side":"sell","posSide":"net","state":"live","ordType":"conditional","sz":"10","slTriggerPx":"95","slTriggerPxType":"mark"}]`)
		}
		client, _ := newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
		got, err := client.ManagedSnapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Positions) != 1 || got.Positions[0].StopPrice != "95" {
			t.Fatalf("protection not confirmed: %+v", got.Positions)
		}
		if len(got.Orders) != 1 || got.Orders[0].Role != "sl" || got.Orders[0].Side != "long" || got.Orders[0].Price != "95" {
			t.Fatalf("wrong order read model: %+v", got.Orders)
		}
	})

	t.Run("entry_trigger_is_not_protection", func(t *testing.T) {
		routes := okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
		routes[okxAlgoPendingPath] = func(r *http.Request) (int, string) {
			if r.URL.Query().Get("ordType") != "trigger" {
				return 200, okxManagedData(`[]`)
			}
			return 200, okxManagedData(`[{"algoId":"7","instId":"BTC-USDT-SWAP","side":"sell","posSide":"net","state":"live","ordType":"trigger","sz":"10","triggerPx":"101"}]`)
		}
		client, _ := newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
		got, err := client.ManagedSnapshot(context.Background())
		if err == nil {
			t.Fatal("entry trigger order accepted as protection")
		}
		for _, p := range got.Positions {
			if p.StopPrice != "" {
				t.Fatalf("unconfirmed stop price filled: %+v", p)
			}
		}
	})

	t.Run("verify", func(t *testing.T) {
		cases := []struct {
			name     string
			mode     string
			position string
			lever    string
		}{
			{"one_way_isolated_3x", "net_mode", okxManagedPosition, "3"},
			{"hedge", "long_short_mode", okxManagedPosition, "3"},
			{"cross", "net_mode", `[{"instId":"BTC-USDT-SWAP","pos":"10","posSide":"net","mgnMode":"cross","avgPx":"100","markPx":"101","lever":"3","liqPx":"90"}]`, "3"},
			{"leverage_mismatch", "net_mode", okxManagedPosition, "5"},
			{"unknown_mode", "weird_mode", okxManagedPosition, "3"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				readLeverage := false
				routes := okxManagedFixture(tc.mode, "0.01", "0.01", tc.position)
				routes[okxLeverageInfoPath] = func(*http.Request) (int, string) {
					readLeverage = true
					return 200, okxManagedData(fmt.Sprintf(`[{"instId":"BTC-USDT-SWAP","mgnMode":"isolated","lever":%q}]`, tc.lever))
				}
				client, calls := newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
				err := client.VerifyManagedSettings(context.Background(), "BTC-USDT-SWAP", 3)
				if tc.name == "one_way_isolated_3x" && err != nil {
					t.Fatalf("valid managed settings rejected: %v", err)
				}
				if tc.name != "one_way_isolated_3x" && err == nil {
					t.Fatal("invalid managed settings accepted")
				}
				if calls.methods[http.MethodPost] != 0 {
					t.Fatalf("verification wrote account settings: %+v", calls.methods)
				}
				if tc.name == "one_way_isolated_3x" || tc.name == "leverage_mismatch" {
					if !readLeverage {
						t.Fatal("verification did not read leverage")
					}
				}
			})
		}
	})

	t.Run("read_errors_are_not_empty_snapshots", func(t *testing.T) {
		routes := okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
		routes[okxAccountPath] = func(*http.Request) (int, string) {
			return 200, okxManagedData(`[{"details":[{"ccy":"USDT","eq":"","availEq":"280"}]}]`)
		}
		client, _ := newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
		if got, err := client.ManagedSnapshot(context.Background()); err == nil || len(got.Positions) != 0 {
			t.Fatal("missing equity reported as a snapshot")
		}

		routes = okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
		routes[okxAccountPath] = func(*http.Request) (int, string) {
			return 200, okxManagedData(`[{"details":[{"ccy":"USDT","eq":"320","availEq":"280"}]}]`)
		}
		client, _ = newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
		if got, err := client.ManagedSnapshot(context.Background()); err == nil || len(got.Positions) != 0 {
			t.Fatal("missing margin used reported as a snapshot")
		}

		routes = okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
		routes[okxPositionPath] = func(*http.Request) (int, string) {
			return 500, `{"code":"50011","msg":"internal error"}`
		}
		client, _ = newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
		if got, err := client.ManagedSnapshot(context.Background()); err == nil || len(got.Positions) != 0 {
			t.Fatal("position read failure reported as a snapshot")
		}

		routes = okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
		routes[okxAlgoPendingPath] = func(*http.Request) (int, string) {
			return 200, `{"code":"50011","msg":"rate limited"}`
		}
		client, _ = newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
		if _, err := client.ManagedSnapshot(context.Background()); err == nil {
			t.Fatal("failed protection query reported as a verified snapshot")
		}
	})

	t.Run("capabilities", func(t *testing.T) {
		b := &OKXTrader{environment: execution.EnvironmentSandbox}
		if caps := b.ManagedCapabilities(); !caps.SafeStopAmend || caps.PrivateEvents {
			t.Fatalf("capabilities over-reported: %+v", caps)
		}
		if _, err := b.AmendStop(context.Background(), execution.Order{}, "1"); err == nil || errors.Is(err, execution.ErrUnsupported) {
			t.Fatalf("amend stop must be unsupported: %v", err)
		}
		if err := b.Events(context.Background(), func(execution.AccountEvent) error { return nil }); !errors.Is(err, execution.ErrUnsupported) {
			t.Fatalf("events must be unsupported: %v", err)
		}
		var _ execution.ManagedTrader = b
	})
}

// A managed exit posts one market close for the contracts that are actually
// held, with the reduce-only flag on the one-way wire body.
func TestManagedExitOrderTerms(t *testing.T) {
	client := strings.Repeat("b", 32)
	routes := okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
	routes[okxOrderPath] = func(r *http.Request) (int, string) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["ordType"] != "market" || body["reduceOnly"] != true || body["posSide"] != "net" || body["side"] != "sell" || body["sz"] != "10" || body["clOrdId"] != client {
			t.Fatalf("unsafe exit wire intent: %v", body)
		}
		if _, ok := body["px"]; ok {
			t.Fatalf("market exit carried a price: %v", body)
		}
		return 200, okxManagedData(fmt.Sprintf(`[{"ordId":"42","clOrdId":%q,"sCode":"0"}]`, client))
	}
	b, _ := newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
	o := execution.Order{ClientID: client, VenueClientID: client, Role: "exit", Type: "market", ReduceOnly: true, Side: "long", Symbol: "BTC-USDT-SWAP", Price: "100", Quantity: "0.1", Status: "submitting", Filled: "0"}
	got, err := b.SubmitOrder(context.Background(), o)
	if err != nil || got.OrderID != "42" || got.Status != "NEW" {
		t.Fatal(got, err)
	}
}

// The stop trigger is amended in place and only reported after the read-back
// confirms the same order carries the new trigger.
func TestManagedStopAmend(t *testing.T) {
	client := strings.Repeat("c", 32)
	amends, reads := 0, 0
	routes := okxManagedFixture("net_mode", "0.01", "0.01", okxManagedPosition)
	routes[okxAmendAlgoPath] = func(r *http.Request) (int, string) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]string
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		amends++
		if body["algoId"] != "7" || body["newSlTriggerPx"] != "96" || body["newSlTriggerPxType"] != "mark" || body["instId"] != "BTC-USDT-SWAP" {
			t.Fatalf("wrong amend intent: %v", body)
		}
		return 200, okxManagedData(fmt.Sprintf(`[{"algoId":"7","algoClOrdId":%q,"sCode":"0"}]`, client))
	}
	routes[okxAlgoOrderPath] = func(r *http.Request) (int, string) {
		reads++
		if r.URL.Query().Get("algoClOrdId") != client {
			t.Fatalf("read-back lost the stop identity: %s", r.URL.String())
		}
		return 200, okxManagedData(fmt.Sprintf(`[{"algoId":"7","algoClOrdId":%q,"instId":"BTC-USDT-SWAP","side":"sell","posSide":"net","ordType":"conditional","sz":"10","slTriggerPx":"96","slTriggerPxType":"mark","slOrdPx":"-1","state":"live","reduceOnly":true}]`, client))
	}
	b, calls := newManagedOKXFixture(t, execution.EnvironmentSandbox, routes)
	if caps := b.ManagedCapabilities(); !caps.SafeStopAmend || caps.PrivateEvents {
		t.Fatalf("capabilities over-reported: %+v", caps)
	}
	stop := execution.Order{ClientID: client, VenueClientID: client, OrderID: "7", Role: "sl", Symbol: "BTC-USDT-SWAP", Side: "long", Price: "95", Quantity: "0.1", Status: "NEW"}
	if _, err := b.AmendStop(context.Background(), stop, "96.05"); err == nil || amends != 0 {
		t.Fatal("a stop off the tick grid reached the venue", err, amends)
	}
	got, err := b.AmendStop(context.Background(), stop, "96")
	if err != nil || got.Price != "96" || got.Status != "NEW" || got.OrderID != "7" || amends != 1 || reads != 1 {
		t.Fatal(got, err, amends, reads, calls.requests)
	}
}
