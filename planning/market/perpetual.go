package market

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"nofx/planning/core"
)

// SnapshotKind is used only to inspect/close preserved legacy spot records.
// All new workspace observations default to perpetual contracts.
func (c *Client) SnapshotKind(ctx context.Context, venue, symbol, kind string) (core.Snapshot, error) {
	if !symbolOK(symbol) {
		return core.Snapshot{}, errors.New("invalid native symbol")
	}
	if kind == "perpetual" {
		switch venue {
		case "binance":
			return c.binancePerpetual(ctx, symbol)
		case "bitget":
			return c.bitgetPerpetual(ctx, symbol)
		case "okx":
			return c.okxPerpetual(ctx, symbol)
		case "hyperliquid":
			return c.spotOrHyperliquid(ctx, venue, symbol)
		}
	}
	if kind == "spot" && (venue == "binance" || venue == "okx") {
		return c.spotOrHyperliquid(ctx, venue, symbol)
	}
	return core.Snapshot{}, errors.New("unsupported market kind")
}
func (c *Client) binancePerpetual(ctx context.Context, symbol string) (core.Snapshot, error) {
	s := core.Snapshot{Venue: "binance", Symbol: symbol, Kind: "perpetual"}
	var info struct {
		Symbols []struct {
			Symbol, Status, ContractType, MarginAsset, QuoteAsset, BaseAsset string
			Filters                                                          []struct{ FilterType, TickSize, StepSize, MinQty, MinNotional, Notional string }
		}
	}
	if err := c.request(ctx, "GET", c.BinanceFutures+"/fapi/v1/exchangeInfo", nil, &info); err != nil {
		return s, err
	}
	found := false
	for _, r := range info.Symbols {
		if r.Symbol != symbol {
			continue
		}
		if r.Status != "TRADING" || (r.ContractType != "PERPETUAL" && r.ContractType != "TRADIFI_PERPETUAL") || r.MarginAsset != "USDT" || r.QuoteAsset != "USDT" {
			return s, errors.New("Only actively trading Binance USDT perpetual contracts are supported")
		}
		found = true
		s.BaseAsset = r.BaseAsset
		s.QuoteAsset = r.QuoteAsset
		for _, f := range r.Filters {
			switch f.FilterType {
			case "PRICE_FILTER":
				s.Tick = core.Decimal(f.TickSize)
			case "LOT_SIZE":
				s.Step = core.Decimal(f.StepSize)
				s.MinQty = core.Decimal(f.MinQty)
			case "MIN_NOTIONAL", "NOTIONAL":
				v := f.Notional
				if v == "" {
					v = f.MinNotional
				}
				s.MinNotional = core.Decimal(v)
			}
		}
	}
	if !found {
		return s, errors.New("Symbol is not listed as a Binance USDT perpetual; no spot fallback was used")
	}
	var b struct {
		Bids, Asks [][]string
		T, E       int64
	}
	if err := c.request(ctx, "GET", c.BinanceFutures+"/fapi/v1/depth?symbol="+url.QueryEscape(symbol)+"&limit=100", nil, &b); err != nil {
		return s, err
	}
	if len(b.Bids) == 0 || len(b.Asks) == 0 || len(b.Bids[0]) < 2 || len(b.Asks[0]) < 2 {
		return s, errors.New("empty Binance perpetual book")
	}
	s.Bid = core.Decimal(b.Bids[0][0])
	s.BidSize = core.Decimal(b.Bids[0][1])
	s.Ask = core.Decimal(b.Asks[0][0])
	s.AskSize = core.Decimal(b.Asks[0][1])
	for _, side := range []struct {
		raw [][]string
		dst *[]core.BookLevel
	}{{b.Bids, &s.Bids}, {b.Asks, &s.Asks}} {
		for _, level := range side.raw {
			if len(level) < 2 {
				return s, errors.New("invalid Binance perpetual book level")
			}
			*side.dst = append(*side.dst, core.BookLevel{Price: core.Decimal(level[0]), Quantity: core.Decimal(level[1])})
		}
	}
	if b.T == 0 {
		b.T = b.E
	}
	if b.T <= 0 {
		return s, errors.New("missing Binance futures source timestamp")
	}
	s.SourceTime = time.UnixMilli(b.T).UTC()
	s.Observed = time.Now().UTC()
	return s, s.Validate(time.Now().UTC())
}

// productDecimal rejects silently rounded contract multipliers/quantity steps.
func productDecimal(a, b string) (core.Decimal, error) {
	x, ok := new(big.Rat).SetString(a)
	if !ok || x.Sign() <= 0 {
		return "", errors.New("invalid contract quantity")
	}
	y, ok := new(big.Rat).SetString(b)
	if !ok || y.Sign() <= 0 {
		return "", errors.New("invalid contract value")
	}
	z := new(big.Rat).Mul(x, y)
	v := core.Decimal(z.FloatString(12))
	r, _ := new(big.Rat).SetString(string(v))
	if !core.Positive(v) || r.Cmp(z) != 0 {
		return "", errors.New("unsupported contract quantity precision")
	}
	return v, nil
}
func (c *Client) okxPerpetual(ctx context.Context, symbol string) (core.Snapshot, error) {
	s := core.Snapshot{Venue: "okx", Symbol: symbol, Kind: "perpetual"}
	if !strings.HasSuffix(symbol, "-USDT-SWAP") {
		return s, errors.New("Use an OKX USDT linear perpetual symbol such as BTC-USDT-SWAP")
	}
	var info struct {
		Code string
		Data []struct{ InstId, State, InstType, CtType, CtVal, CtValCcy, CtMult, SettleCcy, TickSz, LotSz, MinSz string }
	}
	if err := c.request(ctx, "GET", c.OKX+"/api/v5/public/instruments?instType=SWAP&instId="+url.QueryEscape(symbol), nil, &info); err != nil {
		return s, err
	}
	if info.Code != "0" || len(info.Data) != 1 {
		return s, errors.New("invalid OKX perpetual instrument metadata")
	}
	r := info.Data[0]
	if r.InstId != symbol || r.State != "live" || r.InstType != "SWAP" || r.CtType != "linear" || r.SettleCcy != "USDT" || r.CtValCcy != strings.TrimSuffix(symbol, "-USDT-SWAP") {
		return s, errors.New("unsupported OKX contract; inverse/non-USDT/inactive contracts rejected")
	}
	// ctVal defines base units per contract. ctMult is not silently ignored.
	if r.CtMult != "" && r.CtMult != "1" && r.CtMult != "1.0" {
		return s, errors.New("non-unit OKX ctMult unsupported")
	}
	var err error
	s.ContractValue = core.Decimal(r.CtVal)
	s.BaseAsset = r.CtValCcy
	s.QuoteAsset = r.SettleCcy
	s.Tick = core.Decimal(r.TickSz)
	if s.Step, err = productDecimal(r.LotSz, r.CtVal); err != nil {
		return s, err
	}
	if s.MinQty, err = productDecimal(r.MinSz, r.CtVal); err != nil {
		return s, err
	}
	s.MinNotional = "0.000000000001"
	var book struct {
		Code string
		Data []struct {
			Bids, Asks [][]string
			Ts         string
		}
	}
	if err = c.request(ctx, "GET", c.OKX+"/api/v5/market/books?instId="+url.QueryEscape(symbol)+"&sz=100", nil, &book); err != nil {
		return s, err
	}
	if book.Code != "0" || len(book.Data) != 1 || len(book.Data[0].Bids) == 0 || len(book.Data[0].Asks) == 0 || len(book.Data[0].Bids[0]) < 2 || len(book.Data[0].Asks[0]) < 2 {
		return s, errors.New("empty OKX perpetual book")
	}
	b := book.Data[0]
	s.Bid = core.Decimal(b.Bids[0][0])
	s.Ask = core.Decimal(b.Asks[0][0])
	if s.BidSize, err = productDecimal(b.Bids[0][1], r.CtVal); err != nil {
		return s, err
	}
	if s.AskSize, err = productDecimal(b.Asks[0][1], r.CtVal); err != nil {
		return s, err
	}
	for _, side := range []struct {
		raw [][]string
		dst *[]core.BookLevel
	}{{b.Bids, &s.Bids}, {b.Asks, &s.Asks}} {
		for _, level := range side.raw {
			if len(level) < 2 {
				return s, errors.New("invalid OKX book level")
			}
			qty, e := productDecimal(level[1], r.CtVal)
			if e != nil {
				return s, e
			}
			*side.dst = append(*side.dst, core.BookLevel{Price: core.Decimal(level[0]), Quantity: qty})
		}
	}
	var ms int64
	if _, err = fmt.Sscan(b.Ts, &ms); err != nil || ms <= 0 {
		return s, errors.New("invalid OKX book timestamp")
	}
	s.SourceTime = time.UnixMilli(ms).UTC()
	s.Observed = time.Now().UTC()
	return s, s.Validate(time.Now().UTC())
}
