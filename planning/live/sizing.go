package live

import (
	"errors"
	"math/big"
	"nofx/planning/core"
	"nofx/trader/execution"
)

// AccountLimits derives exact budgets from a fresh NOFX account snapshot.
// Use free margin at the selected leverage, reserving estimated execution costs.
// Existing exposure is not a reason to reject a draft. The controller checks
// positions and working orders again before approving or submitting an entry.
func AccountLimits(a Account, c core.Constraints) (core.Constraints, error) {
	if !core.Positive(a.Equity) || !core.Positive(a.Available) {
		return c, errors.New("No available margin for a new order. You can still generate plans; review an order again when trading funds are available.")
	}
	if c.Leverage < 1 || c.Leverage > 20 {
		return c, errors.New("select leverage between 1x and 20x")
	}
	equity, _ := decimal(a.Equity)
	available, _ := decimal(a.Available)
	if available.Cmp(equity) > 0 {
		available = new(big.Rat).Set(equity)
	}
	c.Equity = a.Equity
	c.PlanningOnly = false
	c.AvailableMargin = core.Decimal(available.FloatString(12))
	c.SizingPolicy = core.SizingAccountMargin
	// Auto sizing allocates margin, rather than silently applying a 1% loss budget.
	// The compiler still validates stop distance, reward/risk and market depth.
	c.MaxRisk = a.Equity
	if !core.Positive(a.FeeBPS) && a.FeeBPS != "0" {
		return c, errors.New("account fee estimate unavailable")
	}
	if cmp(a.FeeBPS, c.FeeBPS) > 0 {
		c.FeeBPS = a.FeeBPS
	}
	if !core.Positive(c.FeeBPS) || !core.Positive(c.SlippageBPS) {
		return c, errors.New("positive fee and slippage estimates are required")
	}
	fee, _ := decimal(c.FeeBPS)
	slip, _ := decimal(c.SlippageBPS)
	perUnit := new(big.Rat).Add(big.NewRat(1, int64(c.Leverage)), new(big.Rat).Quo(new(big.Rat).Add(fee, slip), big.NewRat(5000, 1)))
	c.MaxNotional = core.Decimal(new(big.Rat).Quo(available, perUnit).FloatString(12))
	return c, c.Validate()
}

// DraftAccountLimits permits market analysis when existing exposure uses all
// free margin. Equity is a reference budget, never spendable collateral. The
// preview flag and actual available margin are persisted in the plan hash.
func DraftAccountLimits(a Account, c core.Constraints) (core.Constraints, error) {
	available, err := execution.Number(string(a.Available))
	if err != nil || !core.Positive(a.Equity) {
		return c, errors.New("Could not verify account equity and available margin for planning. Refresh the account or explore without an account.")
	}
	if available.Sign() > 0 {
		return AccountLimits(a, c)
	}
	reference := a
	reference.Available = a.Equity
	limits, err := AccountLimits(reference, c)
	if err != nil {
		return c, err
	}
	limits.PlanningOnly = true
	limits.AvailableMargin = a.Available
	return limits, limits.Validate()
}

const EquityPreviewReviewMessage = "This is an equity-based preview. Refresh and review it with available margin before approval."

// Check actual locked orders, not unused planning budget or historical equity.
func CheckAccountCapacity(p core.Plan, a Account) error {
	if p.Constraints.PlanningOnly {
		return errors.New(EquityPreviewReviewMessage)
	}
	if !core.Positive(a.Equity) || !core.Positive(a.Available) {
		return errors.New("live account has no available margin")
	}
	total := new(big.Rat)
	specs := p.Orders
	if len(specs) == 0 {
		specs = []core.OrderSpec{{Price: p.Idea.Entry, Quantity: p.Quantity}}
	}
	for _, o := range specs {
		q, e := decimal(o.Quantity)
		if e != nil {
			return e
		}
		price, e := decimal(o.Price)
		if e != nil {
			return e
		}
		total.Add(total, new(big.Rat).Mul(q, price))
	}
	margin := new(big.Rat).Quo(total, big.NewRat(int64(p.Constraints.EffectiveLeverage()), 1))
	fee, _ := decimal(p.Constraints.FeeBPS)
	slip, _ := decimal(p.Constraints.SlippageBPS)
	costs := new(big.Rat).Mul(total, new(big.Rat).Quo(new(big.Rat).Add(fee, slip), big.NewRat(5000, 1)))
	available, _ := decimal(a.Available)
	if available.Cmp(new(big.Rat).Add(margin, costs)) < 0 || cmp(a.Equity, p.Risk) < 0 {
		return errors.New("available margin no longer covers the locked orders and cost reserve; regenerate with the current account balance")
	}
	return nil
}
