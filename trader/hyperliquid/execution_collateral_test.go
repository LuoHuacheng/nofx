package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	hl "github.com/sonirico/go-hyperliquid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecutionSnapshotReadsUnifiedCollateralForOwner(t *testing.T) {
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		switch req["type"] {
		case "clearinghouseState":
			fmt.Fprint(w, `{"marginSummary":{"accountValue":"0"},"withdrawable":"0","assetPositions":[]}`)
		case "spotClearinghouseState":
			reads++
			if req["user"] != "owner" {
				t.Error("read a different owner")
			}
			fmt.Fprint(w, `{"balances":[{"coin":"USDC","total":"280.5882","hold":"20"}]}`)
		case "openOrders":
			fmt.Fprint(w, `[]`)
		case "userFees":
			fmt.Fprint(w, `{"userCrossRate":"0.00045","userAddRate":"0.00015"}`)
		default:
			t.Error("unexpected request", req)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	key, _ := crypto.GenerateKey()
	meta := &hl.Meta{Universe: []hl.AssetInfo{{Name: "BTC", SzDecimals: 5, MaxLeverage: 50}}}
	ex := hl.NewExchange(context.Background(), key, server.URL, meta, "", "owner", &hl.SpotMeta{}, nil, hl.ExchangeOptClientOptions(hl.ClientOptHTTPClient(server.Client())))
	b := &HyperliquidTrader{ctx: context.Background(), apiURL: server.URL, walletAddr: "owner", privateKey: key, meta: meta, exchange: ex, isUnifiedAccount: true}
	a, e := b.AccountSnapshot(context.Background(), "BTC")
	if e != nil || reads != 1 || a.Equity != "280.588200000000" || a.Available != "260.588200000000" {
		t.Fatal(a, e, reads)
	}
	b.isUnifiedAccount = false
	a, e = b.AccountSnapshot(context.Background(), "BTC")
	if e != nil || reads != 1 || a.Equity != "0" || a.Available != "0" {
		t.Fatal("separate account must not spend Spot collateral", a, e)
	}
}

func TestUnifiedExecutionCollateralUsesSpotEquityWithoutDoubleCounting(t *testing.T) {
	equity, available, e := unifiedExecutionCollateral([]byte(`{"balances":[{"coin":"USDC","total":"280.5882","hold":"20.125"},{"coin":"UBTC","total":"5","hold":"0"}]}`))
	if e != nil || equity != "280.588200000000" || available != "260.463200000000" {
		t.Fatal(equity, available, e)
	}
	equity, available, e = unifiedExecutionCollateral([]byte(`{"balances":[{"coin":"USDC","total":"0.0","hold":"0.0"}]}`))
	if e != nil || equity != "0.000000000000" || available != "0.000000000000" {
		t.Fatal(equity, available, e)
	}
	_, available, e = unifiedExecutionCollateral([]byte(`{"balances":[{"coin":"USDC","total":"2","hold":"3"}]}`))
	if e != nil || available != "0.000000000000" {
		t.Fatal(available, e)
	}
}

func TestUnifiedCollateralFailsClosedOnIncompleteOrInvalidReservations(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"balances":null}`, `{"balances":[{"coin":"USDC","total":"280"}]}`,
		`{"balances":[{"coin":"USDC","total":"280","hold":"-1"}]}`,
		`{"balances":[{"coin":"USDC","total":"1e3","hold":"0"}]}`,
		`{"balances":[{"coin":"USDC","total":"280","hold":"0"},{"coin":"USDC","total":"280","hold":"0"}]}`,
	} {
		if _, _, e := unifiedExecutionCollateral([]byte(body)); e == nil {
			t.Fatal("invalid collateral accepted", body)
		}
	}
}
