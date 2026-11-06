package live

import (
	"context"
	"errors"
	"nofx/trader/execution"
)

// NOFXBroker adapts the finite scheduler to NOFX's shared exchange trader.
// It owns no credentials, exchange SDK, order conversion or submission logic.
type NOFXBroker struct{ execution.Trader }

func (b NOFXBroker) Account(ctx context.Context, s string) (Account, error) {
	return b.AccountSnapshot(ctx, s)
}
func (b NOFXBroker) Prepare(ctx context.Context, s string) error        { return b.PrepareExecution(ctx, s) }
func (b NOFXBroker) Place(ctx context.Context, o Order) (Order, error)  { return b.SubmitOrder(ctx, o) }
func (b NOFXBroker) Lookup(ctx context.Context, o Order) (Order, error) { return b.LookupOrder(ctx, o) }
func (b NOFXBroker) Cancel(ctx context.Context, o Order) error          { return b.CancelExecutionOrder(ctx, o) }
func (b NOFXBroker) RouteClientID(id string) (string, error) {
	if r, ok := b.Trader.(ClientIDRouter); ok {
		return r.RouteClientID(id)
	}
	return "", nil
}

var _ Broker = NOFXBroker{}

// Managed exposes the optional managed-account surface. A trader without it
// keeps the finite scheduler's manual behaviour instead of a silent fallback.
func (b NOFXBroker) Managed() (execution.ManagedTrader, bool) {
	m, ok := b.Trader.(execution.ManagedTrader)
	return m, ok
}

func (b NOFXBroker) PrepareLeverage(ctx context.Context, symbol string, leverage int) error {
	if t, ok := b.Trader.(interface {
		PrepareExecutionLeverage(context.Context, string, int) error
	}); ok {
		return t.PrepareExecutionLeverage(ctx, symbol, leverage)
	}
	if leverage == 1 {
		return b.Prepare(ctx, symbol)
	}
	return errors.New("account does not support the approved leverage")
}
