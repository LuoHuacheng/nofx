package core

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Execution describes a finite, user-approved order basket, never a strategy.
// Weights apply to base quantity. TWAP uses a fixed limit cap, never market orders.
type Execution struct {
	Kind            string  `json:"kind"` // market, limit, ladder, twap
	Levels          []Level `json:"levels,omitempty"`
	Slices          int     `json:"slices,omitempty"`
	IntervalSeconds int     `json:"interval_seconds,omitempty"`
}
type Level struct {
	Price  Decimal `json:"price"`
	Weight Decimal `json:"weight"`
}
type OrderSpec struct {
	Index         int     `json:"index"`
	Price         Decimal `json:"price"`
	Quantity      Decimal `json:"quantity"`
	OffsetSeconds int     `json:"offset_seconds"`
	VenueQuantity Decimal `json:"venue_quantity,omitempty"`
}
type ChildOrder struct {
	Index   int       `json:"index"`
	State   string    `json:"state"` // scheduled, pending, filled, cancelled, expired
	Updated time.Time `json:"updated"`
}

func compileExecution(s Snapshot, c Constraints, i Idea, now time.Time) (Plan, error) {
	x := *i.Execution
	x.Levels = append([]Level(nil), x.Levels...)
	i.Execution = &x
	var levels []Level
	switch x.Kind {
	case "market":
		if len(x.Levels) != 0 || x.Slices != 0 || x.IntervalSeconds != 0 {
			return Plan{}, errors.New("market entry does not accept levels or schedule")
		}
		if err := s.Validate(now); err != nil {
			return Plan{}, err
		}
		if err := c.Validate(); err != nil {
			return Plan{}, err
		}
		if s.Kind != "perpetual" || (s.Venue != "binance" && s.Venue != "hyperliquid" && s.Venue != "okx" && s.Venue != "bitget") {
			return Plan{}, errors.New("market entry requires a supported perpetual exchange")
		}
		// Model prices are not executable quotes. Lock a fresh reference and a
		// conservative execution estimate; Hyperliquid uses it as its IOC bound.
		i.Entry = s.Ask
		slip := div(must(c.SlippageBPS), big.NewRat(10000, 1))
		bound := floorStep(mul(must(s.Ask), add(big.NewRat(1, 1), slip)), must(s.Tick))
		if i.Side == "short" {
			i.Entry = s.Bid
			price := mul(must(s.Bid), sub(big.NewRat(1, 1), slip))
			bound = floorStep(price, must(s.Tick))
			if bound.Cmp(price) < 0 {
				bound = add(bound, must(s.Tick))
			}
		}
		levels = []Level{{number(bound), "1"}}
	case "limit":
		if len(x.Levels) != 0 || x.Slices != 0 || x.IntervalSeconds != 0 {
			return Plan{}, errors.New("single limit does not accept levels or schedule")
		}
		levels = []Level{{i.Entry, "1"}}
	case "ladder":
		if len(x.Levels) < 2 || len(x.Levels) > 8 || x.Slices != 0 || x.IntervalSeconds != 0 {
			return Plan{}, errors.New("A price ladder requires 2-8 prices and quantity weights, without time parameters")
		}
		levels = x.Levels
		sum := new(big.Rat)
		seen := map[Decimal]bool{}
		for _, l := range levels {
			if !Positive(l.Weight) || must(l.Weight).Cmp(big.NewRat(1, 1)) > 0 || !Positive(l.Price) {
				return Plan{}, errors.New("invalid ladder weight or price")
			}
			if seen[number(must(l.Price))] {
				return Plan{}, errors.New("duplicate ladder price")
			}
			seen[number(must(l.Price))] = true
			sum = add(sum, must(l.Weight))
		}
		if sum.Cmp(big.NewRat(1, 1)) != 0 {
			return Plan{}, errors.New("Ladder quantity weights must sum to 1")
		}
	case "twap":
		if len(x.Levels) != 0 || x.Slices < 2 || x.Slices > 24 || x.IntervalSeconds < 5 || x.IntervalSeconds > 300 || (x.Slices-1)*x.IntervalSeconds > 3600 {
			return Plan{}, errors.New("TWAP requires 2-24 slices, 5-300 seconds apart, lasting at most one hour")
		}
		// Rational equal weights avoid approximation in base quantity allocation.
		for n := 0; n < x.Slices; n++ {
			levels = append(levels, Level{i.Entry, "1"})
		}
	default:
		return Plan{}, errors.New("unknown execution kind")
	}
	// Validate the declared chart entry and every child's stop/target and net ratio.
	base := i
	base.Execution = nil
	if _, err := Compile(s, c, base, now); err != nil {
		return Plan{}, err
	}
	lossSum, gainSum, notional := new(big.Rat), new(big.Rat), new(big.Rat)
	cost := div(add(must(c.FeeBPS), must(c.SlippageBPS)), big.NewRat(10000, 1))
	weights := []*big.Rat{}
	for n, l := range levels {
		leg := base
		leg.Entry = l.Price
		if _, err := Compile(s, c, leg, now); err != nil {
			return Plan{}, fmt.Errorf("Order %d: %w", n+1, err)
		}
		w := must(l.Weight)
		if x.Kind == "twap" {
			w = big.NewRat(1, int64(x.Slices))
		}
		weights = append(weights, w)
		loss := add(abs(sub(must(l.Price), must(i.Stop))), mul(add(must(l.Price), must(i.Stop)), cost))
		gain := sub(abs(sub(must(i.Target), must(l.Price))), mul(add(must(l.Price), must(i.Target)), cost))
		lossSum = add(lossSum, mul(w, loss))
		gainSum = add(gainSum, mul(w, gain))
		notional = add(notional, mul(w, mul(must(l.Price), add(big.NewRat(1, 1), cost))))
	}
	liquidity := must(s.AskSize)
	if i.Side == "short" {
		liquidity = must(s.BidSize)
	}
	size, limit := sizeByLimits(c, div(must(c.MaxRisk), lossSum), div(must(c.MaxNotional), notional), div(liquidity, big.NewRat(10, 1)))
	total := floorStep(size, must(s.Step))
	p := Plan{Snapshot: s, Constraints: c, Idea: i, SizingLimit: limit, Created: now, Expires: now.Add(5 * time.Minute)}
	risk, reward, qty := new(big.Rat), new(big.Rat), new(big.Rat)
	for n, l := range levels {
		q := floorStep(mul(total, weights[n]), must(s.Step))
		if q.Cmp(must(s.MinQty)) < 0 || mul(q, must(l.Price)).Cmp(must(s.MinNotional)) < 0 {
			return Plan{}, fmt.Errorf("Child order %d is below venue minimum quantity or notional; reduce slice count or adjust the budget", n+1)
		}
		loss := add(abs(sub(must(l.Price), must(i.Stop))), mul(add(must(l.Price), must(i.Stop)), cost))
		gain := sub(abs(sub(must(i.Target), must(l.Price))), mul(add(must(l.Price), must(i.Target)), cost))
		offset := 0
		if x.Kind == "twap" {
			offset = n * x.IntervalSeconds
		}
		p.Orders = append(p.Orders, OrderSpec{Index: n + 1, Price: l.Price, Quantity: number(q), OffsetSeconds: offset})
		risk = add(risk, mul(q, loss))
		reward = add(reward, mul(q, gain))
		qty = add(qty, q)
	}
	p.Quantity = number(qty)
	p.Risk = number(risk)
	p.Reward = number(reward)
	p.RewardRisk = number(div(reward, risk))
	for _, d := range []Decimal{p.Quantity, p.Risk, p.Reward, p.RewardRisk} {
		if !Positive(d) {
			return Plan{}, errors.New("derived execution amount exceeds decimal limits")
		}
	}
	if s.ContractValue != "" {
		p.VenueQuantity = number(div(must(p.Quantity), must(s.ContractValue)))
		for n := range p.Orders {
			p.Orders[n].VenueQuantity = number(div(must(p.Orders[n].Quantity), must(s.ContractValue)))
		}
	}
	if c.SizingPolicy == SizingAccountMargin {
		if err := checkMarketDepth(p, s); err != nil {
			return Plan{}, err
		}
	}
	p.Hash = p.Digest()
	p.ID = p.Hash[:16]
	return p, nil
}
