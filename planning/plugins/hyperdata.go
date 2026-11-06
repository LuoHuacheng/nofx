package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"math"
	"math/big"

	"regexp"
	"strconv"
	"strings"
	"time"
)

type UpstreamError struct {
	Code       string
	Message    string
	Reason     string
	RetryAfter int
}

func (e *UpstreamError) Error() string { return "Claw402 " + e.Code + ": " + e.Message }

// Fetch is supplied by NOFX's authenticated Claw402 provider. There is no
// public-origin fallback or wallet credential in the plugin decoder.
type Hyperdata struct {
	Offline bool
	Fetch   func(context.Context, string, Request) ([]byte, error)
}

func (h Hyperdata) Definitions() []Definition {
	defs := []Definition{}
	for _, spec := range []struct{ id, name, description string }{
		{"hyperdata_cost", "Cost / liquidation", "Hyperliquid current position costs and estimated liquidation distribution, not a liquidation forecast."},
		{"hyperdata_winrate", "Win rate / positions", "Historical closed-round win rate and current entry cost heatmap. Unknown samples are separate; not a future win-rate forecast."},
		{"hyperdata_profit", "Profit leaderboard", "Collected cash net PnL over 7/30 days, including fees and funding, excluding unrealized PnL and external hedges. No Claw402 endpoint is currently published."},
	} {
		id := spec.id
		defs = append(defs, Definition{Manifest: Manifest{ID: id, Name: spec.name, Description: spec.description, Version: "1.0.0", Capability: "public.read", Enabled: false}, Read: func(ctx context.Context, q Request) (Result, error) { return h.Read(ctx, id, q) }})
	}
	return defs
}

type marketIdentity struct {
	Chain string `json:"chain"`
	Type  string `json:"marketType"`
	ID    string `json:"marketId"`
}

func sameMarket(m marketIdentity, q Request) bool {
	if m.Chain != "mainnet" {
		return false
	}
	if !strings.Contains(q.Symbol, ":") {
		return m.Type == "core_perp" && (m.ID == q.Symbol || m.ID == "core_perp:"+q.Symbol)
	}
	return m.Type == "hip3_perp" && (m.ID == q.Symbol || regexp.MustCompile(`^hip3_perp:0x[0-9a-fA-F]{40}:`+regexp.QuoteMeta(q.Symbol)+`$`).MatchString(m.ID))
}
func numeric(s string) (*big.Rat, bool) {
	n, ok := new(big.Rat).SetString(s)
	return n, ok && len(s) <= 100 && decimal.MatchString(s)
}
func nonnegative(s string) bool { n, ok := numeric(s); return ok && n.Sign() >= 0 }
func finite(n float64) bool     { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func recent(s string, age time.Duration) bool {
	t, e := time.Parse(time.RFC3339Nano, s)
	return e == nil && time.Since(t) <= age && !t.After(time.Now().Add(10*time.Second))
}
func (h Hyperdata) Read(ctx context.Context, id string, q Request) (Result, error) {
	out := Result{Venue: q.Venue, Symbol: q.Symbol, Mode: "paid", Notice: "Claw402 / Hyperliquid collected statistics, not an exchange order book or evidence of fills."}
	if h.Offline {
		return out, errors.New("Offline mode does not read paid live data")
	}
	if q.Venue != "hyperliquid" {
		return out, errors.New("This component supports matching Hyperliquid markets only; other exchange data are not substituted")
	}
	resource := map[string]string{"hyperdata_cost": "riskbins", "hyperdata_winrate": "winrate-map", "hyperdata_profit": "profit-leaderboard"}[id]
	if resource == "" {
		return out, errors.New("unknown Hyperdata component")
	}
	window := q.Window
	if window == "" {
		window = "7d"
	}
	if window != "7d" && window != "30d" {
		return out, errors.New("invalid profit window")
	}
	if h.Fetch == nil {
		return out, errors.New("Claw402 paid data provider is not configured")
	}
	payload, err := h.Fetch(ctx, id, q)
	if err != nil {
		return out, err
	}
	if len(payload) > 2<<20 {
		return out, errors.New("Claw402 response exceeds bounded size")
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
		Meta struct {
			Coverage string `json:"coverage"`
			State    string `json:"state"`
			AsOf     string `json:"asOfTime"`
		} `json:"meta"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return out, errors.New("Hyperdata invalid response")
	}
	if envelope.Error != nil {
		retry := 0 // Paid failures must never schedule automatic retries.
		reason := envelope.Error.Details.Reason
		switch reason {
		case "map_building", "map_busy", "map_timeout":
		default:
			reason = "unavailable"
		}
		return out, &UpstreamError{Code: cutPublic(envelope.Error.Code, 80), Message: cutPublic(envelope.Error.Message, 200), Reason: reason, RetryAfter: retry}
	}
	if len(envelope.Data) == 0 {
		return out, fmt.Errorf("Claw402 response has no data")
	}
	visual := &Visual{Kind: resource, State: envelope.Meta.State, Coverage: envelope.Meta.Coverage, DataAt: envelope.Meta.AsOf}
	out.Source = "https://claw402.ai · " + resource
	out.Observed = time.Now().UTC()
	out.Visual = visual
	switch id {
	case "hyperdata_cost":
		var data struct {
			Market    marketIdentity `json:"market"`
			Mark      float64        `json:"markPrice"`
			CostAddrs int            `json:"costAddrs"`
			LiqAddrs  int            `json:"liqAddrs"`
			Bins      []CostBin      `json:"bins"`
			Cost      struct {
				State string `json:"state"`
			} `json:"cost"`
			Liquidation struct {
				State string `json:"state"`
			} `json:"liquidation"`
		}
		if json.Unmarshal(envelope.Data, &data) != nil || !sameMarket(data.Market, q) || !finite(data.Mark) || data.Mark <= 0 || len(data.Bins) > 512 || data.CostAddrs < 0 || data.LiqAddrs < 0 || !recent(visual.DataAt, 15*time.Minute) {
			return out, errors.New("Invalid cost/liquidation identity, values or timestamp")
		}
		if data.Cost.State == "unavailable" || data.Liquidation.State == "unavailable" {
			return out, errors.New("Cost or liquidation data is unavailable; missing data is not drawn")
		}
		visual.Mark = data.Mark
		visual.Bins = data.Bins
		for i, b := range data.Bins {
			if !finite(b.Px) || b.Px <= 0 || i > 0 && b.Px <= data.Bins[i-1].Px {
				return out, errors.New("invalid cost price bins")
			}
			for _, v := range []float64{b.LongCost, b.ShortCost, b.LongLiq, b.ShortLiq} {
				if !finite(v) || v < 0 {
					return out, errors.New("invalid cost amount")
				}
			}
		}
		out.Metrics = []Metric{{Key: "cost_addresses", Label: "Cost coverage addresses", Value: strconv.Itoa(data.CostAddrs), Unit: "addresses"}, {Key: "liq_addresses", Label: "Liquidation coverage addresses", Value: strconv.Itoa(data.LiqAddrs), Unit: "addresses"}, {Key: "mark_price", Label: "Reference price", Value: strconv.FormatFloat(data.Mark, 'f', -1, 64), Unit: "USDC"}}
		// Give the model bounded concentration locations, not hundreds of chart bars.
		for _, series := range []struct {
			key, label string
			amount     func(CostBin) float64
		}{
			{"long_cost_peak", "Peak long-cost price bin", func(b CostBin) float64 { return b.LongCost }},
			{"short_cost_peak", "Peak short-cost price bin", func(b CostBin) float64 { return b.ShortCost }},
			{"long_liq_peak", "Peak long-liquidation price bin", func(b CostBin) float64 { return b.LongLiq }},
			{"short_liq_peak", "Peak short-liquidation price bin", func(b CostBin) float64 { return b.ShortLiq }},
		} {
			var peak CostBin
			for _, bin := range data.Bins {
				if series.amount(bin) > series.amount(peak) {
					peak = bin
				}
			}
			if series.amount(peak) > 0 {
				out.Metrics = append(out.Metrics, Metric{Key: series.key, Label: series.label, Value: strconv.FormatFloat(peak.Px, 'f', -1, 64), Unit: "USDC"})
			}
		}
		out.Notice += " Cost state " + data.Cost.State + "; liquidation state " + data.Liquidation.State + ". Addresses may not represent distinct owners; liquidation prices are estimates for existing positions."
	case "hyperdata_winrate":
		var data struct {
			AsOf        string                                             `json:"asOf"`
			PositionsAt string                                             `json:"positionsAsOf"`
			Coverage    string                                             `json:"coverage"`
			Mark        string                                             `json:"markPrice"`
			Included    HeatTotal                                          `json:"included"`
			Total       HeatTotal                                          `json:"total"`
			Cells       []HeatCell                                         `json:"cells"`
			Rows        int                                                `json:"winBins"`
			Cols        int                                                `json:"costBins"`
			MinRounds   int                                                `json:"minRoundTrips"`
			Viewport    struct{ WinMin, WinMax, CostMin, CostMax float64 } `json:"viewport"`
		}
		if json.Unmarshal(envelope.Data, &data) != nil || !recent(data.PositionsAt, 15*time.Minute) || data.Rows < 1 || data.Rows > 40 || data.Cols < 1 || data.Cols > 40 || len(data.Cells) > 1600 || data.Included.Count < 0 || data.Total.Count < data.Included.Count || !nonnegative(data.Included.Notional) {
			return out, errors.New("Invalid holder win-rate data, values or timestamp")
		}
		// Identity is established by the requested immutable endpoint; maps carry no market field.
		mark, err := strconv.ParseFloat(data.Mark, 64)
		if err != nil || !finite(mark) || mark <= 0 {
			return out, errors.New("Holder win-rate map has no valid reference price")
		}
		visual.Mark = mark
		visual.Rows = data.Rows
		visual.Columns = data.Cols + 2
		visual.Cells = data.Cells
		visual.DataAt = data.PositionsAt
		visual.Coverage = data.Coverage
		visual.State = "observed"
		visual.WinMin = data.Viewport.WinMin
		visual.WinMax = data.Viewport.WinMax
		visual.CostMin = data.Viewport.CostMin
		visual.CostMax = data.Viewport.CostMax
		if !finite(visual.WinMin) || !finite(visual.WinMax) || visual.WinMin < 0 || visual.WinMax > 100 || visual.WinMin >= visual.WinMax || !finite(visual.CostMin) || !finite(visual.CostMax) || visual.CostMin >= visual.CostMax {
			return out, errors.New("invalid heatmap viewport")
		}
		seen := map[string]bool{}
		for _, c := range data.Cells {
			k := fmt.Sprint(c.Row, ":", c.Column)
			if c.Row < 0 || c.Row >= data.Rows || c.Column < 0 || c.Column >= data.Cols+2 || seen[k] || c.Long.Count < 0 || c.Short.Count < 0 || !nonnegative(c.Long.Notional) || !nonnegative(c.Short.Notional) {
				return out, errors.New("invalid heatmap cell")
			}
			seen[k] = true
		}
		out.Metrics = []Metric{{Key: "included", Label: "Holder addresses with usable history", Value: strconv.Itoa(data.Included.Count), Unit: "addresses"}, {Key: "total", Label: "Current holder addresses", Value: strconv.Itoa(data.Total.Count), Unit: "addresses"}, {Key: "min_rounds", Label: "Minimum completed rounds", Value: strconv.Itoa(data.MinRounds), Unit: "rounds"}}
		totals := []*big.Rat{new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)}
		for _, cell := range data.Cells {
			long, _ := numeric(cell.Long.Notional)
			short, _ := numeric(cell.Short.Notional)
			totals[0].Add(totals[0], long)
			totals[1].Add(totals[1], short)
			lower := visual.WinMin + float64(cell.Row)*(visual.WinMax-visual.WinMin)/float64(data.Rows)
			if lower >= 60 {
				totals[2].Add(totals[2], long)
				totals[3].Add(totals[3], short)
			}
		}
		for n, item := range []struct{ key, label string }{{"heat_long", "Long notional in heatmap cells"}, {"heat_short", "Short notional in heatmap cells"}, {"win60_long", "Long notional in win-rate bins starting at 60% or higher"}, {"win60_short", "Short notional in win-rate bins starting at 60% or higher"}} {
			out.Metrics = append(out.Metrics, Metric{Key: item.key, Label: item.label, Value: totals[n].FloatString(12), Unit: "USDC"})
		}
		out.Notice += " Historical win rate excludes funding and unknown history; it is not a forecast. Heatmap amounts are current positions, not directional probabilities."
	case "hyperdata_profit":
		var data struct {
			Market    marketIdentity `json:"market"`
			Days      int            `json:"windowDays"`
			Limit     int            `json:"limit"`
			Minimum   string         `json:"minNetPnl"`
			State     string         `json:"state"`
			Start     string         `json:"windowStart"`
			AsOf      string         `json:"asOf"`
			Covered   int            `json:"coveredHours"`
			Expected  int            `json:"expectedHours"`
			Qualified int            `json:"qualifiedAddresses"`
			Items     []ProfitEntry  `json:"items"`
		}
		days := 7
		if window == "30d" {
			days = 30
		}
		if json.Unmarshal(envelope.Data, &data) != nil || !sameMarket(data.Market, q) || data.Days != days || data.Limit != 100 || data.Minimum != "10000" || len(data.Items) > 100 || data.Covered < 0 || data.Covered > days*24 || data.Expected != days*24 || data.Qualified < 0 || !recent(data.AsOf, 3*time.Hour) {
			return out, errors.New("Invalid profit-board identity, window or coverage")
		}
		start, e := time.Parse(time.RFC3339, data.Start)
		end, e2 := time.Parse(time.RFC3339, data.AsOf)
		if e != nil || e2 != nil || end.Sub(start) != time.Duration(days)*24*time.Hour {
			return out, errors.New("invalid profit time window")
		}
		visual.State = data.State
		visual.DataAt = data.AsOf
		visual.WindowStart = data.Start
		switch data.State {
		case "ready":
			if data.Covered != data.Expected || len(data.Items) != min(data.Qualified, 100) {
				return out, errors.New("incomplete ready profit board")
			}
		case "building", "stale", "invalid_data":
			if len(data.Items) != 0 {
				return out, errors.New("non-ready profit board contains rankings")
			}
		default:
			return out, errors.New("unknown profit state")
		}
		seen := map[string]bool{}
		var previous *big.Rat
		previousAddress := ""
		for i, r := range data.Items {
			if r.Rank != i+1 || !regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`).MatchString(r.Address) || seen[strings.ToLower(r.Address)] {
				return out, errors.New("invalid profit identity or rank")
			}
			seen[strings.ToLower(r.Address)] = true
			net, ok := numeric(r.Net)
			gross, gok := numeric(r.Realized)
			fee, fok := numeric(r.Fees)
			fund, uok := numeric(r.Funding)
			if !ok || !gok || !fok || !uok || !nonnegative(r.Volume) || net.Cmp(big.NewRat(10000, 1)) < 0 || new(big.Rat).Add(new(big.Rat).Sub(gross, fee), fund).Cmp(net) != 0 {
				return out, errors.New("profit financial amounts do not reconcile")
			}
			if previous != nil && (net.Cmp(previous) > 0 || net.Cmp(previous) == 0 && r.Address < previousAddress) {
				return out, errors.New("profit ranks out of order")
			}
			previous = net
			previousAddress = r.Address
		}
		visual.Entries = data.Items
		out.Metrics = []Metric{{Key: "covered_hours", Label: "Covered hours", Value: strconv.Itoa(data.Covered), Unit: "hours"}, {Key: "expected_hours", Label: "Window hours", Value: strconv.Itoa(data.Expected), Unit: "hours"}}
		if data.State == "ready" {
			out.Metrics = append(out.Metrics, Metric{Key: "qualified", Label: "Addresses with net profit of at least 10,000", Value: strconv.Itoa(data.Qualified), Unit: "addresses"})
		}
		if data.State == "ready" && len(data.Items) > 0 {
			out.Metrics = append(out.Metrics, Metric{Key: "leader_net", Label: "Leader collected cash net PnL", Value: data.Items[0].Net, Unit: "USDC"})
		}
		out.Notice += " State " + data.State + "; last " + strconv.Itoa(days) + " day net PnL = realized PnL - fees + funding; collected records only, excluding unrealized PnL, external hedges and return rates."
	}
	out.Notice += " Data as of " + visual.DataAt + "；Coverage " + visual.Coverage + "。"
	return out, nil
}
func cutPublic(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
