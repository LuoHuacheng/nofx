// Package localpool provides a self-hosted AI500-style candidate pool that
// scores USDT perps from public Binance Futures 24h tickers, with no upstream
// API key. Used as a fallback when the nofxos.ai AI500 endpoint is unavailable.
package localpool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const defaultBinanceFuturesURL = "https://fapi.binance.com"

// minQuoteVolume filters out illiquid perps (USDT quote volume over 24h).
const minQuoteVolume = 5e6

// ticker mirrors the fields of GET /fapi/v1/ticker/24hr we need.
type ticker struct {
	Symbol             string `json:"symbol"`
	PriceChangePercent string `json:"priceChangePercent"`
	LastPrice          string `json:"lastPrice"`
	QuoteVolume        string `json:"quoteVolume"`
	Count              int64  `json:"count"`
}

// scoredSymbol is a candidate with a composite score in [0,1].
type scoredSymbol struct {
	symbol string
	score  float64
}

// PoolProvider scores USDT perpetuals from public Binance Futures data.
type PoolProvider struct {
	client  *http.Client
	baseURL string
}

// NewPoolProvider creates a provider. baseURL defaults to fapi.binance.com.
func NewPoolProvider(client *http.Client, baseURL string) *PoolProvider {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBinanceFuturesURL
	}
	return &PoolProvider{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

// GetTopRatedCoins returns the top N symbols by composite score
// (price momentum 50%, liquidity 30%, activity 20%), descending.
func (p *PoolProvider) GetTopRatedCoins(limit int) ([]string, error) {
	if limit <= 0 {
		limit = 30
	}
	tickers, err := p.fetchTickers(context.Background())
	if err != nil {
		return nil, err
	}

	ranked := rankTickers(tickers, limit)
	symbols := make([]string, len(ranked))
	for i, s := range ranked {
		symbols[i] = s.symbol
	}
	return symbols, nil
}

func (p *PoolProvider) fetchTickers(ctx context.Context) ([]ticker, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/fapi/v1/ticker/24hr", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Binance 24h tickers: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance ticker API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tickers []ticker
	if err := json.Unmarshal(body, &tickers); err != nil {
		return nil, fmt.Errorf("invalid ticker response: %w", err)
	}
	return tickers, nil
}

// rankTickers filters to USDT perps with adequate liquidity, min-max
// normalizes the three factors, then returns the top N weighted.
func rankTickers(tickers []ticker, limit int) []scoredSymbol {
	type candidate struct {
		ticker
		price  float64 // 24h change %
		qvol   float64 // 24h quote volume (USDT)
		logvol float64
		logcnt float64
	}
	cands := make([]candidate, 0, len(tickers))
	for _, t := range tickers {
		if !strings.HasSuffix(t.Symbol, "USDT") || t.Count <= 0 {
			continue
		}
		price, err1 := parseFloat(t.PriceChangePercent)
		qvol, err2 := parseFloat(t.QuoteVolume)
		if err1 != nil || err2 != nil || qvol < minQuoteVolume {
			continue
		}
		cands = append(cands, candidate{
			ticker: t,
			price:  price,
			qvol:   qvol,
			logvol: math.Log10(qvol),
			logcnt: math.Log10(float64(t.Count)),
		})
	}
	if len(cands) == 0 {
		return nil
	}

	// min-max bounds for normalization
	pMin, pMax := cands[0].price, cands[0].price
	vMin, vMax := cands[0].logvol, cands[0].logvol
	cMin, cMax := cands[0].logcnt, cands[0].logcnt
	for _, c := range cands[1:] {
		pMin, pMax = math.Min(pMin, c.price), math.Max(pMax, c.price)
		vMin, vMax = math.Min(vMin, c.logvol), math.Max(vMax, c.logvol)
		cMin, cMax = math.Min(cMin, c.logcnt), math.Max(cMax, c.logcnt)
	}

	ranked := make([]scoredSymbol, 0, len(cands))
	for _, c := range cands {
		score := 0.5*norm(c.price, pMin, pMax) + 0.3*norm(c.logvol, vMin, vMax) + 0.2*norm(c.logcnt, cMin, cMax)
		ranked = append(ranked, scoredSymbol{symbol: c.Symbol, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// norm maps v within [min,max] to [0,1]; degenerate ranges yield 0.
func norm(v, min, max float64) float64 {
	if max <= min {
		return 0
	}
	return (v - min) / (max - min)
}

func parseFloat(v string) (float64, error) {
	var f float64
	if err := json.Unmarshal([]byte(strings.TrimSpace(v)), &f); err != nil {
		return 0, fmt.Errorf("invalid number %q: %w", v, err)
	}
	return f, nil
}
