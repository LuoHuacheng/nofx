package binance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"nofx/trader/execution"
	"testing"

	"github.com/adshao/go-binance/v2/futures"
)

// Binance USDⓈ-M hosts come from the pinned SDK constants (go-binance v2.8.9,
// checked 2026-11-06): production https://fapi.binance.com and testnet
// https://testnet.binancefuture.com. The official docs
// (https://developers.binance.com/docs/derivatives/usds-margined-futures/general-info)
// were not fetched offline.
func newManagedBinanceFixture(t *testing.T, respond func(*http.Request) (int, string)) (*FuturesTrader, *int, *int) {
	t.Helper()
	requests, writes := 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			writes++
		}
		if r.URL.Path == "/fapi/v1/time" {
			fmt.Fprint(w, `{"serverTime":1700000000000}`)
			return
		}
		status, body := respond(r)
		if body == "" {
			t.Fatalf("unexpected Binance endpoint %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	b, err := newManagedAccountTrader(context.Background(), "test-key", "test-secret", "", execution.EnvironmentSandbox, &managedEndpoint{baseURL: s.URL, client: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return b, &requests, &writes
}

func TestManagedEnvironmentAndSettings(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		if _, err := NewManagedAccountTrader(context.Background(), "test-key", "test-secret", "", ""); err == nil {
			t.Fatal("empty managed environment accepted")
		}
		if _, err := NewManagedAccountTrader(context.Background(), "test-key", "test-secret", "", "demo"); err == nil {
			t.Fatal("unknown managed environment accepted")
		}
		if got, err := managedBinanceEndpoint(execution.EnvironmentSandbox); err != nil || got != futures.BaseApiTestnetUrl {
			t.Fatalf("sandbox endpoint %q %v", got, err)
		}
		if got, err := managedBinanceEndpoint(execution.EnvironmentLive); err != nil || got != futures.BaseApiMainUrl {
			t.Fatalf("live endpoint %q %v", got, err)
		}
	})

	t.Run("verify", func(t *testing.T) {
		cases := []struct {
			name     string
			dual     bool
			margin   string
			leverage string
		}{
			{"one_way_isolated_3x", false, "isolated", "3"},
			{"hedge", true, "isolated", "3"},
			{"cross", false, "cross", "3"},
			{"leverage_mismatch", false, "isolated", "5"},
			{"unknown_mode", false, "", "3"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				b, requests, writes := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
					switch r.URL.Path {
					case "/fapi/v1/positionSide/dual":
						return 200, fmt.Sprintf(`{"dualSidePosition":%v}`, tc.dual)
					case "/fapi/v2/positionRisk":
						return 200, fmt.Sprintf(`[{"symbol":"BTCUSDT","positionAmt":"0","entryPrice":"0","markPrice":"100","liquidationPrice":"0","marginType":%q,"leverage":%q,"positionSide":"BOTH"}]`, tc.margin, tc.leverage)
					}
					return 0, ""
				})
				err := b.VerifyManagedSettings(context.Background(), "BTCUSDT", 3)
				if tc.name == "one_way_isolated_3x" && err != nil {
					t.Fatalf("valid managed settings rejected: %v", err)
				}
				if tc.name != "one_way_isolated_3x" && err == nil {
					t.Fatal("invalid managed settings accepted")
				}
				if *requests == 0 {
					t.Fatal("verification did not read the exchange")
				}
				if *writes != 0 {
					t.Fatalf("verification wrote account settings: %d", *writes)
				}
			})
		}
	})

	t.Run("snapshot", func(t *testing.T) {
		const account = `{"canTrade":true,"multiAssetsMargin":false,"totalMarginBalance":"1000.5","availableBalance":"800","totalPositionInitialMargin":"10","positions":[{"symbol":"BTCUSDT","positionAmt":"0.1","entryPrice":"100","positionSide":"BOTH","isolated":true,"positionInitialMargin":"10"}]}`
		const risk = `[{"symbol":"BTCUSDT","positionAmt":"0.1","entryPrice":"100","markPrice":"101","liquidationPrice":"90","marginType":"isolated","leverage":"3","positionSide":"BOTH"}]`
		const algos = `[{"algoId":7,"clientAlgoId":"x-managed","symbol":"BTCUSDT","side":"SELL","positionSide":"BOTH","orderType":"STOP_MARKET","quantity":"0.1","triggerPrice":"95","algoStatus":"NEW"}]`
		b, _, _ := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case "/fapi/v1/positionSide/dual":
				return 200, `{"dualSidePosition":false}`
			case "/fapi/v2/account":
				return 200, account
			case "/fapi/v2/positionRisk":
				return 200, risk
			case "/fapi/v1/openOrders":
				return 200, `[]`
			case "/fapi/v1/openAlgoOrders":
				return 200, algos
			}
			return 0, ""
		})
		got, err := b.ManagedSnapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got.Venue != "binance" || got.Environment != execution.EnvironmentSandbox || got.PositionMode != "one_way" || got.MarginMode != "isolated" {
			t.Fatalf("wrong account mode: %+v", got)
		}
		if got.Equity != "1000.5" || got.Available != "800" || got.MarginUsed != "10" {
			t.Fatalf("wrong balances: %+v", got)
		}
		if len(got.Positions) != 1 || got.Positions[0].Side != "long" || got.Positions[0].StopPrice != "95" || got.Positions[0].Mark != "101" || got.Positions[0].Liquidation != "90" {
			t.Fatalf("wrong position read model: %+v", got.Positions)
		}
		if !execution.Equal(string(got.Positions[0].Quantity), "0.1") {
			t.Fatal("wrong contract conversion")
		}
		if len(got.Orders) != 1 || got.Orders[0].Role != "sl" || got.Orders[0].OrderID != "7" || got.Orders[0].Price != "95" {
			t.Fatalf("wrong order read model: %+v", got.Orders)
		}
	})

	t.Run("read_errors_are_not_empty_snapshots", func(t *testing.T) {
		b, _, _ := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case "/fapi/v1/positionSide/dual":
				return 200, `{"dualSidePosition":false}`
			case "/fapi/v2/account":
				return 200, `{"canTrade":true,"totalMarginBalance":"","availableBalance":"800","positions":[]}`
			}
			return 0, ""
		})
		if got, err := b.ManagedSnapshot(context.Background()); err == nil || len(got.Positions) != 0 {
			t.Fatal("missing equity reported as a snapshot")
		}

		b, _, _ = newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case "/fapi/v1/positionSide/dual":
				return 200, `{"dualSidePosition":false}`
			case "/fapi/v2/account":
				return 500, `{"code":-1001,"msg":"internal error"}`
			}
			return 0, ""
		})
		if got, err := b.ManagedSnapshot(context.Background()); err == nil || got.Equity != "" {
			t.Fatal("account read failure reported as a snapshot")
		}

		b, _, _ = newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case "/fapi/v1/positionSide/dual":
				return 200, `{"dualSidePosition":false}`
			case "/fapi/v2/account":
				return 200, `{"canTrade":true,"totalMarginBalance":"100","availableBalance":"100","positions":[]}`
			case "/fapi/v2/positionRisk":
				return 200, `[]`
			case "/fapi/v1/openOrders":
				return 200, `[]`
			case "/fapi/v1/openAlgoOrders":
				return 500, `{"code":-1001,"msg":"internal error"}`
			}
			return 0, ""
		})
		if _, err := b.ManagedSnapshot(context.Background()); err == nil {
			t.Fatal("failed protection query reported as a verified snapshot")
		}
	})

	t.Run("unsupported_capabilities", func(t *testing.T) {
		b := &FuturesTrader{managedEnvironment: execution.EnvironmentSandbox}
		if caps := b.ManagedCapabilities(); caps.SafeStopAmend || caps.PrivateEvents {
			t.Fatalf("capabilities over-reported: %+v", caps)
		}
		if _, err := b.AmendStop(context.Background(), execution.Order{}, "1"); !errors.Is(err, execution.ErrUnsupported) {
			t.Fatalf("amend stop must be unsupported: %v", err)
		}
		if err := b.Events(context.Background(), func(execution.AccountEvent) error { return nil }); !errors.Is(err, execution.ErrUnsupported) {
			t.Fatalf("events must be unsupported: %v", err)
		}
		var _ execution.ManagedTrader = b
	})
}
