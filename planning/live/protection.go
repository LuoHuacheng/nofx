package live

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"nofx/planning/core"
	"nofx/trader/execution"
)

// protectionGrace bounds how long a supervised position may stay without a
// confirmed stop after its exposure was observed. A venue read or submission
// that cannot finish inside it makes the position an exit candidate.
const protectionGrace = 5 * time.Second

// Run.ProtectionState values of supervised (bound) accounts.
const (
	protectionPending      = "pending"
	protectionProtected    = "protected"
	protectionUnknown      = "unknown"
	protectionExitRequired = "exit_required"
)

// now reads the injected clock so protection deadlines are testable.
func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// supervised reports whether the trading account of this run is an enabled
// managed binding. Revocation takes effect on the next tick. A binding read
// failure stays supervised: protection never degrades to the manual path.
func (c *Controller) supervised(ctx context.Context, v View) bool {
	if c.DB == nil || v.AccountKey == "" {
		return false
	}
	var b execution.Binding
	err := c.DB.WithContext(ctx).Where("id = ? AND enabled = ?", v.AccountKey, true).First(&b).Error
	return !errors.Is(err, gorm.ErrRecordNotFound)
}

// protectionFault handles unconfirmed protection. Manual runs need recovery at
// once; supervised runs stay running and report the fault through
// ProtectionState until the deadline decides, so no stale error is left behind.
func protectionFault(v *View, supervised bool, message string) {
	if supervised {
		return
	}
	v.Error = message
	v.State = "needs_attention"
}

// activeProtection reports a venue status that still protects the position.
func activeProtection(status string) bool {
	switch status {
	case "NEW", "PARTIALLY_FILLED", "FILLED", "TRIGGERED":
		return true
	}
	return false
}

// inspectProtection re-reads the protective orders of one filled entry.
// Supervised runs trust only a fresh venue read: neither an acknowledgement nor
// a stale order status can prove protection.
func (c *Controller) inspectProtection(ctx context.Context, v *View, b Broker, entry Order, supervised bool) (remaining, stop, target core.Decimal, stopUnreadable, targetUnreadable bool) {
	remaining, stop, target = entry.Filled, "0", "0"
	for i := range v.Orders {
		o := &v.Orders[i]
		if o.ParentID != entry.ClientID || (o.Role != "sl" && o.Role != "tp") {
			continue
		}
		if supervised {
			observed, err := b.Lookup(ctx, *o)
			if err != nil || !validReceipt(*o, observed) || (o.OrderID != "" && o.OrderID != observed.OrderID) || cmp(observed.Filled, o.Filled) < 0 {
				if o.Role == "sl" {
					stopUnreadable = true
				} else {
					targetUnreadable = true
				}
				continue
			}
			*o = observed
		}
		remaining = sub(remaining, o.Filled)
		unreadable := o.OrderID == "" || !activeProtection(o.Status)
		switch {
		case o.Role == "sl" && unreadable:
			stopUnreadable = true
		case o.Role == "sl":
			stop = add(stop, o.Quantity)
		case unreadable:
			targetUnreadable = true
		default:
			target = add(target, o.Quantity)
		}
	}
	return
}

// submitProtection submits the missing quantity of one exit role. It reports
// whether the venue acknowledged an order; only a later venue read confirms it.
// A nil error with placed=false means the failure was recorded on the view.
func (c *Controller) submitProtection(ctx context.Context, v *View, b Broker, index int, entry Order, role string, limit, confirmed core.Decimal, supervised bool) (placed bool, err error) {
	if len(v.Orders) >= 1024 {
		protectionFault(v, supervised, "Partial-fill count exceeds the handling limit; manage protection manually")
		return false, nil
	}
	client := cid(v.ID, index+1, role, string(limit))
	for _, o := range v.Orders {
		if o.ClientID == client {
			return false, nil // an unsettled attempt owns this client ID; never resubmit
		}
	}
	price := v.Plan.Idea.Stop
	if role == "tp" {
		price = v.Plan.Idea.Target
	}
	o := Order{ClientID: client, Role: role, Symbol: entry.Symbol, Side: entry.Side, Price: price, Quantity: sub(limit, confirmed), Status: "submitting", Filled: "0", ParentID: entry.ClientID}
	o, err = routedOrder(b, o)
	if err != nil {
		protectionFault(v, supervised, "Protective order route is unconfirmed; further entries stopped")
		return false, nil
	}
	v.Orders = append(v.Orders, o)
	if err = c.save(v); err != nil {
		return false, err
	}
	receipt, err := b.Place(ctx, o)
	if err == nil && !validReceipt(o, receipt) {
		err = errors.New("invalid protection acknowledgement")
	}
	if err != nil {
		v.Orders[len(v.Orders)-1].Status = "UNKNOWN"
		protectionFault(v, supervised, "Protection is unconfirmed. New entries stopped; reconcile positions and protective orders in NOFX immediately. "+err.Error())
		_ = c.save(v)
		return false, nil
	}
	receipt.ParentID = entry.ClientID
	v.Orders[len(v.Orders)-1] = receipt
	if receipt.Status == "TRIGGERED" || cmp(receipt.Filled, "0") > 0 {
		v.State = "stopping"
	}
	return true, c.save(v)
}

// ensureProtection reconciles every filled entry with protective orders and
// returns an error only when a durable write failed. Supervised accounts must
// confirm the stop from venue reads and escalate to exit_required once the
// protection deadline passes; manual accounts keep the original recovery path.
func (c *Controller) ensureProtection(ctx context.Context, v *View, b Broker, now time.Time) error {
	supervised := c.supervised(ctx, *v)
	if supervised && v.ProtectionState == protectionExitRequired {
		// Exit is frozen here; reducing the position belongs to the next stage.
		return nil
	}
	entries := len(v.Plan.Orders)
	if entries == 0 {
		entries = 1
	}
	account, readErr := b.Account(ctx, v.Plan.Snapshot.Symbol)
	_, positionErr := decimal(account.Position)
	if readErr != nil || positionErr != nil {
		// "Cannot read the position" is not "the stop is missing": no deadline
		// may be armed from it. step still forces needs_attention and stops
		// entries through the same unknown state.
		protectionFault(v, supervised, "Live position read failed. New entries and protection submission stopped; reconcile positions immediately.")
		v.ProtectionState = protectionUnknown
		return nil
	}
	expected := filledExposure(v.Orders)
	mismatch := ""
	switch {
	case cmp(expected, "0") < 0:
		mismatch = "Exit fills exceed confirmed entry fills; reconcile live positions."
	case cmp(account.Position, expected) != 0:
		mismatch = "Live position differs from this plan fills. Further entries stopped; check external trades and protective orders."
	}
	if mismatch != "" {
		protectionFault(v, supervised, mismatch)
	}
	protected, unknown := mismatch == "", mismatch != "" // a mismatch is never protection
	for i := 0; i < entries; i++ {
		entry := v.Orders[i]
		if mismatch != "" || cmp(entry.Filled, "0") <= 0 {
			continue
		}
		remaining, stop, target, stopUnreadable, targetUnreadable := c.inspectProtection(ctx, v, b, entry, supervised)
		limit := entry.Filled
		if supervised {
			limit = remaining // never protect quantity that confirmed exits already closed
		}
		reconcile := func(role string, confirmed core.Decimal, unreadable bool) (string, bool, error) {
			if unreadable {
				protectionFault(v, supervised, "Protective order state is abnormal. Further entries stopped; reconcile live positions manually.")
				return protectionUnknown, false, nil
			}
			if cmp(confirmed, limit) >= 0 {
				return protectionProtected, false, nil
			}
			placed, err := c.submitProtection(ctx, v, b, i, entry, role, limit, confirmed, supervised)
			if err != nil {
				return "", false, err
			}
			if !placed {
				return protectionUnknown, true, nil
			}
			return protectionPending, false, nil
		}
		stopState, blocked, err := reconcile("sl", stop, stopUnreadable)
		if err != nil {
			return err
		}
		if stopState != protectionProtected {
			protected = false
		}
		if stopState == protectionUnknown {
			unknown = true
		}
		if blocked {
			continue // the stop is unsettled; do not add another exit order this tick
		}
		if targetState, _, err := reconcile("tp", target, targetUnreadable); err != nil {
			return err
		} else if targetState == protectionUnknown {
			unknown = true
		}
	}
	if !supervised {
		if protected {
			v.ProtectionState = protectionProtected
		} else {
			v.ProtectionState = protectionUnknown
		}
		return nil
	}
	if protected {
		v.ProtectionObservedAt = nil
		v.ProtectionState = protectionProtected
		return nil
	}
	if v.ProtectionObservedAt == nil {
		t := now
		v.ProtectionObservedAt = &t
	}
	if unknown {
		v.ProtectionState = protectionUnknown
	} else {
		v.ProtectionState = protectionPending
	}
	if !now.Before(v.ProtectionObservedAt.Add(protectionGrace)) {
		v.ProtectionState = protectionExitRequired
		if v.State == "running" || v.State == "position_open" {
			v.State = "needs_attention"
		}
		message := "Protection deadline exceeded with an open position. Exit required; protection retries stopped and remaining entries were cancelled."
		if mismatch != "" {
			v.Error = mismatch + " " + message
		} else {
			v.Error = message
		}
	}
	return nil
}
