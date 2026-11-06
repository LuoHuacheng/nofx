package okx

import (
	"context"
	"encoding/json"
	"net/http"
	"nofx/trader/execution"
	"testing"
)

func TestProtectionResolvesLinkedExecution(t *testing.T) {
	for _, tc := range []struct{ name, state, fill, expected string }{
		{"filled", "filled", "1.23", "FILLED"},
		{"live", "live", "0", "TRIGGERED"},
		{"partial", "partially_filled", "0.5", "TRIGGERED"},
		{"cancelled partial", "canceled", "0.5", "CANCELED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := okxIntent()
			o.Role = "sl"
			o.Type = ""
			o.OrderID = "algo-1"
			client := okxFixture(t, "net_mode", `[]`, func(r *http.Request) string {
				if r.Method != "GET" {
					t.Fatal("lookup mutated exchange")
				}
				if r.URL.Path == okxAlgoOrderPath {
					data := map[string]any{"algoId": o.OrderID, "algoClOrdId": o.ClientID, "instId": o.Symbol, "sz": "1.23", "side": "sell", "posSide": "net", "ordType": "conditional", "slTriggerPx": "80000", "slTriggerPxType": "mark", "slOrdPx": "-1", "reduceOnly": true, "state": "effective", "ordIdList": []string{"exit-1"}}
					b, _ := json.Marshal([]any{data})
					return string(b)
				}
				if r.URL.Path == okxOrderPath && r.URL.Query().Get("ordId") == "exit-1" {
					data := map[string]any{"ordId": "exit-1", "instId": o.Symbol, "sz": "1.23", "accFillSz": tc.fill, "avgPx": "79990", "side": "sell", "posSide": "net", "ordType": "market", "reduceOnly": true, "state": tc.state}
					b, _ := json.Marshal([]any{data})
					return string(b)
				}
				t.Fatalf("unexpected lookup %s", r.URL.Path)
				return ""
			})
			got, err := client.LookupOrder(context.Background(), o)
			expectedFill, _ := execution.Multiply(tc.fill, "0.01")
			if err != nil || got.Status != tc.expected || got.OrderID != o.OrderID || !execution.Equal(string(got.Filled), string(expectedFill)) {
				t.Fatalf("receipt %+v error %v", got, err)
			}
		})
	}
}

func TestProtectionRejectsUnrelatedOrUnconfirmedLinkedExecution(t *testing.T) {
	for _, field := range []string{"ordId", "instId", "side", "reduceOnly", "sz", "accFillSz", "state"} {
		t.Run(field, func(t *testing.T) {
			o := okxIntent()
			o.Role = "tp"
			o.Type = ""
			o.OrderID = "algo-1"
			client := okxFixture(t, "net_mode", `[]`, func(r *http.Request) string {
				if r.Method != "GET" {
					t.Fatal("lookup mutated exchange")
				}
				if r.URL.Path == okxAlgoOrderPath {
					b, _ := json.Marshal([]any{map[string]any{"algoId": o.OrderID, "algoClOrdId": o.ClientID, "instId": o.Symbol, "sz": "1.23", "side": "sell", "posSide": "net", "ordType": "conditional", "tpTriggerPx": "80000", "tpTriggerPxType": "mark", "tpOrdPx": "-1", "reduceOnly": true, "state": "effective", "ordId": "exit-1"}})
					return string(b)
				}
				data := map[string]any{"ordId": "exit-1", "instId": o.Symbol, "sz": "1.23", "accFillSz": "1.23", "avgPx": "80000", "side": "sell", "posSide": "net", "ordType": "market", "reduceOnly": true, "state": "filled"}
				data[field] = "wrong"
				if field == "sz" {
					data[field] = "2"
				}
				if field == "accFillSz" {
					data[field] = "0"
				}
				b, _ := json.Marshal([]any{data})
				return string(b)
			})
			if _, err := client.LookupOrder(context.Background(), o); err == nil {
				t.Fatal("accepted inconsistent linked exit")
			}
		})
	}
}

func TestProtectionSplitExecutions(t *testing.T) {
	for _, tc := range []struct {
		name, secondState, secondFill, previous, want string
		duplicate                                     bool
	}{
		{"partially executing", "live", "0", "0", "TRIGGERED", false},
		{"fully filled", "filled", "0.63", "0.006", "FILLED", false},
		{"duplicate linkage", "filled", "0.63", "0", "", true},
		{"decreasing fill", "live", "0", "0.01", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := okxIntent()
			o.Role = "sl"
			o.Type = ""
			o.OrderID = "algo"
			o.Filled = execution.Decimal(tc.previous)
			client := okxFixture(t, "net_mode", `[]`, func(r *http.Request) string {
				if r.Method != "GET" {
					t.Fatal("lookup mutated exchange")
				}
				if r.URL.Path == okxAlgoOrderPath {
					ids := []string{"a", "b"}
					if tc.duplicate {
						ids = []string{"a", "a"}
					}
					b, _ := json.Marshal([]any{map[string]any{"algoId": o.OrderID, "algoClOrdId": o.ClientID, "instId": o.Symbol, "sz": "1.23", "side": "sell", "posSide": "net", "ordType": "conditional", "slTriggerPx": "80000", "slTriggerPxType": "mark", "slOrdPx": "-1", "reduceOnly": true, "state": "effective", "ordIdList": ids}})
					return string(b)
				}
				id := r.URL.Query().Get("ordId")
				sz, fill, state := "0.6", "0.6", "filled"
				if id == "b" {
					sz, fill, state = "0.63", tc.secondFill, tc.secondState
				}
				b, _ := json.Marshal([]any{map[string]any{"ordId": id, "instId": o.Symbol, "sz": sz, "accFillSz": fill, "avgPx": "79990", "side": "sell", "posSide": "net", "ordType": "market", "reduceOnly": true, "state": state}})
				return string(b)
			})
			got, err := client.LookupOrder(context.Background(), o)
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid linked exit accepted")
				}
				return
			}
			if err != nil || got.Status != tc.want {
				t.Fatalf("receipt %+v error %v", got, err)
			}
		})
	}
}
