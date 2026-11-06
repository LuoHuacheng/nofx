package binance

import (
	"context"
	"net/http"
	"testing"
	"time"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

// incomeFixture is a recorded /fapi/v1/income page (docs 2026-11-06): tranId is
// the venue's own unique id and time is epoch milliseconds.
const incomeFixture = `[{"symbol":"BTCUSDT","incomeType":"FUNDING_FEE","income":"-0.0152","asset":"USDT","time":1700000000001,"tranId":101},
{"symbol":"BTCUSDT","incomeType":"REALIZED_PNL","income":"12.5","asset":"USDT","time":1700000000002,"tranId":102},
{"symbol":"","incomeType":"COMMISSION","income":"-0.0125","asset":"USDT","time":1700000000003,"tranId":103},
{"symbol":"","incomeType":"SOMETHING_NEW","income":"7.25","asset":"USDT","time":1700000000004,"tranId":104},
{"symbol":"","incomeType":"TRANSFER","income":"-100","asset":"USDT","time":1700000000005,"tranId":105}]`

func TestManagedHistoryReadsIncomeOnce(t *testing.T) {
	b, _, _ := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/fapi/v1/income" {
			return 200, ""
		}
		return 200, incomeFixture
	})
	reader := b.ManagedHistory()
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
	if len(events) != 5 {
		t.Fatalf("read %d income rows, wanted 5", len(events))
	}
	want := map[string]string{
		"binance:income:101": risk.KindFunding,
		"binance:income:102": risk.KindCashflow,
		"binance:income:103": risk.KindFee,
		"binance:income:104": risk.KindFundingGap,
		"binance:income:105": risk.KindCashflow,
	}
	for _, e := range events {
		if e.ID == "" || e.AccountKey != "" {
			t.Fatalf("event id/key not from the venue: %+v", e)
		}
		if want[e.ID] == "" {
			t.Fatalf("unknown event id %q", e.ID)
		}
		if e.Kind != want[e.ID] {
			t.Fatalf("%s mapped to %q, wanted %q", e.ID, e.Kind, want[e.ID])
		}
		if _, err := execution.Number(string(e.Amount)); err != nil {
			t.Fatalf("%s carries an unparsable amount %q", e.ID, e.Amount)
		}
	}
	// Replaying the same page must not create new identities.
	again, _, _, err := reader.History(context.Background(), time.Unix(0, 0), "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range again {
		if again[i].ID != events[i].ID {
			t.Fatal("a replayed income row changed its identity")
		}
	}
}

func TestManagedHistoryPaging(t *testing.T) {
	b, _, _ := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/fapi/v1/income" {
			return 200, ""
		}
		rows := `[`
		for i := 0; i < binanceIncomeLimit; i++ {
			if i > 0 {
				rows += ","
			}
			rows += `{"incomeType":"FUNDING_FEE","income":"-0.01","asset":"USDT","time":1700000000000,"tranId":` + itoa(200+i) + `}`
		}
		return 200, rows + `]`
	})
	events, next, complete, err := b.ManagedHistory().History(context.Background(), time.Unix(0, 0), "")
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Fatal("a full page was reported as the last page")
	}
	// The cursor stops at the boundary millisecond itself: funding settles at
	// one timestamp across many symbols, so advancing past it would skip them.
	if want := "1700000000000"; next != want {
		t.Fatalf("the page cursor = %q, wanted the boundary %q", next, want)
	}
	if len(events) != binanceIncomeLimit {
		t.Fatalf("read %d rows from a full page", len(events))
	}
}

// TestManagedHistoryFailsOnUnidentifiedRows proves a row without the venue's
// own id fails the window instead of being dropped: it moved money, so a
// silent skip would widen the day's loss budget.
func TestManagedHistoryFailsOnUnidentifiedRows(t *testing.T) {
	b, _, _ := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/fapi/v1/income" {
			return 200, ""
		}
		return 200, `[{"incomeType":"FUNDING_FEE","income":"-0.01","asset":"USDT","time":1700000000000,"tranId":0}]`
	})
	if _, _, _, err := b.ManagedHistory().History(context.Background(), time.Unix(0, 0), ""); err == nil {
		t.Fatal("an income row without a tranId was silently dropped")
	}
}

// TestManagedHistoryWindowEndsAtLastRow proves the completed window ends at the
// newest row read rather than the wall clock.
func TestManagedHistoryWindowEndsAtLastRow(t *testing.T) {
	b, _, _ := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/fapi/v1/income" {
			return 200, ""
		}
		return 200, `[{"incomeType":"FUNDING_FEE","income":"-0.01","asset":"USDT","time":1700000007000,"tranId":301}]`
	})
	_, next, complete, err := b.ManagedHistory().History(context.Background(), time.Unix(0, 0), "")
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("a short page was reported as unfinished")
	}
	if want := time.UnixMilli(1700000007000).UTC().Format(time.RFC3339); next != want {
		t.Fatalf("the window ended at %q, wanted %q", next, want)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		out = string(rune('0'+v%10)) + out
		v /= 10
	}
	return out
}
