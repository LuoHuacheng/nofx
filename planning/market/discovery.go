package market

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MarketSymbol struct {
	Symbol             string  `json:"symbol"`
	Base               string  `json:"base"`
	Category           string  `json:"category"`
	Dex                string  `json:"dex,omitempty"`
	Volume24H          float64 `json:"volume_24h"`
	Price              string  `json:"price"`
	ExecutionSupported bool    `json:"execution_supported"`
}
type MarketList struct {
	Venue     string         `json:"venue"`
	Markets   []MarketSymbol `json:"markets"`
	FetchedAt time.Time      `json:"fetched_at"`
	Sources   []string       `json:"sources"`
	Warnings  []string       `json:"warnings,omitempty"`
}

// Discovery only reads public exchange metadata and 24h notional volume. It
// cannot use fee wallets, submit orders, or substitute one venue for another.
func (c *Client) Markets(ctx context.Context, venue string) (MarketList, error) {
	if venue != "binance" && venue != "hyperliquid" && venue != "okx" && venue != "bitget" {
		return MarketList{}, errors.New("Unsupported discovery venue")
	}
	result, err, _ := c.discoveryFlight.Do(venue, func() (any, error) {
		c.discoveryMu.Lock()
		cached, exists := c.discoveryCache[venue]
		c.discoveryMu.Unlock()
		if exists && time.Since(cached.FetchedAt) < time.Minute {
			return cached, nil
		}
		deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		out := MarketList{Venue: venue, Markets: []MarketSymbol{}, FetchedAt: time.Now().UTC()}
		var err error
		if venue == "binance" {
			err = c.binanceMarkets(deadline, &out)
		} else if venue == "okx" {
			err = c.okxMarkets(deadline, &out)
		} else if venue == "bitget" {
			err = c.bitgetMarkets(deadline, &out)
		} else {
			err = c.hyperliquidMarkets(deadline, &out)
		}
		if err != nil {
			return MarketList{}, err
		}
		sort.Slice(out.Markets, func(i, j int) bool {
			if out.Markets[i].Volume24H == out.Markets[j].Volume24H {
				return out.Markets[i].Symbol < out.Markets[j].Symbol
			}
			return out.Markets[i].Volume24H > out.Markets[j].Volume24H
		})
		c.discoveryMu.Lock()
		if c.discoveryCache == nil {
			c.discoveryCache = map[string]MarketList{}
		}
		c.discoveryCache[venue] = out
		c.discoveryMu.Unlock()
		return out, nil
	})
	if err != nil {
		return MarketList{}, err
	}
	return result.(MarketList), nil
}
func validVolume(value string) (float64, bool) {
	v, e := strconv.ParseFloat(value, 64)
	return v, e == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}
func (c *Client) binanceMarkets(ctx context.Context, out *MarketList) error {
	var info struct {
		Symbols []struct{ Symbol, BaseAsset, QuoteAsset, MarginAsset, Status, ContractType, UnderlyingType string }
	}
	var tickers []struct{ Symbol, QuoteVolume, LastPrice string }
	if err := c.request(ctx, "GET", c.BinanceFutures+"/fapi/v1/exchangeInfo", nil, &info); err != nil {
		return err
	}
	if err := c.request(ctx, "GET", c.BinanceFutures+"/fapi/v1/ticker/24hr", nil, &tickers); err != nil {
		return err
	}
	quotes := map[string]struct {
		volume float64
		price  string
	}{}
	for _, t := range tickers {
		if v, ok := validVolume(t.QuoteVolume); ok {
			quotes[t.Symbol] = struct {
				volume float64
				price  string
			}{v, t.LastPrice}
		}
	}
	for _, m := range info.Symbols {
		if m.Status != "TRADING" || m.QuoteAsset != "USDT" || m.MarginAsset != "USDT" || (m.ContractType != "PERPETUAL" && m.ContractType != "TRADIFI_PERPETUAL") || !symbolOK(m.Symbol) {
			continue
		}
		q, ok := quotes[m.Symbol]
		if !ok {
			continue
		}
		category := "crypto"
		switch m.UnderlyingType {
		case "EQUITY":
			category = "stocks"
		case "HK_EQUITY", "CN_EQUITY", "KR_EQUITY":
			category = "international_stocks"
		case "INDEX":
			category = "indices"
		case "COMMODITY":
			category = "commodities"
		case "FX":
			category = "fx"
		}
		out.Markets = append(out.Markets, MarketSymbol{Symbol: m.Symbol, Base: m.BaseAsset, Category: category, Volume24H: q.volume, Price: q.price, ExecutionSupported: true})
	}
	out.Sources = []string{c.BinanceFutures + "/fapi/v1/exchangeInfo", c.BinanceFutures + "/fapi/v1/ticker/24hr"}
	return nil
}
func (c *Client) hyperliquidMarkets(ctx context.Context, out *MarketList) error {
	var dexes []*struct{ Name string }
	if err := c.request(ctx, "POST", c.Hyperliquid+"/info", map[string]string{"type": "perpDexs"}, &dexes); err != nil {
		return err
	}
	var categories [][]string
	categoriesOK := c.request(ctx, "POST", c.Hyperliquid+"/info", map[string]string{"type": "perpCategories"}, &categories) == nil
	classes := map[string]string{}
	for _, pair := range categories {
		if len(pair) == 2 {
			classes[pair[0]] = pair[1]
		}
	}
	names := []string{""}
	seen := map[string]bool{"": true}
	for _, d := range dexes {
		if d != nil && d.Name != "" && len(d.Name) <= 32 && !seen[d.Name] && symbolOK(d.Name+":BTC") {
			names = append(names, d.Name)
			seen[d.Name] = true
		}
	}
	if len(names) > 33 {
		names = names[:33]
		out.Warnings = append(out.Warnings, "Discovery capped at 32 builder markets")
	}
	var mu sync.Mutex
	var group sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, dex := range names {
		group.Add(1)
		go func(dex string) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				label := dex
				if label == "" {
					label = "core"
				}
				out.Warnings = append(out.Warnings, label+": discovery deadline reached")
				mu.Unlock()
				return
			}
			defer func() { <-slots }()
			rows, err := c.hyperDexMarkets(ctx, dex, classes)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				label := dex
				if label == "" {
					label = "core"
				}
				out.Warnings = append(out.Warnings, label+": public market data unavailable")
				return
			}
			out.Markets = append(out.Markets, rows...)
		}(dex)
	}
	group.Wait()
	if !categoriesOK {
		out.Warnings = append(out.Warnings, "Stock categories unavailable; use All markets to search by symbol")
	}
	if len(out.Markets) == 0 {
		return errors.New("Hyperliquid market discovery unavailable")
	}
	sort.Strings(out.Warnings)
	out.Sources = []string{c.Hyperliquid + "/info · perpDexs / perpCategories / metaAndAssetCtxs"}
	return nil
}
func (c *Client) hyperDexMarkets(ctx context.Context, dex string, categories map[string]string) ([]MarketSymbol, error) {
	var raw []json.RawMessage
	if err := c.request(ctx, "POST", c.Hyperliquid+"/info", map[string]string{"type": "metaAndAssetCtxs", "dex": dex}, &raw); err != nil {
		return nil, err
	}
	if len(raw) != 2 {
		return nil, errors.New("Invalid asset contexts")
	}
	var meta struct {
		CollateralToken *int
		Universe        []struct {
			Name       string
			IsDelisted bool
		}
	}
	var quotes []struct{ DayNtlVlm, MarkPx string }
	if json.Unmarshal(raw[0], &meta) != nil || json.Unmarshal(raw[1], &quotes) != nil || len(meta.Universe) != len(quotes) {
		return nil, errors.New("Mismatched market metadata")
	}
	rows := []MarketSymbol{}
	for i, m := range meta.Universe {
		if m.IsDelisted || !symbolOK(m.Name) || (dex != "" && !strings.HasPrefix(m.Name, dex+":")) || (dex == "" && strings.Contains(m.Name, ":")) {
			continue
		}
		volume, ok := validVolume(quotes[i].DayNtlVlm)
		if !ok {
			continue
		}
		category := categories[m.Name]
		if category == "stock" {
			category = "stocks"
		}
		if category == "" {
			if dex == "" {
				category = "crypto"
			} else {
				category = "other"
			}
		}
		rows = append(rows, MarketSymbol{Symbol: m.Name, Base: strings.TrimPrefix(m.Name, dex+":"), Category: category, Dex: dex, Volume24H: volume, Price: quotes[i].MarkPx, ExecutionSupported: dex == "" || (dex == "xyz" && meta.CollateralToken != nil && *meta.CollateralToken == 0)})
	}
	return rows, nil
}
