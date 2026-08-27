package backtest

import (
	"math"
	"sort"
)

// Metrics aggregates run performance (§1.1 / §5 P3.1).
type Metrics struct {
	FinalBalance   float64 `json:"final_balance"`
	TotalReturnPct float64 `json:"total_return_pct"`
	MaxDrawdownPct float64 `json:"max_drawdown_pct"`
	SharpeRatio    float64 `json:"sharpe_ratio"`
	WinRate        float64 `json:"win_rate"`
	ProfitFactor   float64 `json:"profit_factor"`
	TradeCount     int     `json:"trade_count"`
	TotalPnL       float64 `json:"total_pnl"`
}

// ComputeMetrics derives metrics from the equity curve and trades.
// Only closed trades (Position == "close") count toward win rate,
// profit factor and trade count. Returns are per equity sample; the
// Sharpe ratio is annualized assuming continuous trading
// (sqrt(samplesPerYear)).
func ComputeMetrics(equity []EquityPoint, trades []BacktestTrade, initialBalance float64) Metrics {
	m := Metrics{}

	if len(equity) == 0 {
		return m
	}
	last := equity[len(equity)-1]
	m.FinalBalance = last.Equity
	m.TotalPnL = last.Equity - initialBalance
	if initialBalance > 0 {
		m.TotalReturnPct = (last.Equity/initialBalance - 1) * 100
	}

	// Max drawdown on the equity curve (peak to trough, in %).
	peak := equity[0].Equity
	maxDD := 0.0
	for _, p := range equity {
		if p.Equity > peak {
			peak = p.Equity
		}
		if peak > 0 {
			dd := (peak - p.Equity) / peak * 100
			if dd > maxDD {
				maxDD = dd
			}
		}
	}
	m.MaxDrawdownPct = maxDD

	// Sharpe: mean(period return)/std(period return) * sqrt(periods/year).
	// Use only periods with more than one point.
	returns := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		prev := equity[i-1].Equity
		cur := equity[i].Equity
		if prev <= 0 {
			continue
		}
		returns = append(returns, cur/prev-1)
	}
	if len(returns) >= 2 {
		mean := 0.0
		for _, r := range returns {
			mean += r
		}
		mean /= float64(len(returns))

		variance := 0.0
		for _, r := range returns {
			d := r - mean
			variance += d * d
		}
		variance /= float64(len(returns) - 1)
		std := math.Sqrt(variance)
		if std > 0 {
			// Annualization: assume one sample per bar period, 365*24*60
			// minutes a year; samplesPerYear is derived below from the
			// actual spacing when possible.
			samplesPerYear := 525600.0
			if len(equity) >= 2 {
				spanMin := equity[len(equity)-1].Time.Sub(equity[0].Time).Minutes()
				if spanMin > 0 {
					samplesPerYear = 525600.0 / (spanMin / float64(len(equity)-1))
				}
			}
			m.SharpeRatio = mean / std * math.Sqrt(samplesPerYear)
		}
	}

	// Win rate / profit factor over closed trades.
	wins := 0.0
	losses := 0.0
	grossProfit := 0.0
	grossLoss := 0.0
	for _, t := range trades {
		if t.Position != "close" {
			continue
		}
		m.TradeCount++
		if t.PnL > 0 {
			wins++
			grossProfit += t.PnL
		} else {
			losses++
			grossLoss += -t.PnL
		}
	}
	total := wins + losses
	if total > 0 {
		m.WinRate = wins / total * 100
	}
	if grossLoss > 0 {
		m.ProfitFactor = grossProfit / grossLoss
	} else if grossProfit > 0 {
		m.ProfitFactor = math.Inf(1)
	}

	return m
}

// SortTradesByTime returns trades sorted ascending by time (stable).
func SortTradesByTime(trades []BacktestTrade) []BacktestTrade {
	out := make([]BacktestTrade, len(trades))
	copy(out, trades)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}
