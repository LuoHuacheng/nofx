package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	hl "github.com/sonirico/go-hyperliquid"
	"net/http"
	"net/http/httptest"
	"nofx/trader/execution"
	"nofx/trader/types"
	"strings"
	"testing"
	"time"
)

func TestPlanningHyperliquidNativeReceiptsAndUnknownLookup(t *testing.T) {
	key, _ := crypto.GenerateKey()
	agent := crypto.PubkeyToAddress(key.PublicKey).Hex()
	clientID := strings.Repeat("b", 32)
	var actions []map[string]any
	unknown := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/exchange" {
			a := request["action"].(map[string]any)
			actions = append(actions, a)
			switch a["type"] {
			case "order":
				builder, ok := a["builder"].(map[string]any)
				if !ok || builder["b"] != defaultBuilder.Builder || builder["f"] != float64(defaultBuilder.Fee) {
					t.Errorf("planning order lost NOFX Builder routing: %v", a["builder"])
				}
				fmt.Fprint(w, `{"status":"ok","response":{"type":"order","data":{"statuses":[{"resting":{"oid":12345}}]}}}`)
			case "cancelByCloid":
				fmt.Fprint(w, `{"status":"ok","response":{"type":"cancel","data":{"statuses":["success"]}}}`)
			default:
				t.Error(a)
			}
			return
		}
		switch request["type"] {
		case "extraAgents":
			fmt.Fprintf(w, `[{"address":%q,"validUntil":%d}]`, agent, time.Now().Add(time.Hour).UnixMilli())
		case "orderStatus":
			if unknown {
				fmt.Fprint(w, `{"status":"unknownOid"}`)
			} else {
				fmt.Fprintf(w, `{"status":"order","order":{"order":{"coin":"BTC","side":"B","limitPx":"100000","sz":"0.0005","origSz":"0.001","oid":12345,"timestamp":1,"cloid":%q},"status":"open","statusTimestamp":1}}`, "0x"+clientID)
			}
		default:
			t.Error(request)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer s.Close()
	meta := &hl.Meta{Universe: []hl.AssetInfo{{Name: "BTC", SzDecimals: 5, MaxLeverage: 50}}}
	ex := hl.NewExchange(context.Background(), key, s.URL, meta, "", "owner", &hl.SpotMeta{}, nil, hl.ExchangeOptClientOptions(hl.ClientOptHTTPClient(s.Client())))
	tr := &HyperliquidTrader{ctx: context.Background(), apiURL: s.URL, privateKey: key, walletAddr: "owner", meta: meta, exchange: ex}
	b := tr
	o := execution.Order{ClientID: clientID, Symbol: "BTC", Role: "entry", Side: "long", Price: "100000", Quantity: "0.001", Status: "submitting", Filled: "0"}
	got, e := b.SubmitOrder(context.Background(), o)
	if e != nil || got.OrderID != "12345" || got.Status != "NEW" {
		t.Fatalf("%+v %v", got, e)
	}
	wire := actions[0]["orders"].([]any)[0].(map[string]any)
	if wire["c"] != "0x"+clientID || wire["r"] != false || wire["p"] != "100000" || wire["s"] != "0.001" {
		t.Fatal(wire)
	}
	got, e = b.LookupOrder(context.Background(), o)
	if e != nil || got.Status != "PARTIALLY_FILLED" || got.Filled != "0.000500000000" {
		t.Fatalf("%+v %v", got, e)
	}
	status, err := tr.GetOrderStatus("BTC", "12345")
	if err != nil || status["status"] != "PARTIALLY_FILLED" || status["executedQty"] != 0.0005 {
		t.Fatalf("native status: %v %v", status, err)
	}
	unknown = true
	if status, err = tr.GetOrderStatus("BTC", "12345"); err == nil {
		t.Fatalf("missing order fabricated status: %v", status)
	}
	got, e = b.LookupOrder(context.Background(), o)
	if e == nil || got.Status == "FILLED" {
		t.Fatal("unknown order fabricated fill")
	}
	o.Role = "sl"
	o.Price = "99000"
	if _, e = b.SubmitOrder(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	wire = actions[1]["orders"].([]any)[0].(map[string]any)
	if wire["r"] != true || wire["b"] != false {
		t.Fatal("protection not reduce only", wire)
	}
	// The original NOFX API must return the exchange ID, never a timestamp.
	legacy, e := tr.PlaceLimitOrder(&types.LimitOrderRequest{Symbol: "BTC", Side: "BUY", Price: 100000, Quantity: 0.001, ClientID: "grid-2-456", PostOnly: true, ReduceOnly: true})
	if e != nil || legacy.OrderID != "12345" {
		t.Fatalf("legacy API: %+v %v", legacy, e)
	}
	last := actions[len(actions)-1]["orders"].([]any)[0].(map[string]any)
	canonical, _ := execution.CanonicalClientID("grid-2-456")
	if last["c"] != "0x"+canonical || last["r"] != true || last["t"].(map[string]any)["limit"].(map[string]any)["tif"] != "Alo" {
		t.Fatalf("legacy intent changed: %v", last)
	}
	if e = b.CancelExecutionOrder(context.Background(), o); e != nil {
		t.Fatal(e)
	}
}

func TestMarketEntryUsesIOCAndKeepsBuilder(t *testing.T) {
	key, _ := crypto.GenerateKey()
	agent := crypto.PubkeyToAddress(key.PublicKey).Hex()
	filled := "0.001"
	resting := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/exchange" {
			fmt.Fprintf(w, `[{"address":%q,"validUntil":%d}]`, agent, time.Now().Add(time.Hour).UnixMilli())
			return
		}
		action := request["action"].(map[string]any)
		wire := action["orders"].([]any)[0].(map[string]any)
		if wire["t"].(map[string]any)["limit"].(map[string]any)["tif"] != "Ioc" || wire["p"] != "100100" || wire["s"] != "0.001" || wire["r"] != false {
			t.Errorf("market did not use bounded IOC: %v", wire)
		}
		builder := action["builder"].(map[string]any)
		if builder["b"] != defaultBuilder.Builder || builder["f"] != float64(defaultBuilder.Fee) {
			t.Error("lost builder routing")
		}
		if resting {
			fmt.Fprint(w, `{"status":"ok","response":{"type":"order","data":{"statuses":[{"resting":{"oid":12345}}]}}}`)
			return
		}
		fmt.Fprintf(w, `{"status":"ok","response":{"type":"order","data":{"statuses":[{"filled":{"oid":12345,"totalSz":%q,"avgPx":"100001"}}]}}}`, filled)
	}))
	defer s.Close()
	meta := &hl.Meta{Universe: []hl.AssetInfo{{Name: "BTC", SzDecimals: 5, MaxLeverage: 50}}}
	ex := hl.NewExchange(context.Background(), key, s.URL, meta, "", "owner", &hl.SpotMeta{}, nil, hl.ExchangeOptClientOptions(hl.ClientOptHTTPClient(s.Client())))
	b := &HyperliquidTrader{ctx: context.Background(), apiURL: s.URL, privateKey: key, walletAddr: "owner", meta: meta, exchange: ex}
	o := execution.Order{Type: "market", ClientID: strings.Repeat("b", 32), Symbol: "BTC", Role: "entry", Side: "long", Price: "100100", Quantity: "0.001", Filled: "0"}
	got, err := b.SubmitOrder(context.Background(), o)
	if err != nil || got.Status != "FILLED" || got.Filled != "0.001" {
		t.Fatal(got, err)
	}
	filled = "0.0005"
	got, err = b.SubmitOrder(context.Background(), o)
	if err != nil || got.Status != "CANCELED" || got.Filled != execution.Decimal(filled) {
		t.Fatal("IOC remainder must not rest or be resubmitted", got, err)
	}
	resting = true
	if _, err = b.SubmitOrder(context.Background(), o); err == nil {
		t.Fatal("resting receipt accepted for immediate order")
	}
}
