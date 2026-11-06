package market

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"time"

	"nofx/planning/core"
)

// Candles are chart observations, never executable quotes or backtest results.
type Candle struct {
	Time   int64        `json:"time"`
	Open   core.Decimal `json:"open"`
	High   core.Decimal `json:"high"`
	Low    core.Decimal `json:"low"`
	Close  core.Decimal `json:"close"`
	Volume core.Decimal `json:"volume"`
}
type CandleQuery struct {
	Venue    string `json:"venue"`
	Symbol   string `json:"symbol"`
	Interval string `json:"interval"`
	From     int64  `json:"from,omitempty"`
	To       int64  `json:"to,omitempty"`
	Limit    int    `json:"limit"`
}
type ChartSource interface {
	Candles(context.Context, CandleQuery) ([]Candle, error)
}

func IntervalSeconds(interval string) int64 {
	return map[string]int64{"1m": 60, "5m": 300, "15m": 900, "1h": 3600, "4h": 14400, "1d": 86400}[interval]
}
func (q CandleQuery) Validate() error {
	if (q.Venue != "binance" && q.Venue != "okx" && q.Venue != "bitget" && q.Venue != "hyperliquid") || !symbolOK(q.Symbol) || IntervalSeconds(q.Interval) == 0 {
		return errors.New("invalid chart venue, symbol or interval")
	}
	if q.Limit < 1 || q.Limit > 300 || q.From < 0 || q.To < 0 || q.To > time.Now().Add(time.Minute).Unix() || (q.From > 0 && q.To > 0 && q.From >= q.To) {
		return errors.New("invalid candle time range or limit (1–300)")
	}
	return nil
}
func NormalizeCandles(rows []Candle, now time.Time) ([]Candle, error) {
	if len(rows) > 1000 {
		return nil, errors.New("too many venue candles")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Time < rows[j].Time })
	for i, row := range rows {
		if row.Time <= 0 || row.Time > now.Add(30*time.Second).Unix() || (i > 0 && rows[i-1].Time == row.Time) {
			return nil, errors.New("invalid or duplicate candle timestamp")
		}
		for _, v := range []core.Decimal{row.Open, row.High, row.Low, row.Close} {
			if !core.Positive(v) {
				return nil, errors.New("invalid candle price")
			}
		}
		o, _ := new(big.Rat).SetString(string(row.Open))
		h, _ := new(big.Rat).SetString(string(row.High))
		l, _ := new(big.Rat).SetString(string(row.Low))
		c, _ := new(big.Rat).SetString(string(row.Close))
		if h.Cmp(l) < 0 || h.Cmp(o) < 0 || h.Cmp(c) < 0 || l.Cmp(o) > 0 || l.Cmp(c) > 0 {
			return nil, errors.New("invalid candle OHLC bounds")
		}
		if len(row.Volume) > 40 || (!core.Positive(row.Volume) && !zeroVolume.MatchString(string(row.Volume))) {
			return nil, errors.New("invalid candle volume")
		}
	}
	return rows, nil
}

var zeroVolume = regexp.MustCompile(`^0+(\.0{1,12})?$`)

func (c *Client) Candles(ctx context.Context, q CandleQuery) ([]Candle, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	var rows []Candle
	switch q.Venue {
	case "binance":
		values := url.Values{"symbol": {q.Symbol}, "interval": {q.Interval}, "limit": {strconv.Itoa(q.Limit)}}
		if q.To > 0 {
			values.Set("endTime", strconv.FormatInt(q.To*1000-1, 10))
		}
		var raw [][]json.RawMessage
		if err := c.request(ctx, "GET", c.BinanceFutures+"/fapi/v1/klines?"+values.Encode(), nil, &raw); err != nil {
			return nil, err
		}
		for _, r := range raw {
			if len(r) < 6 {
				return nil, errors.New("invalid Binance candle")
			}
			var ms int64
			var fields [5]string
			if json.Unmarshal(r[0], &ms) != nil {
				return nil, errors.New("invalid Binance candle time")
			}
			for i := range fields {
				if json.Unmarshal(r[i+1], &fields[i]) != nil {
					return nil, errors.New("invalid Binance candle value")
				}
			}
			rows = append(rows, Candle{ms / 1000, core.Decimal(fields[0]), core.Decimal(fields[1]), core.Decimal(fields[2]), core.Decimal(fields[3]), core.Decimal(fields[4])})
		}
	case "bitget":
		var e error
		rows, e = c.bitgetCandles(ctx, q)
		if e != nil {
			return nil, e
		}
	case "okx":
		bar := map[string]string{"1m": "1m", "5m": "5m", "15m": "15m", "1h": "1H", "4h": "4H", "1d": "1Dutc"}[q.Interval]
		values := url.Values{"instId": {q.Symbol}, "bar": {bar}, "limit": {strconv.Itoa(q.Limit)}}
		path := "/api/v5/market/candles"
		if q.To > 0 && q.To < time.Now().Unix()-IntervalSeconds(q.Interval) {
			path = "/api/v5/market/history-candles"
			values.Set("after", strconv.FormatInt(q.To*1000, 10))
		}
		var raw struct {
			Code string
			Data [][]string
		}
		if err := c.request(ctx, "GET", c.OKX+path+"?"+values.Encode(), nil, &raw); err != nil {
			return nil, err
		}
		if raw.Code != "0" {
			return nil, errors.New("OKX rejected candle request")
		}
		for _, r := range raw.Data {
			if len(r) < 7 {
				return nil, errors.New("invalid OKX candle")
			}
			ms, err := strconv.ParseInt(r[0], 10, 64)
			if err != nil {
				return nil, errors.New("invalid OKX candle time")
			}
			rows = append(rows, Candle{ms / 1000, core.Decimal(r[1]), core.Decimal(r[2]), core.Decimal(r[3]), core.Decimal(r[4]), core.Decimal(r[6])})
		}
	case "hyperliquid":
		end := q.To
		if end == 0 {
			end = time.Now().Unix() + 1
		}
		start := end - int64(q.Limit)*IntervalSeconds(q.Interval)
		var raw []struct {
			Start         int64  `json:"t"`
			End           int64  `json:"T"`
			Coin          string `json:"s"`
			Interval      string `json:"i"`
			O, H, L, C, V string
		}
		body := map[string]any{"type": "candleSnapshot", "req": map[string]any{"coin": q.Symbol, "interval": q.Interval, "startTime": start * 1000, "endTime": end * 1000}}
		if err := c.request(ctx, "POST", c.Hyperliquid+"/info", body, &raw); err != nil {
			return nil, err
		}
		for _, r := range raw {
			if r.Coin != q.Symbol || r.Interval != q.Interval {
				return nil, errors.New("unexpected Hyperliquid candle instrument")
			}
			rows = append(rows, Candle{r.Start / 1000, core.Decimal(r.O), core.Decimal(r.H), core.Decimal(r.L), core.Decimal(r.C), core.Decimal(r.V)})
		}
	}
	rows, err := NormalizeCandles(rows, time.Now())
	if err != nil {
		return nil, err
	}
	var out []Candle
	for _, r := range rows {
		if q.To == 0 || r.Time < q.To {
			out = append(out, r)
		}
	}
	if len(out) > q.Limit {
		out = out[len(out)-q.Limit:]
	}
	// From is advisory: older bars can satisfy TradingView's countBack request.
	return out, nil
}

// DemoCharts is explicitly synthetic and cannot silently replace public data.
