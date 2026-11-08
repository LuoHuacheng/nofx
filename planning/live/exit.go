package live

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nofx/planning/core"
	"nofx/trader/execution"
)

// runHaltedUnknown is a non-terminal run state that keeps the account lease.
// An exit whose receipt could not be resolved is never reported as done and no
// other submission is authorized until the original client ID resolves it.
const runHaltedUnknown = "halted_unknown"

// managedBroker is the optional managed surface of a Broker. A trader without
// it keeps the finite scheduler's manual behaviour instead of a silent fallback.
type managedBroker interface {
	Broker
	Managed() (execution.ManagedTrader, bool)
}

func managedTrader(b Broker) (execution.ManagedTrader, bool) {
	m, ok := b.(managedBroker)
	if !ok {
		return nil, false
	}
	return m.Managed()
}

// closeOrderTerminal reports a venue status that no longer changes.
func closeOrderTerminal(status string) bool {
	switch status {
	case "FILLED", "CANCELED", "REJECTED", "EXPIRED":
		return true
	}
	return false
}

// exitFilled sums confirmed closing fills of this run.
func exitFilled(orders []Order) core.Decimal {
	closed := core.Decimal("0")
	for _, o := range orders {
		if o.Role == "exit" {
			closed = add(closed, o.Filled)
		}
	}
	return closed
}

// exitAllocation attributes confirmed closing fills to entries in submission
// order. A multi-entry plan then keeps every protector within the live size
// instead of over-protecting the same closed quantity once per entry.
func exitAllocation(orders []Order, entry Order) core.Decimal {
	fills := exitFilled(orders)
	for _, o := range orders {
		if o.Role != "entry" {
			continue
		}
		if o.ClientID == entry.ClientID {
			break
		}
		if cmp(fills, o.Filled) <= 0 {
			return "0"
		}
		fills = sub(fills, o.Filled)
	}
	if cmp(fills, entry.Filled) > 0 {
		return entry.Filled
	}
	return fills
}

// Flatten reduces this run's live position with one reduce-only market exit.
// The intent is persisted before submission, an earlier receipt is resolved by
// its own client ID instead of resubmitted, and the run only reports done after
// the venue confirms flatness with this run's protection cancelled.
func (c *Controller) Flatten(ctx context.Context, user, runID, reason string) (View, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock := c.runLock(runID)
	if err := lock.LockContext(ctx); err != nil {
		return View{}, err
	}
	defer lock.Unlock()
	return c.flatten(ctx, user, runID, reason)
}

// flatten assumes the caller holds the run lock: MoveStop reduces through it
// without re-entering the non-reentrant mutex.
func (c *Controller) flatten(ctx context.Context, user, runID, reason string) (v View, err error) {
	v, err = c.Get(user, runID)
	if err != nil {
		return v, err
	}
	if v.State == "completed" || v.State == "cancelled" {
		return v, nil
	}
	if v.State == "approved" || v.State == "" {
		return v, errors.New("This trade has no live position to reduce")
	}
	broker, key, err := c.Factory(ctx, user, v.ExchangeID)
	if err != nil || key != v.AccountKey || broker.Venue() != v.Plan.Snapshot.Venue {
		return v, errors.New("Account unavailable or identity changed. No exit was sent; check the account in Config.")
	}
	symbol := v.Plan.Snapshot.Symbol
	pendingExit := false
	for i := range v.Orders {
		o := &v.Orders[i]
		if o.Role != "exit" || closeOrderTerminal(o.Status) {
			continue
		}
		observed, err := broker.Lookup(ctx, *o)
		if err != nil || !validReceipt(*o, observed) || (o.OrderID != "" && o.OrderID != observed.OrderID) || cmp(observed.Filled, o.Filled) < 0 {
			return c.halt(&v, "An earlier exit is still unconfirmed. Check exchange order history; it will not be sent again.")
		}
		*o = observed
		pendingExit = !closeOrderTerminal(observed.Status)
	}
	if err = c.save(&v); err != nil {
		return v, err
	}
	// A resting entry can fill after this call, and a halted run has no
	// supervisor left to cancel it: entries are always cancelled first, flat or
	// not, before any exit decision is taken.
	entries := len(v.Plan.Orders)
	if entries == 0 {
		entries = 1
	}
	for i := 0; i < entries; i++ {
		o := &v.Orders[i]
		if o.Status == "scheduled" {
			o.Status = "CANCELED"
			continue
		}
		if closeOrderTerminal(o.Status) {
			continue
		}
		observed, er := broker.Lookup(ctx, *o)
		if er != nil || !validReceipt(*o, observed) || (o.OrderID != "" && o.OrderID != observed.OrderID) {
			return c.halt(&v, "A live entry could not be confirmed before the exit. No exit was sent; check exchange orders.")
		}
		*o = observed
		switch observed.Status {
		case "NEW", "PARTIALLY_FILLED":
			if er = broker.Cancel(ctx, observed); er != nil {
				return c.halt(&v, "An entry cancellation is unconfirmed before the exit. No exit was sent; check exchange orders.")
			}
			o.Status = "CANCELED"
		default:
			if !closeOrderTerminal(observed.Status) {
				return c.halt(&v, "An entry is still open at the exchange. No exit was sent; reconcile it first.")
			}
		}
	}
	if err = c.save(&v); err != nil {
		return v, err
	}
	if pendingExit {
		return c.halt(&v, "An earlier exit is still open at the exchange. Reconcile it before another exit is sent; protection orders are still live.")
	}
	position, err := readLivePosition(ctx, broker, symbol)
	if err != nil {
		return v, fmt.Errorf("read position after cancelling entries: %w", err)
	}
	if cmp(position, "0") > 0 {
		if c.isStopped() {
			return v, errors.New("Service is shutting down; no exit was sent")
		}
		// The venue position may hold size this run never opened (a manual trade,
		// another hedge side, an over-filled basket); only the run's remaining
		// exposure may be closed, and it must still be non-zero.
		exposure := filledExposure(v.Orders)
		if cmp(exposure, "0") <= 0 {
			return c.halt(&v, "The live position is not this run's remaining exposure. Check the exchange before this account is reused.")
		}
		quantity := position
		if cmp(quantity, exposure) > 0 {
			quantity = exposure
		}
		generation := 0
		for _, o := range v.Orders {
			if o.Role == "exit" {
				generation++
			}
		}
		// The position side keeps the exit direction of this run's protectors.
		exit := Order{ClientID: cid(v.ID, generation+1, "exit", ""), Role: "exit", Type: "market", ReduceOnly: true, Symbol: symbol, Side: v.Plan.Idea.Side, Price: v.Plan.Idea.Entry, Quantity: quantity, Status: "submitting", Filled: "0", Error: reason}
		if exit, err = routedOrder(broker, exit); err != nil {
			return v, errors.New("Exit route is unconfirmed; no exit was sent")
		}
		v.Orders = append(v.Orders, exit)
		if err = c.save(&v); err != nil {
			return v, err // the intent is not durable, so nothing may be sent
		}
		index := len(v.Orders) - 1
		receipt, er := broker.Place(ctx, v.Orders[index])
		if er == nil && !validReceipt(v.Orders[index], receipt) {
			er = errors.New("invalid exit acknowledgement")
		}
		if er != nil {
			v.Orders[index].Status = "UNKNOWN"
			return c.halt(&v, "Exit receipt is unconfirmed; no further exit will be sent. "+er.Error())
		}
		receipt.Error = reason
		v.Orders[index] = receipt
		v.State = "stopping"
		if err = c.save(&v); err != nil {
			return v, err
		}
		// A market exit is often acknowledged before it fills. Record the venue
		// fill so the closed trade can be proven instead of halting on the ack.
		if observed, er := broker.Lookup(ctx, v.Orders[index]); er == nil && validReceipt(v.Orders[index], observed) {
			observed.Error = reason
			v.Orders[index] = observed
			if err = c.save(&v); err != nil {
				return v, err
			}
		}
		if position, err = readLivePosition(ctx, broker, symbol); err != nil {
			return v, errors.New("Exit was sent, but the resulting position is unconfirmed. The account stays reserved; check the exchange.")
		}
	}
	if cmp(position, "0") != 0 {
		return v, errors.New("Exit is sent, but the position is not confirmed flat yet. The account stays reserved; check the exchange before closing again.")
	}
	for i := range v.Orders {
		o := &v.Orders[i]
		if o.Role != "sl" && o.Role != "tp" || o.Status == "scheduled" || closeOrderTerminal(o.Status) {
			continue
		}
		observed, er := broker.Lookup(ctx, *o)
		if er != nil || !validReceipt(*o, observed) || (o.OrderID != "" && o.OrderID != observed.OrderID) {
			return c.halt(&v, "A protective order is unconfirmed after the exit. Check exchange orders before this account is reused.")
		}
		*o = observed
		if observed.Status == "NEW" || observed.Status == "PARTIALLY_FILLED" {
			if er = broker.Cancel(ctx, observed); er != nil {
				return c.halt(&v, "Protective order cancellation is unconfirmed. Check exchange orders before this account is reused.")
			}
			o.Status = "CANCELED"
		}
	}
	if err = c.save(&v); err != nil {
		return v, err
	}
	account, er := broker.Account(ctx, symbol)
	if _, parseErr := decimal(account.Position); er != nil || parseErr != nil {
		return c.halt(&v, "The closed position could not be re-confirmed. Check the exchange before this account is reused.")
	}
	if !confirmedClosedTrade(v.Orders, account) {
		return c.halt(&v, "This trade still has a position or order that is not fully reconciled. Check the exchange before this account is reused.")
	}
	return v, c.complete(&v)
}

// halt freezes the run on a durable write: the lease is kept and any later
// attempt reconciles the same exit client ID before it can submit again.
func (c *Controller) halt(v *View, message string) (View, error) {
	v.State = runHaltedUnknown
	v.Error = message
	if err := c.save(v); err != nil {
		return *v, err
	}
	return *v, errors.New(message)
}

func readLivePosition(ctx context.Context, b Broker, symbol string) (execution.Decimal, error) {
	account, err := b.Account(ctx, symbol)
	if err != nil {
		return "", errors.New("Live position read failed. No exit is sent on an unknown position; check the exchange.")
	}
	if _, err = decimal(account.Position); err != nil {
		return "", errors.New("Invalid venue position field. No exit is sent on an unknown position; check the exchange.")
	}
	return account.Position, nil
}

// MoveStop only tightens an existing stop and never widens or crosses the mark.
// It refuses (leaving the original stop untouched at the venue) when the venue
// cannot amend in place or the current protection cannot be re-confirmed, so a
// cancel-then-place fallback can never leave the position unprotected.
func (c *Controller) MoveStop(ctx context.Context, user, runID string, price execution.Decimal) (View, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock := c.runLock(runID)
	if err := lock.LockContext(ctx); err != nil {
		return View{}, err
	}
	defer lock.Unlock()
	v, err := c.Get(user, runID)
	if err != nil {
		return v, err
	}
	if v.State == "completed" || v.State == "cancelled" || v.State == "approved" {
		return v, errors.New("This trade has no open position to protect")
	}
	if v.State == runHaltedUnknown {
		return v, errors.New("An exit receipt is still unknown. Resolve it before moving the stop.")
	}
	broker, key, err := c.Factory(ctx, user, v.ExchangeID)
	if err != nil || key != v.AccountKey || broker.Venue() != v.Plan.Snapshot.Venue {
		return v, errors.New("Could not verify the original trading account. Check its connection in Config.")
	}
	managed, ok := managedTrader(broker)
	if !ok || !managed.ManagedCapabilities().SafeStopAmend {
		return v, execution.ErrUnsupported
	}
	if !execution.Positive(price) {
		return v, errors.New("Enter a positive stop price")
	}
	snapshot, err := managed.ManagedSnapshot(ctx)
	if err != nil {
		return v, errors.New("Could not read the live position. The stop was not changed.")
	}
	mark := execution.Decimal("")
	live := false
	for _, p := range snapshot.Positions {
		if p.Symbol == v.Plan.Snapshot.Symbol && execution.Positive(p.Quantity) {
			mark, live = p.Mark, true
			break
		}
	}
	if !live {
		return v, errors.New("This trade has no live position to protect")
	}
	if !execution.Positive(mark) {
		return v, errors.New("The live mark price is unreadable. The stop was not changed.")
	}
	long := v.Plan.Idea.Side == "long"
	if long && cmp(price, mark) >= 0 || !long && cmp(price, mark) <= 0 {
		// A stop at or through the mark is a close, not a protection change.
		return c.flatten(ctx, user, runID, "Stop price crossed the current mark")
	}
	targets := []int{}
	for i := range v.Orders {
		o := &v.Orders[i]
		if o.Role != "sl" || closeOrderTerminal(o.Status) {
			continue
		}
		observed, er := broker.Lookup(ctx, *o)
		if er != nil || !validReceipt(*o, observed) || (o.OrderID != "" && o.OrderID != observed.OrderID) || observed.Status != "NEW" {
			return v, errors.New("The existing stop could not be confirmed. It was left in place and no stop was moved.")
		}
		*o = observed
		if long && cmp(price, o.Price) <= 0 || !long && cmp(price, o.Price) >= 0 {
			return v, errors.New("A stop can only move closer to the current mark. The existing stop was left in place.")
		}
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return v, errors.New("No confirmed stop exists to move. Protect the position first.")
	}
	for _, i := range targets {
		o := &v.Orders[i]
		expected := *o
		expected.Price = price
		amended, er := managed.AmendStop(ctx, *o, price)
		if er != nil {
			return v, exitAmendError(er)
		}
		if !validReceipt(expected, amended) || amended.Status != "NEW" {
			return v, errors.New("The venue did not confirm the amended stop. Check the protective order before relying on it.")
		}
		*o = amended
		if er = c.save(&v); er != nil {
			return v, er
		}
	}
	return v, nil
}

// exitAmendError keeps the refusal explicit: an unsupported in-place amend is
// reported as unsupported, never retried as a cancel-then-place sequence.
func exitAmendError(err error) error {
	if errors.Is(err, execution.ErrUnsupported) {
		return err
	}
	return errors.New("The stop was not amended: " + err.Error())
}
