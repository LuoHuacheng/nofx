package backtest

import (
	"errors"
	"math/big"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

// ManagedCostInput is one closed managed trade. FeeBPS and SlippageBPS are the
// reserved planning rates, while Funding holds only venue-settled events: a
// reserved funding estimate is not a realised cost.
type ManagedCostInput struct {
	Side        string
	Quantity    execution.Decimal
	Entry       execution.Decimal
	Exit        execution.Decimal
	FeeBPS      execution.Decimal
	SlippageBPS execution.Decimal
	Funding     []execution.AccountEvent
}

// ManagedCostResult splits every cost so a report can show it. Net is
// gross - fees - slippage + funding (a negative funding event is a payment).
// Complete is false when a settled movement could not be attributed exactly.
type ManagedCostResult struct {
	Gross    execution.Decimal
	Fees     execution.Decimal
	Slippage execution.Decimal
	Funding  execution.Decimal
	Net      execution.Decimal
	Complete bool
}

// CalculateManagedCosts prices one closed trade exactly. All arithmetic is
// rational; nothing is rounded to a display precision.
func CalculateManagedCosts(in ManagedCostInput) (ManagedCostResult, error) {
	var result ManagedCostResult
	if in.Side != "long" && in.Side != "short" {
		return result, errors.New("managed cost: invalid side")
	}
	quantity, err := positiveRat(in.Quantity)
	if err != nil {
		return result, errors.New("managed cost: invalid quantity")
	}
	entry, err := positiveRat(in.Entry)
	if err != nil {
		return result, errors.New("managed cost: invalid entry")
	}
	exit, err := positiveRat(in.Exit)
	if err != nil {
		return result, errors.New("managed cost: invalid exit")
	}
	feeBPS, err := nonNegativeRat(in.FeeBPS)
	if err != nil {
		return result, errors.New("managed cost: invalid fee rate")
	}
	slipBPS, err := nonNegativeRat(in.SlippageBPS)
	if err != nil {
		return result, errors.New("managed cost: invalid slippage rate")
	}

	// Both legs are charged: the entry and the exit each pay the taker rate.
	turnover := new(big.Rat).Mul(quantity, new(big.Rat).Add(entry, exit))
	fees := new(big.Rat).Quo(new(big.Rat).Mul(turnover, feeBPS), big.NewRat(10_000, 1))
	slippage := new(big.Rat).Quo(new(big.Rat).Mul(turnover, slipBPS), big.NewRat(10_000, 1))

	gross := new(big.Rat).Sub(exit, entry)
	if in.Side == "short" {
		gross.Neg(gross)
	}
	gross.Mul(gross, quantity)

	funding, complete, err := fundingCost(in.Funding)
	if err != nil {
		return result, err
	}
	net := new(big.Rat).Add(gross, funding)
	net.Sub(net, fees)
	net.Sub(net, slippage)

	if result.Gross, err = execution.ExactDecimal(gross); err != nil {
		return result, err
	}
	if result.Fees, err = execution.ExactDecimal(fees); err != nil {
		return result, err
	}
	if result.Slippage, err = execution.ExactDecimal(slippage); err != nil {
		return result, err
	}
	if result.Funding, err = execution.ExactDecimal(funding); err != nil {
		return result, err
	}
	if result.Net, err = execution.ExactDecimal(net); err != nil {
		return result, err
	}
	result.Complete = complete
	return result, nil
}

// fundingCost sums the venue's settled funding. Events keep the venue's own
// timestamps and identifiers: there is no assumed eight-hour cycle, and a
// re-read of the same event never counts twice.
func fundingCost(events []execution.AccountEvent) (*big.Rat, bool, error) {
	total := new(big.Rat)
	complete := true
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		if event.ID == "" {
			// An event without the venue's identifier cannot be proven unique,
			// so counting it risks double counting on the next replay.
			complete = false
			continue
		}
		if event.Kind != risk.KindFunding {
			// A gap or any other movement still touched equity; this cost split
			// has no field for it, so the report must stay incomplete.
			complete = false
			continue
		}
		if event.Currency != "USDT" {
			// The venue may settle in another coin or leave the currency unknown.
			// Without a conversion rate the amount cannot be added to USDT
			// costs, and assuming USDT would understate or overstate the trade.
			complete = false
			continue
		}
		if _, duplicate := seen[event.ID]; duplicate {
			continue
		}
		seen[event.ID] = struct{}{}
		amount, err := execution.Number(string(event.Amount))
		if err != nil {
			return nil, false, errors.New("managed cost: invalid funding amount")
		}
		total.Add(total, amount)
	}
	return total, complete, nil
}

func positiveRat(d execution.Decimal) (*big.Rat, error) {
	r, err := execution.Number(string(d))
	if err != nil || r.Sign() <= 0 {
		return nil, errors.New("managed cost: not a positive number")
	}
	return r, nil
}

func nonNegativeRat(d execution.Decimal) (*big.Rat, error) {
	r, err := execution.Number(string(d))
	if err != nil || r.Sign() < 0 {
		return nil, errors.New("managed cost: not a non-negative number")
	}
	return r, nil
}

// ResolveManagedBarExit picks the price a bar would fill at. When one bar
// touches both levels the stop wins, because a backtest that resolves the
// ambiguity in favour of the target hides the losing branch. A gap through the
// stop fills at the open, which is worse than the stop price.
//
// An untouched bar returns the zero price and an empty reason.
func ResolveManagedBarExit(side string, stop, target, open, high, low execution.Decimal) (execution.Decimal, string, error) {
	if side != "long" && side != "short" {
		return "", "", errors.New("managed bar exit: invalid side")
	}
	stopRat, err := positiveRat(stop)
	if err != nil {
		return "", "", errors.New("managed bar exit: invalid stop")
	}
	targetRat, err := positiveRat(target)
	if err != nil {
		return "", "", errors.New("managed bar exit: invalid target")
	}
	openRat, err := positiveRat(open)
	if err != nil {
		return "", "", errors.New("managed bar exit: invalid open")
	}
	highRat, err := positiveRat(high)
	if err != nil {
		return "", "", errors.New("managed bar exit: invalid high")
	}
	lowRat, err := positiveRat(low)
	if err != nil {
		return "", "", errors.New("managed bar exit: invalid low")
	}
	if highRat.Cmp(lowRat) < 0 {
		return "", "", errors.New("managed bar exit: high is below low")
	}

	resolve := func(price *big.Rat, reason string) (execution.Decimal, string, error) {
		value, err := execution.ExactDecimal(price)
		return value, reason, err
	}
	// The stop is tested before the target on the same bar on purpose.
	if side == "long" {
		if openRat.Cmp(stopRat) <= 0 {
			return resolve(openRat, "stop_loss")
		}
		if lowRat.Cmp(stopRat) <= 0 {
			return resolve(stopRat, "stop_loss")
		}
		// A favourable gap is capped at the target: understating the win is the
		// conservative reading.
		if highRat.Cmp(targetRat) >= 0 {
			return resolve(targetRat, "take_profit")
		}
		return "0", "", nil
	}
	if openRat.Cmp(stopRat) >= 0 {
		return resolve(openRat, "stop_loss")
	}
	if highRat.Cmp(stopRat) >= 0 {
		return resolve(stopRat, "stop_loss")
	}
	if lowRat.Cmp(targetRat) <= 0 {
		return resolve(targetRat, "take_profit")
	}
	return "0", "", nil
}
