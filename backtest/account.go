package backtest

import (
	"fmt"
	"time"
)

// Position is an open backtest position. Cross margin: all equity backs
// all positions; margin used per position is notional/leverage.
type Position struct {
	Symbol      string
	Side        string // long | short
	Qty         float64
	EntryPrice  float64
	Leverage    int
	StopLoss    float64
	TakeProfit  float64
	EntryTime   time.Time
	MarkPrice   float64
	LiqPrice    float64
	Margin      float64 // margin locked at open (sizeUSD/leverage)
	FundingPaid float64 // cumulative funding paid (positive = paid)
}

// Notional returns the position value at entry price.
func (p *Position) Notional() float64 { return p.Qty * p.EntryPrice }

// MarginUsed returns the margin locked by this position.
func (p *Position) MarginUsed() float64 { return p.Margin }

// UnrealizedPnL at the given mark price.
func (p *Position) UnrealizedPnL(mark float64) float64 {
	if p.Side == "long" {
		return (mark - p.EntryPrice) * p.Qty
	}
	return (p.EntryPrice - mark) * p.Qty
}

// BacktestAccount simulates a cross-margin futures account. It only
// supports market orders, one position per symbol (open long/short and
// close via the same action), bar-on-close fills and perp funding.
type BacktestAccount struct {
	initialBalance float64
	balance        float64 // cash
	feeMaker       float64
	feeTaker       float64
	positions      map[string]*Position
	trades         []BacktestTrade
	realizedPnL    float64
	liquidated     bool // any position force-closed via liquidation
}

// NewAccount creates an account with the given balance and fee rates.
func NewAccount(initialBalance, feeMaker, feeTaker float64) *BacktestAccount {
	return &BacktestAccount{
		initialBalance: initialBalance,
		balance:        initialBalance,
		feeMaker:       feeMaker,
		feeTaker:       feeTaker,
		positions:      make(map[string]*Position),
	}
}

// Balance returns the cash balance.
func (a *BacktestAccount) Balance() float64 { return a.balance }

// InitialBalance returns the starting cash.
func (a *BacktestAccount) InitialBalance() float64 { return a.initialBalance }

// Positions returns a snapshot of open positions.
func (a *BacktestAccount) Positions() map[string]*Position {
	out := make(map[string]*Position, len(a.positions))
	for k, v := range a.positions {
		cp := *v
		out[k] = &cp
	}
	return out
}

// HasPosition reports whether symbol currently has an open position.
func (a *BacktestAccount) HasPosition(symbol string) bool {
	_, ok := a.positions[symbol]
	return ok
}

// PositionAt returns the open position for symbol, or nil.
func (a *BacktestAccount) PositionAt(symbol string) *Position {
	if p, ok := a.positions[symbol]; ok {
		cp := *p
		return &cp
	}
	return nil
}

// UnrealizedPnL values all open positions at their current mark.
func (a *BacktestAccount) UnrealizedPnL() float64 {
	total := 0.0
	for _, p := range a.positions {
		total += p.UnrealizedPnL(p.MarkPrice)
	}
	return total
}

// MarginUsed returns total locked margin.
func (a *BacktestAccount) MarginUsed() float64 {
	total := 0.0
	for _, p := range a.positions {
		total += p.MarginUsed()
	}
	return total
}

// Equity = cash + unrealized PnL (cross margin).
func (a *BacktestAccount) Equity() float64 { return a.balance + a.UnrealizedPnL() }

// RealizedPnL returns cumulative realized PnL from closed trades.
func (a *BacktestAccount) RealizedPnL() float64 { return a.realizedPnL }

// Trades returns all recorded trades (open + close).
func (a *BacktestAccount) Trades() []BacktestTrade {
	out := make([]BacktestTrade, len(a.trades))
	copy(out, a.trades)
	return out
}

// Liquidated reports whether any position was force-closed.
func (a *BacktestAccount) Liquidated() bool { return a.liquidated }

// OpenLong / OpenShort open a position with notional sizeUSD at price.
// Fees are charged against the notional with the taker rate. Margin is
// locked at notional/leverage. A same-symbol position in the opposite
// direction is rejected (the real trader requires closing first).
func (a *BacktestAccount) Open(symbol, side string, sizeUSD, price float64, leverage int, t time.Time) error {
	if sizeUSD <= 0 {
		return fmt.Errorf("open %s: non-positive size %.4f", symbol, sizeUSD)
	}
	if price <= 0 {
		return fmt.Errorf("open %s: non-positive price %.6f", symbol, price)
	}
	if a.HasPosition(symbol) {
		return fmt.Errorf("open %s: position already exists", symbol)
	}
	if leverage <= 0 {
		leverage = 1
	}
	fee := sizeUSD * a.feeTaker
	margin := sizeUSD / float64(leverage)
	if a.balance < margin+fee {
		return fmt.Errorf("open %s: insufficient balance (need %.2f, have %.2f)", symbol, margin+fee, a.balance)
	}
	notionalAfterFee := sizeUSD - fee
	qty := notionalAfterFee / price

	a.balance -= margin + fee
	a.positions[symbol] = &Position{
		Symbol:     symbol,
		Side:       side,
		Qty:        qty,
		EntryPrice: price,
		Leverage:   leverage,
		StopLoss:   0,
		TakeProfit: 0,
		EntryTime:  t,
		MarkPrice:  price,
		LiqPrice:   liquidationPrice(side, price, leverage),
		Margin:     margin,
	}
	a.trades = append(a.trades, BacktestTrade{
		Time: t, Symbol: symbol, Side: "buy", Position: "open", Price: price, Size: qty, Fee: fee,
	})
	return nil
}

// Close closes the position for symbol at price (market order). PnL is
// realized and margin is returned. StopLoss/TakeProfit are checked by
// the engine against bar extremes before this call; the close price is
// the fill price determined by the execution model.
func (a *BacktestAccount) Close(symbol string, price float64, t time.Time) (BacktestTrade, error) {
	p, ok := a.positions[symbol]
	if !ok {
		return BacktestTrade{}, fmt.Errorf("close %s: no open position", symbol)
	}
	if price <= 0 {
		return BacktestTrade{}, fmt.Errorf("close %s: non-positive price %.6f", symbol, price)
	}
	closeSide := "sell"
	if p.Side == "short" {
		closeSide = "buy"
	}
	notional := p.Qty * price
	fee := notional * a.feeTaker
	pricePnL := p.UnrealizedPnL(price)
	// Funding was already applied to cash incrementally by ApplyFunding, so
	// only the price PnL and the close fee move the balance here — adding
	// FundingPaid again would double-count it.
	a.balance += p.MarginUsed() + pricePnL - fee
	// The trade's reported PnL keeps funding attribution (what the user
	// expects a trade result to include).
	tradePnL := pricePnL - fee - p.FundingPaid
	a.realizedPnL += tradePnL

	trade := BacktestTrade{
		Time: t, Symbol: symbol, Side: closeSide, Position: "close", Price: price, Size: p.Qty, Fee: fee, PnL: tradePnL,
	}
	a.trades = append(a.trades, trade)
	delete(a.positions, symbol)
	return trade, nil
}

// UpdateMark sets the mark price for a symbol's position (no-op if none).
func (a *BacktestAccount) UpdateMark(symbol string, mark float64) {
	if p, ok := a.positions[symbol]; ok {
		p.MarkPrice = mark
	}
}

// SetProtection sets stop-loss / take-profit levels on an open
// position (0 disables that level).
func (a *BacktestAccount) SetProtection(symbol string, stopLoss, takeProfit float64) {
	if p, ok := a.positions[symbol]; ok {
		p.StopLoss = stopLoss
		p.TakeProfit = takeProfit
	}
}

// ApplyFunding settles perp funding for a position at the given rate.
// Positive rate = longs pay shorts. Funding is charged against cash.
func (a *BacktestAccount) ApplyFunding(symbol string, rate float64, t time.Time) {
	p, ok := a.positions[symbol]
	if !ok {
		return
	}
	amount := p.Qty * p.MarkPrice * rate
	if p.Side == "long" {
		a.balance -= amount
		p.FundingPaid += amount
	} else {
		// Shorts receive funding when rate is positive.
		a.balance += amount
		p.FundingPaid -= amount
	}
}

// CheckLiquidation force-closes any position whose mark price has
// crossed its liquidation price (cross margin with zero maintenance
// margin: a long liquidates when equity <= margin of that position).
// The fill happens at the liquidation price, not the mark.
// ponytail: per-position zero-maintenance-margin liq price; a proper
// cross-margin maintenance-margin model (isolated not supported per §2.2)
// can be layered in later without changing the interface.
func (a *BacktestAccount) CheckLiquidation(t time.Time) []BacktestTrade {
	var liquidated []BacktestTrade
	for symbol, p := range a.positions {
		crossed := false
		if p.Side == "long" && p.MarkPrice <= p.LiqPrice {
			crossed = true
		}
		if p.Side == "short" && p.MarkPrice >= p.LiqPrice {
			crossed = true
		}
		if !crossed {
			continue
		}
		trade, err := a.Close(symbol, p.LiqPrice, t)
		if err == nil {
			a.liquidated = true
			trade.Position = "liquidation"
			liquidated = append(liquidated, trade)
		}
	}
	return liquidated
}

// liquidationPrice computes the zero-maintenance-margin liquidation
// price for a position: long -> entry*(1-1/leverage), short ->
// entry*(1+1/leverage).
func liquidationPrice(side string, entry float64, leverage int) float64 {
	if leverage <= 1 {
		if side == "short" {
			return entry * 2 // worst case: price doubles
		}
		return 0 // price goes to zero
	}
	if side == "long" {
		return entry * (1 - 1/float64(leverage))
	}
	return entry * (1 + 1/float64(leverage))
}
