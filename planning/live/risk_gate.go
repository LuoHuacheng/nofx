package live

import (
	"context"
	"errors"
	"math/big"
	"time"

	"gorm.io/gorm"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// policySnapshot builds the reservation for the whole run from the approval
// that was stored on the Run; the caller never supplies policy identity.
func (c *Controller) policySnapshot(v *View) (risk.Reservation, error) {
	price := v.Plan.Idea.Entry
	if len(v.Plan.Orders) > 0 {
		price = v.Plan.Orders[0].Price
	}
	quantity, err := decimal(v.Plan.Quantity)
	if err != nil {
		return risk.Reservation{}, err
	}
	px, err := decimal(price)
	if err != nil {
		return risk.Reservation{}, err
	}
	notional := mulRat(quantity, px)
	margin := new(big.Rat).Quo(notional, big.NewRat(int64(v.Plan.Constraints.EffectiveLeverage()), 1))
	notionalDec, err := execution.ExactDecimal(notional)
	if err != nil {
		return risk.Reservation{}, err
	}
	// Margin is a requirement, not a venue balance: rounding it up to the
	// execution precision keeps the hold conservative and keeps 1/3, 1/5 etc.
	// representable, which plain ExactDecimal rejects.
	marginDec, err := ceilExact(margin)
	if err != nil {
		return risk.Reservation{}, err
	}
	return risk.Reservation{
		RunID:         v.ID,
		UserID:        v.UserID,
		AccountKey:    v.AccountKey,
		PolicyID:      v.PolicyID,
		PolicyVersion: v.PolicyVersion,
		PortfolioID:   v.PortfolioID,
		Symbol:        v.Plan.Snapshot.Symbol,
		Side:          v.Plan.Idea.Side,
		Risk:          v.Plan.Risk,
		Notional:      notionalDec,
		Margin:        marginDec,
		State:         risk.StateReserved,
	}, nil
}

// reserveRisk commits the whole-run reservation; it must commit before any
// order is submitted, so callers pass the transaction that starts the run.
func (c *Controller) reserveRisk(tx *gorm.DB, v *View, now time.Time) error {
	if !v.Managed {
		return nil
	}
	if c.Risk == nil {
		return errors.New("managed run requires the risk store")
	}
	res, err := c.policySnapshot(v)
	if err != nil {
		return err
	}
	// The tx carries its own context; callers only have the transaction here.
	return c.Risk.ReserveTx(context.Background(), tx, res, now)
}

func (c *Controller) exposeRisk(ctx context.Context, v *View) error {
	if !v.Managed {
		return nil
	}
	if c.Risk == nil {
		return errors.New("managed run requires the risk store")
	}
	return c.Risk.ExposeTx(ctx, c.DB, v.ID)
}

// releaseRisk only runs from transactions that prove the run is terminal.
func (c *Controller) releaseRisk(tx *gorm.DB, v *View) error {
	if !v.Managed || c.Risk == nil {
		return nil
	}
	return c.Risk.ReleaseTx(context.Background(), tx, v.ID)
}

// riskDecision returns the persisted hard-breaker verdict of this run's
// account. A manual run without a risk store keeps the unreserved behaviour.
func (c *Controller) riskDecision(ctx context.Context, v *View) (risk.Decision, error) {
	if c.Risk == nil {
		if v.Managed {
			return risk.Decision{}, errors.New("managed run requires the risk store")
		}
		return risk.Decision{Action: risk.ActionAllow}, nil
	}
	return c.Risk.Evaluate(ctx, v.UserID, v.AccountKey, c.now())
}

// applyRisk marks the run as needing attention when the breaker blocks new
// entries. It only mutates the View: the caller owns the durable write, because
// Execute must persist the failure and release the hold in one transaction.
func (c *Controller) applyRisk(v *View, decision risk.Decision) error {
	if decision.Action == risk.ActionAllow {
		return nil
	}
	v.State, v.Error = "needs_attention", decision.Reason
	return errors.New(decision.Reason)
}

// checkRisk blocks new entries on a pause/flatten latch. It is called only
// before the first submit, so a failure leaves no exposure and the caller
// releases the hold.
func (c *Controller) checkRisk(ctx context.Context, v *View) error {
	decision, err := c.riskDecision(ctx, v)
	if err != nil {
		return err
	}
	return c.applyRisk(v, decision)
}

// checkPolicy re-reads the automation policy immediately before any submission,
// so a disabled policy or a version change stops orders that were already planned.
func (c *Controller) checkPolicy(ctx context.Context, v *View) error {
	if !v.Managed || v.AuthorizationMode != "policy" {
		return nil
	}
	if c.Risk == nil {
		return errors.New("managed account requires the risk store")
	}
	p, err := c.Risk.Policy(ctx, v.UserID, v.AccountKey)
	if err != nil {
		return err
	}
	if !p.Enabled {
		return errors.New("automation policy is disabled")
	}
	if p.Version != v.PolicyVersion {
		return errors.New("automation policy version changed; re-authorize")
	}
	return nil
}

// ceilExact rounds a requirement up to 12 decimals. A hold that rounds up is
// conservative; rounding down would let the venue margin exceed the reservation.
func ceilExact(r *big.Rat) (execution.Decimal, error) {
	units := new(big.Rat).Mul(r, new(big.Rat).SetInt64(1e12))
	num, den := units.Num(), units.Denom()
	q, m := new(big.Int).QuoRem(num, den, new(big.Int))
	if m.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return execution.ExactDecimal(new(big.Rat).SetFrac(q, big.NewInt(1e12)))
}
