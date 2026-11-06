package market

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nofx/planning/core"
)

type candleTransport func(*http.Request) (*http.Response, error)

func (f candleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCandleAdaptersAndUTCIntervals(t *testing.T) {
	now := time.Now().Unix() / 60 * 60
	for _, venue := range []string{"binance", "okx", "hyperliquid"} {
		t.Run(venue, func(t *testing.T) {
			symbol := map[string]string{"binance": "BTCUSDT", "okx": "BTC-USDT-SWAP", "hyperliquid": "BTC"}[venue]
			client := New()
			client.HTTP = &http.Client{Transport: candleTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				switch venue {
				case "binance":
					if r.Method != "GET" || r.URL.Path != "/fapi/v1/klines" || r.URL.Query().Get("interval") != "1d" {
						t.Fatal("wrong Binance candle route")
					}
					raw, _ := json.Marshal([]any{[]any{(now - 60) * 1000, "100", "110", "90", "105", "0"}, []any{now * 1000, "105", "112", "100", "108", "2"}})
					body = string(raw)
				case "okx":
					if r.URL.Query().Get("bar") != "1Dutc" {
						t.Fatal("daily candles must use UTC")
					}
					raw, _ := json.Marshal(map[string]any{"code": "0", "data": [][]string{{stringInt(now * 1000), "105", "112", "100", "108", "200", "2"}, {stringInt((now - 60) * 1000), "100", "110", "90", "105", "0", "0"}}})
					body = string(raw)
				case "hyperliquid":
					var request struct {
						Type string
						Req  struct {
							Coin, Interval     string
							StartTime, EndTime int64
						}
					}
					json.NewDecoder(r.Body).Decode(&request)
					if request.Type != "candleSnapshot" || request.Req.Coin != symbol || request.Req.Interval != "1d" {
						t.Fatal("wrong HL payload")
					}
					raw, _ := json.Marshal([]map[string]any{{"t": now * 1000, "T": (now+86400)*1000 - 1, "s": symbol, "i": "1d", "o": "105", "h": "112", "l": "100", "c": "108", "v": "2"}})
					body = string(raw)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			rows, err := client.Candles(context.Background(), CandleQuery{Venue: venue, Symbol: symbol, Interval: "1d", Limit: 300})
			if err != nil || len(rows) == 0 {
				t.Fatalf("candle decode failed: %v", err)
			}
			if rows[len(rows)-1].Close != "108" {
				t.Fatal("latest candle lost")
			}
			if len(rows) > 1 && rows[0].Time >= rows[1].Time {
				t.Fatal("candles not ordered")
			}
		})
	}
}
func stringInt(v int64) string { b, _ := json.Marshal(v); return string(b) }
func TestCandleRejectionAndRangeBounds(t *testing.T) {
	now := time.Now()
	base := Candle{now.Unix(), "100", "110", "90", "105", "0.00000000"}
	bad := []Candle{base, base, base, base, base, base}
	bad[0].High = "99"
	bad[1].Close = "NaN"
	bad[2].Volume = "0/1"
	bad[3].Volume = core.Decimal(strings.Repeat("0", 1000))
	bad[4].Time = now.Add(time.Hour).Unix()
	bad[5].Low = "-1"
	for _, row := range bad {
		if _, err := NormalizeCandles([]Candle{row}, now); err == nil {
			t.Fatalf("accepted invalid candle: %+v", row)
		}
	}
	if _, err := NormalizeCandles([]Candle{base, base}, now); err == nil {
		t.Fatal("duplicate candle accepted")
	}
	if _, err := NormalizeCandles([]Candle{base}, now); err != nil {
		t.Fatal(err)
	}
	for _, q := range []CandleQuery{{"binance", "BTCUSDT", "invalid", 0, 0, 300}, {"okx", "../../secret", "1m", 0, 0, 300}, {"hyperliquid", "BTC", "1m", 0, 0, 301}, {"binance", "BTCUSDT", "1m", 100, 50, 1}} {
		if q.Validate() == nil {
			t.Fatal("invalid candle request accepted")
		}
	}

}
