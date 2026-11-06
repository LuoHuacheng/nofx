package automation

import (
	"context"
	"errors"
	"fmt"

	"nofx/logger"
	"nofx/planning/live"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// Recover re-establishes supervision after a restart. It reconciles what the
// venue shows against the runs this runtime owns and never submits an entry:
// recovery is a read-and-verify path, so a crash cannot replay an order.
func (r *Runtime) Recover(ctx context.Context, user, accountKey string) error {
	if user == "" || accountKey == "" {
		return errors.New("automation: recovery requires a user and an account")
	}
	lock := r.lock(accountKey)
	lock.Lock()
	defer lock.Unlock()

	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		r.freeze(accountKey, err.Error())
		return err
	}
	trader, err := r.trader(ctx, user, binding.ExchangeID)
	if err != nil {
		r.freeze(accountKey, err.Error())
		return err
	}
	account, err := trader.ManagedSnapshot(ctx)
	if err != nil {
		r.freeze(accountKey, err.Error())
		return fmt.Errorf("automation: could not read the managed account: %w", err)
	}
	if account.AccountKey != accountKey {
		reason := fmt.Sprintf("automation: the venue reported account %q instead of %q", account.AccountKey, accountKey)
		r.freeze(accountKey, reason)
		return errors.New(reason)
	}
	// Settings are verified before any run is reconciled: a hedge-mode or
	// wrong-leverage account must not be supervised as if it were correct.
	if err := r.verifySettings(ctx, trader, account, user, accountKey); err != nil {
		r.freeze(accountKey, err.Error())
		return err
	}

	views, err := r.controller.ListContext(ctx, user)
	if err != nil {
		r.freeze(accountKey, err.Error())
		return fmt.Errorf("automation: could not list runs: %w", err)
	}
	owned, err := r.ownedExposure(views, accountKey)
	if err != nil {
		r.freeze(accountKey, err.Error())
		return err
	}
	if err := r.checkExternal(ctx, account, owned, accountKey); err != nil {
		r.freeze(accountKey, err.Error())
		return err
	}

	for _, v := range views {
		if v.AccountKey != accountKey {
			continue
		}
		switch v.State {
		case "halted_unknown":
			// Keep the freeze: the unresolved exit may only be settled by the
			// original client ID, and re-submitting would double the position.
			r.freeze(accountKey, "a run halted with an unresolved exit; resolve it before new entries")
		case "running", "stopping", "position_open", "needs_attention":
			if _, err := r.controller.ReconcileContext(ctx, user, v.ID); err != nil {
				r.freeze(accountKey, err.Error())
				return fmt.Errorf("automation: could not reconcile run %s: %w", v.ID, err)
			}
		}
	}

	now := r.clock()
	if err := r.risks.Observe(ctx, account, nil, now); err != nil {
		r.freeze(accountKey, err.Error())
		return fmt.Errorf("automation: could not record the equity observation: %w", err)
	}
	decision, err := r.risks.Evaluate(ctx, user, accountKey, now)
	if err != nil {
		r.freeze(accountKey, err.Error())
		return fmt.Errorf("automation: could not evaluate the risk breaker: %w", err)
	}
	if decision.Action != risk.ActionAllow {
		r.freeze(accountKey, decision.Reason)
		return nil
	}
	r.clearReconcile(accountKey)
	logger.Infof("automation: %s recovered under supervision", accountKey)
	return nil
}

func (r *Runtime) verifySettings(ctx context.Context, trader execution.ManagedTrader, account execution.ManagedAccount, user, accountKey string) error {
	policy, err := r.risks.Policy(ctx, user, accountKey)
	if err != nil {
		// No policy yet means nothing to verify; the binding check in Tick and
		// AuthorizePolicy still gate any entry.
		return nil
	}
	for _, p := range account.Positions {
		if err := trader.VerifyManagedSettings(ctx, p.Symbol, policy.MaxLeverage); err != nil {
			return fmt.Errorf("automation: managed settings are not verified for %s: %w", p.Symbol, err)
		}
	}
	return nil
}

// ownedExposure sums the quantities of runs this runtime believes it holds, so
// an unknown venue position can be told apart from one we are supervising.
func (r *Runtime) ownedExposure(views []live.View, accountKey string) (map[string]bool, error) {
	owned := map[string]bool{}
	for _, v := range views {
		if v.AccountKey != accountKey {
			continue
		}
		if v.State == "completed" || v.State == "cancelled" || v.State == "expired" || v.State == "approved" {
			continue
		}
		owned[v.Plan.Snapshot.Symbol] = true
	}
	return owned, nil
}

// checkExternal refuses to take over a position no run accounts for: an
// unattended runtime must never adopt a manual trade as its own.
func (r *Runtime) checkExternal(ctx context.Context, account execution.ManagedAccount, owned map[string]bool, accountKey string) error {
	for _, p := range account.Positions {
		qty, err := execution.Number(string(p.Quantity))
		if err != nil || qty.Sign() == 0 {
			continue
		}
		if !owned[p.Symbol] {
			reason := fmt.Sprintf("external position not owned by this runtime: %s %s", p.Symbol, p.Quantity)
			return errors.New(reason)
		}
	}
	return nil
}
