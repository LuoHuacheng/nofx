package harness

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"nofx/planning/core"
	"nofx/planning/market"
)

func macroIntervals(interval string) []string {
	switch interval {
	case "1m", "5m", "15m":
		return []string{"1h", "4h"}
	case "1h":
		return []string{"4h", "1d"}
	case "4h":
		return []string{"1h", "1d"}
	default:
		return []string{"1h", "4h"}
	}
}

// Planning always reads fresh history independently of chart zoom and scroll.
// The viewport is retained only as drawing context, never as a data limit.
const primaryHistoryBars = 1200
const comparisonHistoryBars = 500

func (e *Engine) history(ctx context.Context, input *Input, interval string, count int, to int64, id string, emit Emit) ([]market.Candle, error) {
	q := market.CandleQuery{Venue: input.Venue, Symbol: input.Symbol, Interval: interval, To: to}
	rows := []market.Candle{}
	// Bound requests even if an exchange returns unusually small pages.
	for pageNo := 0; len(rows) < count && pageNo < 16; pageNo++ {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		q.Limit = min(300, count-len(rows))
		page, err := e.Charts.Candles(ctx, q)
		if err != nil {
			return rows, err
		}
		// Preserve strict chronological order across inclusive venue cursors.
		older := make([]market.Candle, 0, len(page))
		for _, bar := range page {
			if bar.Time < q.To {
				older = append(older, bar)
			}
		}
		if len(older) == 0 {
			break
		}
		if len(older) > q.Limit {
			older = older[len(older)-q.Limit:]
		}
		q.To = older[0].Time
		rows = append(older, rows...)
		if err := emit(Event{"tool_progress", map[string]any{"name": "get_candles", "id": id, "interval": interval, "count": len(rows), "target_count": count, "scope": "Latest history · independent of chart view"}}); err != nil {
			return rows, err
		}
	}
	return rows, nil
}

func (e *Engine) chartContext(ctx context.Context, input *Input, emit Emit) (map[string]any, error) {
	to := time.Now().Unix() + 1
	input.Analysis = &core.Analysis{Interval: input.Interval, VisibleFrom: input.VisibleFrom, VisibleTo: input.VisibleTo, HistoryPolicy: "latest_history", Windows: []core.DataWindow{}}
	data := map[string]any{
		"active_interval":         input.Interval,
		"visible_range_requested": map[string]int64{"from": input.VisibleFrom, "to": input.VisibleTo},
		"history_policy":          "Latest history independent of chart viewport; primary 1200 bars, comparison timeframes 500 bars each. Fewer bars means limited exchange history, never synthetic candles.",
		"received":                time.Now().UTC(),
	}
	intervals := append([]string{input.Interval}, macroIntervals(input.Interval)...)
	macro := []any{}
	for index, interval := range intervals {
		id, target := "context", primaryHistoryBars
		if index > 0 {
			id, target = "macro_"+interval, comparisonHistoryBars
		}
		if err := emit(Event{"tool_start", map[string]any{"name": "get_candles", "id": id}}); err != nil {
			return nil, err
		}
		bars, err := e.history(ctx, input, interval, target, to, id, emit)
		w := window(interval, bars)
		w.TargetCount, w.Truncated = target, len(bars) < target
		result := map[string]any{"name": "get_candles", "id": id, "ok": err == nil && len(bars) > 0, "count": len(bars), "target_count": target, "interval": interval, "scope": "Latest history · independent of chart view", "from": w.From, "to": w.To, "truncated": w.Truncated}
		if err != nil {
			result["reason"] = err.Error()
		} else if len(bars) == 0 {
			err = fmt.Errorf("no %s candles available for %s", interval, input.Symbol)
			result["reason"] = err.Error()
		}
		if emitErr := emit(Event{"tool_result", result}); emitErr != nil {
			return nil, emitErr
		}
		if err != nil {
			return nil, fmt.Errorf("could not load %s planning history: %w", interval, err)
		}
		input.Analysis.Windows = append(input.Analysis.Windows, w)
		if index == 0 {
			data["primary"] = series(bars, w)
			data["effective_data_range"] = map[string]int64{"from": w.From, "to": w.To}
		} else {
			macro = append(macro, series(bars, w))
		}
	}
	data["comparison_timeframes"] = macro
	if err := emit(Event{"context", input.Analysis}); err != nil {
		return nil, err
	}
	return data, nil
}
func window(interval string, bars []market.Candle) core.DataWindow {
	w := core.DataWindow{Interval: interval, Count: len(bars)}
	if len(bars) > 0 {
		w.From = bars[0].Time
		w.To = bars[len(bars)-1].Time
		w.Sampled = len(bars) > 240
	}
	return w
}
func series(rows []market.Candle, w core.DataWindow) any {
	if len(rows) == 0 {
		return map[string]any{"window": w, "candles": []market.Candle{}}
	}
	high, low := new(big.Rat), new(big.Rat)
	high.SetString(string(rows[0].High))
	low.SetString(string(rows[0].Low))
	for _, row := range rows {
		h, _ := new(big.Rat).SetString(string(row.High))
		l, _ := new(big.Rat).SetString(string(row.Low))
		if h.Cmp(high) > 0 {
			high = h
		}
		if l.Cmp(low) < 0 {
			low = l
		}
	}
	// Keep the latest raw candles contiguous. Older OHLCV groups cover every
	// fetched bar, preserving extremes and volume instead of dropping samples.
	cut := max(0, len(rows)-240)
	older := [][]any{}
	if cut > 0 {
		groupSize := (cut + 95) / 96
		for start := 0; start < cut; start += groupSize {
			group := rows[start:min(start+groupSize, cut)]
			h, l := new(big.Rat), new(big.Rat)
			h.SetString(string(group[0].High))
			l.SetString(string(group[0].Low))
			volume := new(big.Rat)
			for _, bar := range group {
				bh, _ := new(big.Rat).SetString(string(bar.High))
				bl, _ := new(big.Rat).SetString(string(bar.Low))
				bv, _ := new(big.Rat).SetString(string(bar.Volume))
				if bh.Cmp(h) > 0 {
					h = bh
				}
				if bl.Cmp(l) < 0 {
					l = bl
				}
				volume.Add(volume, bv)
			}
			older = append(older, []any{group[0].Time, group[len(group)-1].Time, len(group), group[0].Open, compactDecimal(h), compactDecimal(l), group[len(group)-1].Close, compactDecimal(volume)})
		}
	}
	raw := make([][]any, 0, len(rows)-cut)
	for _, row := range rows[cut:] {
		raw = append(raw, []any{row.Time, row.Open, row.High, row.Low, row.Close, row.Volume})
	}
	return map[string]any{"window": w, "candle_columns": []string{"time", "open", "high", "low", "close", "volume"}, "candles": raw, "aggregate_columns": []string{"from", "to", "count", "open", "high", "low", "close", "volume"}, "historical_aggregates": older, "raw_recent_count": len(rows) - cut, "sample_is_noncontiguous": false, "range_high": high.FloatString(12), "range_low": low.FloatString(12), "first_close": rows[0].Close, "last_close": rows[len(rows)-1].Close, "notice": "Latest raw candles preserve the original sequence. Older aggregates cover every remaining fetched bar with OHLCV and timestamps; do not treat aggregates as single candles at the active interval. Request raw historical slices for indicators. Limited history is disclosed in the window."}
}

// Compact decimal text preserves the exact value while reducing model input.
func compactDecimal(r *big.Rat) string {
	return strings.TrimRight(strings.TrimRight(r.FloatString(12), "0"), ".")
}
