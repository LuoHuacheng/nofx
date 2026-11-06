package live

import (
	"errors"
	"math/big"
	"nofx/planning/core"
	"nofx/trader/execution"
	"nofx/trading/risk"
	"time"
)

// Cost assumptions for managed sizing. The managed account snapshot carries no
// fee estimate, so the design's conservative 5bps fee / 10bps slippage apply.
const (
	ManagedFeeBPS      core.Decimal = "5"
	ManagedSlippageBPS core.Decimal = "10"
)

// ManagedSnapshotMaxAge bounds how stale a managed account snapshot may be
// before it can size a new entry.
const ManagedSnapshotMaxAge = 5 * time.Second

// ManagedAccountLimits sizes a managed entry by the stop-loss risk budget from
// the policy, never by account margin. Manual planning keeps using AccountLimits.
func ManagedAccountLimits(policy risk.Policy, a execution.ManagedAccount, now time.Time) (core.Constraints, error) {
	if err := risk.ValidatePolicy(policy); err != nil {
		return core.Constraints{}, err
	}
	if a.AccountKey != policy.AccountKey {
		return core.Constraints{}, errors.New("managed account does not match the risk policy")
	}
	if a.Venue != policy.Venue || a.Environment != policy.Environment {
		return core.Constraints{}, errors.New("managed account venue or environment does not match the risk policy")
	}
	if a.ObservedAt.IsZero() || now.Sub(a.ObservedAt) > ManagedSnapshotMaxAge || a.ObservedAt.After(now.Add(time.Second)) {
		return core.Constraints{}, errors.New("managed account snapshot is stale; refresh before sizing")
	}
	equity, err := execution.Number(string(a.Equity))
	if err != nil || equity.Sign() <= 0 {
		return core.Constraints{}, errors.New("managed account equity is unavailable")
	}
	available, err := execution.Number(string(a.Available))
	if err != nil || available.Sign() <= 0 {
		return core.Constraints{}, errors.New("No available margin for a new order. You can still generate plans; review an order again when trading funds are available.")
	}
	if available.Cmp(equity) > 0 {
		available = new(big.Rat).Set(equity)
	}
	maxRisk := mulRat(equity, big.NewRat(int64(policy.PerTradeRiskBPS), 10000))
	maxNotional := mulRat(available, big.NewRat(int64(policy.MaxLeverage), 1))
	c := core.Constraints{
		Leverage:          policy.MaxLeverage,
		AvailableMargin:   core.Decimal(available.FloatString(12)),
		Equity:            core.Decimal(equity.FloatString(12)),
		MaxRisk:           core.Decimal(maxRisk.FloatString(12)),
		MaxNotional:       core.Decimal(maxNotional.FloatString(12)),
		MinRewardRisk:     policy.MinRewardRisk,
		FeeBPS:            ManagedFeeBPS,
		SlippageBPS:       ManagedSlippageBPS,
		FundingReserveBPS: core.Decimal(big.NewRat(int64(policy.FundingReserveBPS), 1).FloatString(0)),
	}
	if err := c.Validate(); err != nil {
		return core.Constraints{}, err
	}
	return c, nil
}

// VerifyManagedBudget re-checks the compiled plan against the policy budget so
// rounding or an edited plan cannot exceed the per-trade risk.
func VerifyManagedBudget(p core.Plan) error {
	if p.Constraints.SizingPolicy != "" {
		return errors.New("managed plan must not use account-margin sizing")
	}
	riskAmount, err := execution.Number(string(p.Risk))
	if err != nil || riskAmount.Sign() <= 0 {
		return errors.New("managed plan has no computed risk")
	}
	limit, err := execution.Number(string(p.Constraints.MaxRisk))
	if err != nil || limit.Sign() <= 0 {
		return errors.New("managed plan has no risk budget")
	}
	if riskAmount.Cmp(limit) > 0 {
		return errors.New("managed plan risk exceeds the per-trade budget")
	}
	quote := p.Snapshot.Ask
	if p.Idea.Side == "short" {
		quote = p.Snapshot.Bid
	}
	notional := mulRat(mustRat(p.Quantity), mustRat(quote))
	maxNotional, err := execution.Number(string(p.Constraints.MaxNotional))
	if err != nil || maxNotional.Sign() <= 0 {
		return errors.New("managed plan has no notional budget")
	}
	if notional.Cmp(maxNotional) > 0 {
		return errors.New("managed plan notional exceeds the policy budget")
	}
	return nil
}

func mustRat(d core.Decimal) *big.Rat {
	r, err := execution.Number(string(d))
	if err != nil {
		return new(big.Rat)
	}
	return r
}

func mulRat(a *big.Rat, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }
