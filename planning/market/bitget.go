package market

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"nofx/planning/core"
	"nofx/planning/plugins"
	"strconv"
	"strings"
	"time"
)

type bitgetContract struct{ Symbol, BaseCoin, QuoteCoin, SymbolStatus, SymbolType, SizeMultiplier, MinTradeNum, MinTradeUSDT, PricePlace, PriceEndStep string }

func bitgetQuery(symbol string) string {
	return "?productType=USDT-FUTURES&symbol=" + url.QueryEscape(symbol)
}
func (c *Client) bitgetPerpetual(ctx context.Context, symbol string) (core.Snapshot, error) {
	s := core.Snapshot{Venue: "bitget", Symbol: symbol, Kind: "perpetual"}
	var info struct {
		Code string
		Data []bitgetContract
	}
	if e := c.request(ctx, "GET", c.Bitget+"/api/v2/mix/market/contracts"+bitgetQuery(symbol), nil, &info); e != nil {
		return s, e
	}
	if info.Code != "00000" || len(info.Data) != 1 {
		return s, errors.New("invalid Bitget instrument metadata")
	}
	r := info.Data[0]
	if r.Symbol != symbol || r.SymbolStatus != "normal" || r.SymbolType != "perpetual" || r.QuoteCoin != "USDT" {
		return s, errors.New("Only active Bitget USDT perpetuals are supported")
	}
	p, e := strconv.Atoi(r.PricePlace)
	if e != nil || p < 0 || p > 12 {
		return s, errors.New("invalid Bitget tick precision")
	}
	unit := "1"
	if p > 0 {
		unit = "0." + strings.Repeat("0", p-1) + "1"
	}
	s.Tick, e = productDecimal(r.PriceEndStep, unit)
	if e != nil {
		return s, e
	}
	s.Step = core.Decimal(r.SizeMultiplier)
	s.MinQty = core.Decimal(r.MinTradeNum)
	s.MinNotional = core.Decimal(r.MinTradeUSDT)
	s.BaseAsset = r.BaseCoin
	s.QuoteAsset = "USDT"
	var b struct {
		Code string
		Data struct {
			Bids, Asks [][]json.Number
			Ts         string
		}
	}
	if e = c.request(ctx, "GET", c.Bitget+"/api/v2/mix/market/merge-depth"+bitgetQuery(symbol)+"&precision=scale0&limit=50", nil, &b); e != nil {
		return s, e
	}
	if b.Code != "00000" || len(b.Data.Bids) == 0 || len(b.Data.Asks) == 0 {
		return s, errors.New("empty Bitget order book")
	}
	for _, side := range []struct {
		raw [][]json.Number
		dst *[]core.BookLevel
	}{{b.Data.Bids, &s.Bids}, {b.Data.Asks, &s.Asks}} {
		for _, r := range side.raw {
			if len(r) < 2 {
				return s, errors.New("invalid Bitget book level")
			}
			*side.dst = append(*side.dst, core.BookLevel{Price: core.Decimal(r[0]), Quantity: core.Decimal(r[1])})
		}
	}
	s.Bid = s.Bids[0].Price
	s.BidSize = s.Bids[0].Quantity
	s.Ask = s.Asks[0].Price
	s.AskSize = s.Asks[0].Quantity
	ms, e := strconv.ParseInt(b.Data.Ts, 10, 64)
	if e != nil || ms <= 0 {
		return s, errors.New("invalid Bitget book timestamp")
	}
	s.SourceTime = time.UnixMilli(ms).UTC()
	s.Observed = time.Now().UTC()
	return s, s.Validate(time.Now().UTC())
}
func (c *Client) bitgetCandles(ctx context.Context, q CandleQuery) ([]Candle, error) {
	bar := map[string]string{"1m": "1m", "5m": "5m", "15m": "15m", "1h": "1H", "4h": "4H", "1d": "1Dutc"}[q.Interval]
	limit := q.Limit
	if limit > 200 {
		limit = 200
	}
	values := url.Values{"symbol": {q.Symbol}, "productType": {"USDT-FUTURES"}, "granularity": {bar}, "limit": {strconv.Itoa(limit)}}
	path := "/api/v2/mix/market/candles"
	if q.To > 0 && q.To < time.Now().Unix()-IntervalSeconds(q.Interval) {
		path = "/api/v2/mix/market/history-candles"
		values.Set("endTime", strconv.FormatInt(q.To*1000-1, 10))
	}
	var data struct {
		Code string
		Data [][]string
	}
	if e := c.request(ctx, "GET", c.Bitget+path+"?"+values.Encode(), nil, &data); e != nil {
		return nil, e
	}
	if data.Code != "00000" {
		return nil, errors.New("Bitget rejected candle request")
	}
	rows := []Candle{}
	for _, r := range data.Data {
		if len(r) < 6 {
			return nil, errors.New("invalid Bitget candle")
		}
		ms, e := strconv.ParseInt(r[0], 10, 64)
		if e != nil {
			return nil, e
		}
		rows = append(rows, Candle{ms / 1000, core.Decimal(r[1]), core.Decimal(r[2]), core.Decimal(r[3]), core.Decimal(r[4]), core.Decimal(r[5])})
	}
	return rows, nil
}
func (c *Client) bitgetMarkets(ctx context.Context, out *MarketList) error {
	var info struct {
		Code string
		Data []bitgetContract
	}
	var tickers struct {
		Code string
		Data []struct{ Symbol, LastPr, UsdtVolume string }
	}
	p1 := "/api/v2/mix/market/contracts?productType=USDT-FUTURES"
	p2 := "/api/v2/mix/market/tickers?productType=USDT-FUTURES"
	if e := c.request(ctx, "GET", c.Bitget+p1, nil, &info); e != nil {
		return e
	}
	if e := c.request(ctx, "GET", c.Bitget+p2, nil, &tickers); e != nil {
		return e
	}
	if info.Code != "00000" || tickers.Code != "00000" {
		return errors.New("Bitget market discovery unavailable")
	}
	active := map[string]string{}
	for _, r := range info.Data {
		if r.SymbolStatus == "normal" && r.SymbolType == "perpetual" && r.QuoteCoin == "USDT" && symbolOK(r.Symbol) {
			active[r.Symbol] = r.BaseCoin
		}
	}
	for _, r := range tickers.Data {
		base, ok := active[r.Symbol]
		v, valid := validVolume(r.UsdtVolume)
		if !ok || !valid {
			continue
		}
		out.Markets = append(out.Markets, MarketSymbol{Symbol: r.Symbol, Base: base, Category: "crypto", Volume24H: v, Price: r.LastPr, ExecutionSupported: true})
	}
	out.Sources = []string{c.Bitget + p1, c.Bitget + p2}
	return nil
}
func (c *Client) okxMarkets(ctx context.Context, out *MarketList) error {
	var info struct {
		Code string
		Data []struct{ InstId, InstType, CtType, SettleCcy, State, CtValCcy, CtMult string }
	}
	var tickers struct {
		Code string
		Data []struct{ InstId, Last, VolCcy24h string }
	}
	p1 := "/api/v5/public/instruments?instType=SWAP"
	p2 := "/api/v5/market/tickers?instType=SWAP"
	if e := c.request(ctx, "GET", c.OKX+p1, nil, &info); e != nil {
		return e
	}
	if e := c.request(ctx, "GET", c.OKX+p2, nil, &tickers); e != nil {
		return e
	}
	if info.Code != "0" || tickers.Code != "0" {
		return errors.New("OKX market discovery unavailable")
	}
	active := map[string]string{}
	for _, r := range info.Data {
		if r.InstType == "SWAP" && r.CtType == "linear" && r.SettleCcy == "USDT" && r.State == "live" && strings.HasSuffix(r.InstId, "-USDT-SWAP") && (r.CtMult == "" || r.CtMult == "1" || r.CtMult == "1.0") && symbolOK(r.InstId) {
			active[r.InstId] = r.CtValCcy
		}
	}
	for _, r := range tickers.Data {
		base, ok := active[r.InstId]
		v, valid := validVolume(r.VolCcy24h)
		price, pok := validVolume(r.Last)
		if !ok || !valid || !pok {
			continue
		}
		out.Markets = append(out.Markets, MarketSymbol{Symbol: r.InstId, Base: base, Category: "crypto", Volume24H: v * price, Price: r.Last, ExecutionSupported: true})
	}
	out.Sources = []string{c.OKX + p1, c.OKX + p2}
	out.Warnings = []string{"OKX 24h notional volume is estimated from base volume and the latest price."}
	return nil
}
func (c *Client) bitgetPlugin(ctx context.Context, id string, q plugins.Request, out plugins.Result) (plugins.Result, error) {
	if !strings.HasSuffix(q.Symbol, "USDT") {
		return out, errors.New("Bitget USDT futures symbol required")
	}
	if id == "funding" {
		path := "/api/v2/mix/market/current-fund-rate"
		var data struct {
			Code string
			Data []struct{ Symbol, FundingRate, FundingRateInterval string }
		}
		if e := c.request(ctx, "GET", c.Bitget+path+bitgetQuery(q.Symbol), nil, &data); e != nil {
			return out, e
		}
		if data.Code != "00000" || len(data.Data) != 1 || data.Data[0].Symbol != q.Symbol {
			return out, errors.New("invalid Bitget funding response")
		}
		out.Source = "https://api.bitget.com" + path
		out.Metrics = []plugins.Metric{{Key: "funding_rate", Label: "Current funding rate", Value: data.Data[0].FundingRate, Unit: "fraction"}}
		out.Notice += " Timestamp is receipt time; funding interval is " + data.Data[0].FundingRateInterval + " hours."
	} else {
		path := "/api/v2/mix/market/open-interest"
		var data struct {
			Code string
			Data struct {
				OpenInterestList []struct{ Symbol, Size string }
				Ts               string
			}
		}
		if e := c.request(ctx, "GET", c.Bitget+path+bitgetQuery(q.Symbol), nil, &data); e != nil {
			return out, e
		}
		if data.Code != "00000" || len(data.Data.OpenInterestList) != 1 || data.Data.OpenInterestList[0].Symbol != q.Symbol {
			return out, errors.New("invalid Bitget OI response")
		}
		ms, e := strconv.ParseInt(data.Data.Ts, 10, 64)
		if e != nil || ms <= 0 {
			return out, errors.New("invalid Bitget OI timestamp")
		}
		out.SourceTime = time.UnixMilli(ms).UTC()
		out.Source = "https://api.bitget.com" + path
		out.Metrics = []plugins.Metric{{Key: "open_interest", Label: "Open interest", Value: data.Data.OpenInterestList[0].Size, Unit: strings.TrimSuffix(q.Symbol, "USDT")}}
	}
	out.Observed = time.Now().UTC()
	return out, nil
}
