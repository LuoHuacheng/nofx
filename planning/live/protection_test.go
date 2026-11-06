package live

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"nofx/planning/core"
	"nofx/trader/execution"
)

// protectionVenue can reject or lose the acknowledgement of protection orders
// while every other trader call reaches the plain test broker.
type protectionVenue struct {
	testNOFXTrader
	rejectSL, rejectTP, ackLost bool
}

func (v *protectionVenue) SubmitOrder(ctx context.Context, o Order) (Order, error) {
	if (o.Role == "sl" && v.rejectSL) || (o.Role == "tp" && v.rejectTP) {
		return o, errors.New("venue rejected the protection")
	}
	if v.ackLost && (o.Role == "sl" || o.Role == "tp") {
		v.testNOFXTrader.SubmitOrder(ctx, o) // the venue accepted it; the reply was lost
		return o, errors.New("connection lost after submit")
	}
	return v.testNOFXTrader.SubmitOrder(ctx, o)
}

// supervisedSetup binds the run account to a managed binding and installs a
// controllable clock so protection deadlines never wait on the wall clock.
func supervisedSetup(t *testing.T, kind string) (*Controller, *testBroker, *protectionVenue, core.Plan, *time.Time) {
	t.Helper()
	c, b, _, p := setup(t, kind)
	venue := &protectionVenue{testNOFXTrader: testNOFXTrader{b}}
	c.Factory = func(context.Context, string, string) (Broker, string, error) {
		return NOFXBroker{Trader: venue}, "account", nil
	}
	binding := execution.Binding{ID: "account", UserID: "alice", ExchangeID: "exchange", Venue: "binance", Environment: execution.EnvironmentLive, VenueAccountID: "venue-account", VerifiedAt: time.Now().UTC(), Enabled: true}
	if e := c.DB.Create(&binding).Error; e != nil {
		t.Fatal(e)
	}
	clock := new(time.Time)
	*clock = time.Now().UTC()
	c.Now = func() time.Time { return *clock }
	return c, b, venue, p, clock
}

// entryFilled mirrors a venue fill of the first entry and the resulting position.
func entryFilled(t *testing.T, c *Controller, b *testBroker, runID, status, quantity, position string) {
	t.Helper()
	v, e := c.Get("alice", runID)
	if e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.orders[v.Orders[0].ClientID]
	o.Status, o.Filled = status, execution.Decimal(quantity)
	b.orders[o.ClientID] = o
	b.account.Position = execution.Decimal(position)
}

// submitted counts venue submission attempts of one role.
func submitted(b *testBroker, role string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, o := range b.placed {
		if o.Role == role {
			n++
		}
	}
	return n
}

func TestManagedProtectionDeadline(t *testing.T) {
	c, b, venue, p, clock := supervisedSetup(t, "limit")
	v := started(t, c, p)
	if c.step(context.Background(), "alice", v.ID) {
		t.Fatal("entry tick stopped the monitor")
	}
	entryFilled(t, c, b, v.ID, "FILLED", "0.00998", "0.00998")
	venue.rejectSL, venue.rejectTP = true, true
	if c.step(context.Background(), "alice", v.ID) {
		t.Fatal("fill tick stopped the monitor")
	}
	got, _ := c.Get("alice", v.ID)
	if got.ProtectionObservedAt == nil || !got.ProtectionObservedAt.Equal(*clock) || got.State != "running" || got.ProtectionState != protectionUnknown {
		t.Fatalf("unprotected fill was not recorded: %+v", got.Run)
	}
	if submitted(b, "sl") != 0 || submitted(b, "tp") != 0 {
		t.Fatal("rejected protection reached the venue")
	}
	*clock = clock.Add(protectionGrace - time.Millisecond)
	c.step(context.Background(), "alice", v.ID)
	if got, _ = c.Get("alice", v.ID); got.State != "running" || got.ProtectionState == protectionExitRequired {
		t.Fatalf("escalated before the protection deadline: %+v", got.Run)
	}
	*clock = clock.Add(time.Millisecond)
	c.step(context.Background(), "alice", v.ID)
	got, _ = c.Get("alice", v.ID)
	if got.State != "needs_attention" || got.ProtectionState != protectionExitRequired || !strings.Contains(got.Error, "Protection deadline") {
		t.Fatalf("unprotected position did not require an exit: %+v", got.Run)
	}
	*clock = clock.Add(time.Minute)
	c.step(context.Background(), "alice", v.ID)
	if again, _ := c.Get("alice", v.ID); again.State != "needs_attention" || again.ProtectionState != protectionExitRequired || submitted(b, "sl") != 0 {
		t.Fatalf("exit requirement was not frozen: %+v", again.Run)
	}
}

func TestManagedProtectionUnknownAck(t *testing.T) {
	c, b, venue, p, _ := supervisedSetup(t, "limit")
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID)
	entryFilled(t, c, b, v.ID, "FILLED", "0.00998", "0.00998")
	venue.ackLost = true
	c.step(context.Background(), "alice", v.ID)
	if submitted(b, "sl") != 1 {
		t.Fatalf("stop-loss was not submitted once: %d", submitted(b, "sl"))
	}
	if got, _ := c.Get("alice", v.ID); got.ProtectionState == protectionProtected || got.ProtectionObservedAt == nil {
		t.Fatalf("acknowledgement alone was taken as protection: %+v", got.Run)
	}
	venue.ackLost = false
	c.step(context.Background(), "alice", v.ID)
	got, _ := c.Get("alice", v.ID)
	if submitted(b, "sl") != 1 || submitted(b, "tp") != 1 {
		t.Fatalf("venue-confirmed stop was resubmitted: sl=%d tp=%d", submitted(b, "sl"), submitted(b, "tp"))
	}
	if got.ProtectionState != protectionProtected || got.ProtectionObservedAt != nil {
		t.Fatalf("venue-confirmed stop did not clear the deadline: %+v", got.Run)
	}
	c.step(context.Background(), "alice", v.ID)
	if submitted(b, "sl") != 1 || submitted(b, "tp") != 1 {
		t.Fatal("confirmed protection was resubmitted")
	}
}

func TestManagedPartialFillProtection(t *testing.T) {
	c, b, venue, p, _ := supervisedSetup(t, "twap")
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID)
	// Make the remaining TWAP slice due without waiting on the wall clock.
	if e := c.DB.Model(&Run{}).Where("id = ?", v.ID).Update("started_at", time.Now().UTC().Add(-35*time.Second)).Error; e != nil {
		t.Fatal(e)
	}
	entryFilled(t, c, b, v.ID, "PARTIALLY_FILLED", "0.001", "0.001")
	c.step(context.Background(), "alice", v.ID)
	if submitted(b, "entry") != 1 {
		t.Fatal("released a second entry before the stop was confirmed")
	}
	if len(b.placed) != 3 || b.placed[1].Quantity != "0.001000000000" || b.placed[2].Quantity != "0.001000000000" {
		t.Fatal(b.placed)
	}
	c.step(context.Background(), "alice", v.ID)
	if submitted(b, "entry") != 2 {
		t.Fatal("venue-confirmed stop did not release the remaining entry")
	}
	entryFilled(t, c, b, v.ID, "PARTIALLY_FILLED", "0.0025", "0.0025")
	venue.rejectTP = true
	c.step(context.Background(), "alice", v.ID)
	got, _ := c.Get("alice", v.ID)
	topUp := Order{}
	for _, o := range b.placed {
		if o.Role == "sl" {
			topUp = o
		}
	}
	if submitted(b, "sl") != 2 || topUp.Quantity != "0.001500000000" {
		t.Fatalf("increased fill was not topped up by the missing quantity: %v", b.placed)
	}
	if len(b.cancelled) != 0 || got.State != "running" {
		t.Fatalf("confirmed stop was lost with the failed take-profit: %+v", got.Run)
	}
}

func TestManagedProtectionDBFailure(t *testing.T) {
	c, b, _, p, _ := supervisedSetup(t, "limit")
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID)
	if e := c.DB.Callback().Update().Before("gorm:update").Register("fail_protection_save", func(tx *gorm.DB) {
		m, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		if raw, ok := m["orders_json"].(string); ok && strings.Contains(raw, `"role":"sl"`) {
			tx.AddError(errors.New("disk full"))
		}
	}); e != nil {
		t.Fatal(e)
	}
	entryFilled(t, c, b, v.ID, "FILLED", "0.00998", "0.00998")
	c.step(context.Background(), "alice", v.ID)
	if submitted(b, "sl") != 0 || submitted(b, "tp") != 0 {
		t.Fatal("submitted protection without a durable intent")
	}
	got, _ := c.Get("alice", v.ID)
	for _, o := range got.Orders {
		if o.Role != "entry" {
			t.Fatalf("unpersisted protection was recorded: %+v", o)
		}
	}
	c.DB.Callback().Update().Remove("fail_protection_save")
	c.step(context.Background(), "alice", v.ID)
	if submitted(b, "sl") != 1 || submitted(b, "tp") != 1 {
		t.Fatal("protection was not submitted after the write recovered")
	}
}
