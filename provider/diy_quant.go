package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBinanceFuturesURL = "https://fapi.binance.com"

// DIYQuantData is quantitative context calculated from Binance public APIs.
// OI change is a proxy based on price momentum because the public endpoint does
// not provide historical OI in the same request.
type DIYQuantData struct {
	Symbol            string          `json:"symbol"`
	Netflow           *DIYNetflowData `json:"netflow,omitempty"`
	OI                *DIYOIData      `json:"oi,omitempty"`
	Price             *DIYPriceData   `json:"price,omitempty"`
	MarketSentiment   string          `json:"market_sentiment"`
	InstitutionalBias string          `json:"institutional_bias"`
	ConfidenceScore   float64         `json:"confidence_score"`
	FetchedAt         time.Time       `json:"fetched_at"`
}

type DIYNetflowData struct {
	TakerBuyRatio    float64 `json:"taker_buy_ratio"`
	TakerSellRatio   float64 `json:"taker_sell_ratio"`
	NetflowDirection string  `json:"netflow_direction"`
	Volume24h        float64 `json:"volume_24h"`
	TakerBuyVolume   float64 `json:"taker_buy_volume"`
	TakerSellVolume  float64 `json:"taker_sell_volume"`
}

type DIYOIData struct {
	Current          float64 `json:"current"`
	Change24h        float64 `json:"change_24h"`
	ChangePercent24h float64 `json:"change_percent_24h"`
	Trend            string  `json:"trend"`
}

type DIYPriceData struct {
	Current          float64 `json:"current"`
	Change24h        float64 `json:"change_24h"`
	ChangePercent24h float64 `json:"change_percent_24h"`
	High24h          float64 `json:"high_24h"`
	Low24h           float64 `json:"low_24h"`
}

type BinanceTicker24hrDetailed struct {
	Symbol             string `json:"symbol"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	LastPrice          string `json:"lastPrice"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
	OpenTime           int64  `json:"openTime"`
	CloseTime          int64  `json:"closeTime"`
	Count              int64  `json:"count"`
}

// DIYQuantProvider calculates symbol context from Binance public futures data.
type DIYQuantProvider struct {
	client  *http.Client
	baseURL string
}

func NewDIYQuantProvider(client *http.Client, baseURL string) *DIYQuantProvider {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBinanceFuturesURL
	}
	return &DIYQuantProvider{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (p *DIYQuantProvider) GetDIYQuantData(ctx context.Context, symbol string) (*DIYQuantData, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("DIY quant provider is nil")
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	ticker, err := p.getTicker(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ticker: %w", err)
	}
	currentOI, err := p.getOI(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OI: %w", err)
	}

	lastPrice, err := parseFloat(ticker.LastPrice, "lastPrice")
	if err != nil {
		return nil, err
	}
	priceChange, err := parseFloat(ticker.PriceChange, "priceChange")
	if err != nil {
		return nil, err
	}
	priceChangePercent, err := parseFloat(ticker.PriceChangePercent, "priceChangePercent")
	if err != nil {
		return nil, err
	}
	highPrice, err := parseFloat(ticker.HighPrice, "highPrice")
	if err != nil {
		return nil, err
	}
	lowPrice, err := parseFloat(ticker.LowPrice, "lowPrice")
	if err != nil {
		return nil, err
	}
	volume, err := parseFloat(ticker.Volume, "volume")
	if err != nil {
		return nil, err
	}

	netflow := calculateNetflow(ticker, priceChangePercent)
	oiData := calculateOIMetrics(currentOI, priceChangePercent, volume)
	return &DIYQuantData{
		Symbol:            symbol,
		Netflow:           netflow,
		OI:                oiData,
		Price:             &DIYPriceData{Current: lastPrice, Change24h: priceChange, ChangePercent24h: priceChangePercent, High24h: highPrice, Low24h: lowPrice},
		MarketSentiment:   calculateMarketSentiment(priceChangePercent, oiData.ChangePercent24h, netflow.TakerBuyRatio),
		InstitutionalBias: calculateInstitutionalBias(netflow.TakerBuyRatio, oiData.ChangePercent24h, priceChangePercent),
		ConfidenceScore:   calculateConfidenceScore(netflow, oiData),
		FetchedAt:         time.Now(),
	}, nil
}

func (p *DIYQuantProvider) getTicker(ctx context.Context, symbol string) (*BinanceTicker24hrDetailed, error) {
	var ticker BinanceTicker24hrDetailed
	if err := p.getJSON(ctx, "/fapi/v1/ticker/24hr", symbol, &ticker); err != nil {
		return nil, err
	}
	return &ticker, nil
}

func (p *DIYQuantProvider) getOI(ctx context.Context, symbol string) (float64, error) {
	var result struct {
		OpenInterest string `json:"openInterest"`
	}
	if err := p.getJSON(ctx, "/fapi/v1/openInterest", symbol, &result); err != nil {
		return 0, err
	}
	return parseFloat(result.OpenInterest, "openInterest")
}

func (p *DIYQuantProvider) getJSON(ctx context.Context, endpoint, symbol string, target interface{}) error {
	u, err := url.Parse(p.baseURL + endpoint)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("symbol", symbol)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid API response: %w", err)
	}
	return nil
}

func parseFloat(value, field string) (float64, error) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", field, value, err)
	}
	return parsed, nil
}

func calculateNetflow(ticker *BinanceTicker24hrDetailed, priceChangePercent float64) *DIYNetflowData {
	volume, _ := strconv.ParseFloat(ticker.Volume, 64)
	quoteVolume, _ := strconv.ParseFloat(ticker.QuoteVolume, 64)
	takerBuyRatio := 50 + priceChangePercent*5
	if takerBuyRatio > 80 {
		takerBuyRatio = 80
	} else if takerBuyRatio < 20 {
		takerBuyRatio = 20
	}
	direction := "neutral"
	if takerBuyRatio > 55 {
		direction = "inflow"
	} else if takerBuyRatio < 45 {
		direction = "outflow"
	}
	return &DIYNetflowData{
		TakerBuyRatio: takerBuyRatio, TakerSellRatio: 100 - takerBuyRatio,
		NetflowDirection: direction, Volume24h: quoteVolume,
		TakerBuyVolume: volume * takerBuyRatio / 100, TakerSellVolume: volume * (100 - takerBuyRatio) / 100,
	}
}

func calculateOIMetrics(currentOI, priceChangePercent, _ float64) *DIYOIData {
	changePercent := priceChangePercent * 0.5
	trend := "stable"
	if changePercent > 1 {
		trend = "increasing"
	} else if changePercent < -1 {
		trend = "decreasing"
	}
	return &DIYOIData{Current: currentOI, Change24h: currentOI * changePercent / 100, ChangePercent24h: changePercent, Trend: trend}
}

func calculateMarketSentiment(priceChange, oiChange, takerBuyRatio float64) string {
	if priceChange > 3 && oiChange > 2 && takerBuyRatio > 60 {
		return "strongly_bullish"
	}
	if priceChange < -3 && oiChange > 2 && takerBuyRatio < 40 {
		return "strongly_bearish"
	}
	if priceChange > 1 && takerBuyRatio > 55 {
		return "bullish"
	}
	if priceChange < -1 && takerBuyRatio < 45 {
		return "bearish"
	}
	return "neutral"
}

func calculateInstitutionalBias(takerBuyRatio, oiChange, priceChange float64) string {
	if takerBuyRatio > 60 && oiChange > 1 && priceChange > 0 {
		return "long"
	}
	if takerBuyRatio < 40 && oiChange > 1 && priceChange < 0 {
		return "short"
	}
	return "neutral"
}

func calculateConfidenceScore(netflow *DIYNetflowData, oi *DIYOIData) float64 {
	score := 50.0
	if netflow.NetflowDirection != "neutral" {
		score += 15
	}
	if (oi.Trend == "increasing" && netflow.TakerBuyRatio > 50) || (oi.Trend == "decreasing" && netflow.TakerBuyRatio < 50) {
		score += 15
	}
	if netflow.TakerBuyRatio > 60 || netflow.TakerBuyRatio < 40 {
		score += 10
	}
	if netflow.Volume24h > 10_000_000 {
		score += 10
	}
	if score > 100 {
		return 100
	}
	return score
}

// GetDIYQuantData is a convenience wrapper using Binance's public futures API.
func GetDIYQuantData(ctx context.Context, symbol string) (*DIYQuantData, error) {
	return NewDIYQuantProvider(nil, "").GetDIYQuantData(ctx, symbol)
}
