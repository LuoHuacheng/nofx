package okx

import (
	"context"
	"net/http"
	"testing"
	"time"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

// okxBillsFixture is a recorded /api/v5/account/bills page (docs 2026-11-06):
// billId is the venue's own unique id, ts is epoch milliseconds, and type 8 is
// funding while 2 is trade and 9 is a fee debit.
const okxBillsFixture = `[{"billId":"601","type":"8","subType":"173","ts":"1700000000001","bal":"-0.0321","ccy":"USDT","instId":"BTC-USDT-SWAP","ordId":""},
{"billId":"602","type":"2","subType":"1","ts":"1700000000002","bal":"12.5","ccy":"USDT","instId":"BTC-USDT-SWAP","ordId":"555"},
{"billId":"603","type":"9","subType":"","ts":"1700000000003","bal":"-0.0125","ccy":"USDT","instId":"BTC-USDT-SWAP","ordId":"555"},
{"billId":"604","type":"77","subType":"","ts":"1700000000004","bal":"3.3","ccy":"USDT","instId":"","ordId":""}]`

func TestManagedHistoryReadsBillsOnce(t *testing.T) {
	trader := okxFixture(t, "net", `[]`, func(r *http.Request) string {
		if r.URL.Path != okxBillsPath {
			return ""
		}
		if r.URL.Query().Get("instType") != "SWAP" {
			t.Fatal("bills must be scoped to SWAP")
		}
		if r.URL.Query().Get("begin") == "" {
			t.Fatal("bills must be read from a window start")
		}
		return okxBillsFixture
	})
	reader := trader.ManagedHistory()
	if reader == nil {
		t.Fatal("the managed trader exposes no history reader")
	}
	events, _, complete, err := reader.History(context.Background(), time.Unix(0, 0), "")
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("a short page was reported as unfinished")
	}
	if len(events) != 4 {
		t.Fatalf("read %d bills, wanted 4", len(events))
	}
	want := map[string]string{
		"okx:bill:601": risk.KindFunding,
		"okx:bill:602": risk.KindCashflow,
		"okx:bill:603": risk.KindFee,
		"okx:bill:604": risk.KindFundingGap,
	}
	for _, e := range events {
		if e.Kind != want[e.ID] {
			t.Fatalf("%s mapped to %q, wanted %q", e.ID, e.Kind, want[e.ID])
		}
		if _, err := execution.Number(string(e.Amount)); err != nil {
			t.Fatalf("%s carries an unparsable amount %q", e.ID, e.Amount)
		}
	}
}

// TestManagedHistoryFailsOnUnidentifiedBills proves a bill the ledger cannot
// attribute fails the window instead of being dropped: it moved money, so
// skipping it would silently widen the day's loss budget.
func TestManagedHistoryFailsOnUnidentifiedBills(t *testing.T) {
	trader := okxFixture(t, "net", `[]`, func(r *http.Request) string {
		if r.URL.Path != okxBillsPath {
			return ""
		}
		return `[{"billId":"","type":"8","ts":"1700000000001","bal":"-0.03","ccy":"USDT"},{"billId":"701","type":"8","ts":"1700000000002","bal":"-0.04","ccy":"USDT"}]`
	})
	if _, _, _, err := trader.ManagedHistory().History(context.Background(), time.Unix(0, 0), ""); err == nil {
		t.Fatal("an unidentifiable bill was silently dropped")
	}
}

// TestManagedHistoryWindowEndsAtLastRow proves the completed window ends at the
// newest row read, so a row that lands later with an older timestamp is still
// inside the overlapping replay.
func TestManagedHistoryWindowEndsAtLastRow(t *testing.T) {
	trader := okxFixture(t, "net", `[]`, func(r *http.Request) string {
		if r.URL.Path != okxBillsPath {
			return ""
		}
		return `[{"billId":"702","type":"8","ts":"1700000005000","bal":"-0.04","ccy":"USDT"}]`
	})
	_, next, complete, err := trader.ManagedHistory().History(context.Background(), time.Unix(0, 0), "")
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("a short page was reported as unfinished")
	}
	if want := time.UnixMilli(1700000005000).UTC().Format(time.RFC3339); next != want {
		t.Fatalf("the window ended at %q, wanted %q", next, want)
	}
}
