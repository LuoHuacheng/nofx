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
	marginDec, err := execution.ExactDecimal(margin)
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
