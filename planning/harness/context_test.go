package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"nofx/planning/market"
)

type historyFixture struct {
	queries  []market.CandleQuery
	start    int64
	pageSize int
	fail     bool
}

func (f *historyFixture) Candles(_ context.Context, q market.CandleQuery) ([]market.Candle, error) {
	f.queries = append(f.queries, q)
	if f.fail && len(f.queries) == 2 {
		return nil, errors.New("history unavailable")
	}
	step := market.IntervalSeconds(q.Interval)
	end := (q.To - 1) / step * step
	count := q.Limit
	if f.pageSize > 0 {
		count = min(count, f.pageSize)
	}
	rows := []market.Candle{}
	for stamp := end - int64(count-1)*step; stamp <= end; stamp += step {
		if stamp < f.start {
			continue
		}
		rows = append(rows, market.Candle{Time: stamp, Open: "100", High: "101", Low: "99", Close: "100", Volume: "1"})
	}
	return rows, nil
}

func TestPlanningHistoryIgnoresViewportOnEveryVenue(t *testing.T) {
	for _, venue := range []string{"binance", "hyperliquid", "okx", "bitget"} {
		for _, offset := range []int64{-3600, -120 * 86400, 86400} {
			f := &historyFixture{pageSize: 200}
			e := Engine{Charts: f}
			now := time.Now().Unix()
			input := Input{Venue: venue, Symbol: "BTCUSDT", Interval: "1h", VisibleFrom: now + offset, VisibleTo: now + offset + 600}
			progress := 0
			data, err := e.chartContext(context.Background(), &input, func(ev Event) error {
				if ev.Type == "tool_progress" {
					progress++
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if input.Analysis.HistoryPolicy != "latest_history" || progress < 10 {
				t.Fatal("missing history provenance or progress")
			}
			for i, want := range []int{1200, 500, 500} {
				w := input.Analysis.Windows[i]
				if w.Count != want || w.TargetCount != want || w.Truncated || now-w.To >= market.IntervalSeconds(w.Interval) {
					t.Fatalf("wrong %s window: %+v", venue, w)
				}
			}
			for _, q := range f.queries {
				if q.From != 0 || q.Limit > 300 {
					t.Fatalf("viewport limited history: %+v", q)
				}
			}
			primary := data["primary"].(map[string]any)
			if len(primary["candles"].([][]any)) != 240 {
				t.Fatal("recent sequence missing")
			}
		}
	}
}

func TestLimitedHistoryAndFailedPaginationAreDisclosed(t *testing.T) {
	now := time.Now().Unix()
	// Include a daily candle regardless of the UTC hour when this test runs.
	f := &historyFixture{start: now - 36*3600}
	e := Engine{Charts: f}
	input := Input{Venue: "okx", Symbol: "BTC-USDT-SWAP", Interval: "1h"}
	if _, err := e.chartContext(context.Background(), &input, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	w := input.Analysis.Windows[0]
	if w.Count != 36 || !w.Truncated || w.TargetCount != 1200 {
		t.Fatalf("fabricated or hidden missing history: %+v", w)
	}
	f = &historyFixture{fail: true}
	e.Charts = f
	if _, err := e.chartContext(context.Background(), &input, func(Event) error { return nil }); err == nil {
		t.Fatal("partial request failure silently accepted")
	}
}

func TestHistoryAggregatesCoverEveryOlderBar(t *testing.T) {
	f := &historyFixture{}
	rows, err := f.Candles(context.Background(), market.CandleQuery{Interval: "1h", To: time.Now().Unix(), Limit: 1200})
	if err != nil {
		t.Fatal(err)
	}
	rows[17].High = "999"
	rows[18].Low = "1"
	v := series(rows, window("1h", rows)).(map[string]any)
	if v["range_high"] != "999.000000000000" || v["range_low"] != "1.000000000000" {
		t.Fatal("lost historical extrema")
	}
	seen := 0
	for _, group := range v["historical_aggregates"].([][]any) {
		if group[0] != rows[seen].Time {
			t.Fatal("gap in aggregate coverage")
		}
		seen += group[2].(int)
		if group[1] != rows[seen-1].Time {
			t.Fatal("bad group range")
		}
	}
	for _, row := range v["candles"].([][]any) {
		if row[0] != rows[seen].Time || row[4] != rows[seen].Close {
			t.Fatal("recent sequence skipped a candle")
		}
		seen++
	}
	if seen != len(rows) {
		t.Fatal("history dropped")
	}
}
