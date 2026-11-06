package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nofx/planning/core"
	"nofx/trader/execution"

	"gorm.io/gorm"
)

// managedTestVenue is a reduce-only venue: a market exit can only ever shrink
// the position, never flip it, and it may lose the acknowledgement of a close it
// already executed.
type managedTestVenue struct {
	testNOFXTrader
	capabilities execution.Capabilities
	mark         execution.Decimal
	accountErr   error
	amendErr     error
	exitAckLost  bool
	cancelEntErr error
	beforeExit   func()
	amended      []execution.Order
}

func (v *managedTestVenue) RouteClientID(id string) (string, error) { return id, nil }

// CancelExecutionOrder can fail for an entry so cancellation stays unconfirmed.
func (v *managedTestVenue) CancelExecutionOrder(ctx context.Context, o Order) error {
	if v.cancelEntErr != nil && o.Role == "entry" {
		return v.cancelEntErr
	}
	return v.testNOFXTrader.CancelExecutionOrder(ctx, o)
}

func (v *managedTestVenue) PrepareExecutionLeverage(context.Context, string, int) error {
	return nil
}

func (v *managedTestVenue) VerifyManagedSettings(context.Context, string, int) error { return nil }

func (v *managedTestVenue) ManagedCapabilities() execution.Capabilities { return v.capabilities }

func (v *managedTestVenue) ManagedSnapshot(context.Context) (execution.ManagedAccount, error) {
	b := v.testNOFXTrader.testBroker
	b.mu.Lock()
	quantity := b.account.Position
	b.mu.Unlock()
	return execution.ManagedAccount{Positions: []execution.ManagedPosition{{
		Symbol: "BTCUSDT", Side: "long", Quantity: quantity, Entry: "100000", Mark: v.mark,
	}}}, nil
}

func (v *managedTestVenue) AmendStop(_ context.Context, o execution.Order, price execution.Decimal) (execution.Order, error) {
	if v.amendErr != nil {
		return o, v.amendErr
	}
	v.amended = append(v.amended, o)
	b := v.testNOFXTrader.testBroker
	b.mu.Lock()
	amended := b.orders[o.ClientID]
	amended.Price = price
	b.orders[o.ClientID] = amended
	b.mu.Unlock()
	return amended, nil
}

func (v *managedTestVenue) Events(context.Context, func(execution.AccountEvent) error) error {
	return execution.ErrUnsupported
}

func (v *managedTestVenue) AccountSnapshot(ctx context.Context, s string) (Account, error) {
	if v.accountErr != nil {
		return Account{}, v.accountErr
	}
	return v.testNOFXTrader.AccountSnapshot(ctx, s)
}

func (v *managedTestVenue) SubmitOrder(ctx context.Context, o Order) (Order, error) {
	if o.Role != "exit" {
		return v.testNOFXTrader.SubmitOrder(ctx, o)
	}
	if v.beforeExit != nil {
		v.beforeExit()
	}
	b := v.testNOFXTrader.testBroker
	b.mu.Lock()
	b.placed = append(b.placed, o)
	available := b.account.Position
	if cmp(available, "0") < 0 {
		available = "0"
	}
	filled := o.Quantity
	if cmp(filled, available) > 0 {
		filled = available
	}
	b.account.Position = sub(available, filled)
	o.OrderID = fmt.Sprint(len(b.placed))
	o.Filled, o.Status = filled, "FILLED"
	if cmp(filled, o.Quantity) < 0 {
		o.Status = "PARTIALLY_FILLED"
	}
	if cmp(filled, "0") == 0 {
		o.Status = "REJECTED"
	}
	b.orders[o.ClientID] = o
	b.mu.Unlock()
	if v.exitAckLost {
		return o, errors.New("connection lost after the exit was accepted")
	}
	return o, nil
}

// managedSetup binds the run account to a managed venue so protection is
// supervised and the exit/amend surface is reachable through the broker.
func managedSetup(t *testing.T, kind string) (*Controller, *testBroker, *managedTestVenue, core.Plan, *time.Time) {
	t.Helper()
	c, b, _, p := setup(t, kind)
	venue := &managedTestVenue{
		testNOFXTrader: testNOFXTrader{b},
		capabilities:   execution.Capabilities{SafeStopAmend: true},
		mark:           "101000",
	}
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
	managedRisk(t, c, "binance", "10000")
	return c, b, venue, p, clock
}

// managedProtected runs one filled entry through protection until the venue
// confirms a stop and a target for the live size.
func managedProtected(t *testing.T, kind string) (*Controller, *testBroker, *managedTestVenue, View) {
	t.Helper()
	c, b, venue, p, _ := managedSetup(t, kind)
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID)
	entryFilled(t, c, b, v.ID, "FILLED", "0.00998", "0.00998")
	c.step(context.Background(), "alice", v.ID) // submits the stop and target
	c.step(context.Background(), "alice", v.ID) // confirms them at the venue
	v, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.ProtectionState != protectionProtected {
		t.Fatalf("fixture is not protected: %s", v.ProtectionState)
	}
	return c, b, venue, v
}

func exitOrders(b *testBroker) []Order {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Order{}
	for _, o := range b.placed {
		if o.Role == "exit" {
			out = append(out, o)
		}
	}
	return out
}

func stopOf(t *testing.T, c *Controller, runID string) Order {
	t.Helper()
	v, err := c.Get("alice", runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range v.Orders {
		if o.Role == "sl" {
			return o
		}
	}
	t.Fatal("run has no stop")
	return Order{}
}

// A venue can trigger the stop before the exit arrives. The reduce-only exit
// then closes nothing; it must never turn into a new opposite position.
func TestManagedExitIsReduceOnly(t *testing.T) {
	c, b, venue, v := managedProtected(t, "limit")
	venue.beforeExit = func() {
		sl := stopOf(t, c, v.ID)
		b.mu.Lock()
		triggered := b.orders[sl.ClientID]
		triggered.Status, triggered.Filled = "FILLED", triggered.Quantity
		b.orders[triggered.ClientID] = triggered
		b.account.Position = "0"
		b.mu.Unlock()
	}
	got, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline")
	if err != nil {
		t.Fatal(err)
	}
	exits := exitOrders(b)
	if len(exits) != 1 {
		t.Fatalf("expected exactly one exit, got %d", len(exits))
	}
	exit := exits[0]
	if !exit.ReduceOnly || exit.Type != "market" || exit.Side != "long" || exit.Role != "exit" {
		t.Fatalf("unsafe exit terms: %+v", exit)
	}
	if exit.ClientID != cid(v.ID, 1, "exit", "") {
		t.Fatalf("exit client ID is not the run's generation: %s", exit.ClientID)
	}
	b.mu.Lock()
	remaining := b.account.Position
	b.mu.Unlock()
	if !execution.Equal(string(remaining), "0") {
		t.Fatalf("position did not finish flat: %s", remaining)
	}
	if got.State != "completed" {
		t.Fatalf("run not completed after a flat position: %s %s", got.State, got.Error)
	}
	if submitted(b, "sl") != 1 {
		t.Fatal("a triggered stop was replaced")
	}
}

// The venue filled the exit but the reply was lost. The run keeps its lease and
// the same client ID resolves it after a restart; nothing is submitted twice.
func TestManagedExitUnknownReceipt(t *testing.T) {
	c, b, venue, v := managedProtected(t, "limit")
	venue.exitAckLost = true
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("an unconfirmed exit was reported as done")
	}
	got, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != runHaltedUnknown {
		t.Fatalf("unknown exit left the run in %q", got.State)
	}
	var lease Lease
	if err = c.DB.First(&lease, "run_id = ?", v.ID).Error; err != nil {
		t.Fatal("a halted exit released the account lease")
	}
	if len(exitOrders(b)) != 1 {
		t.Fatalf("exit was submitted %d times", len(exitOrders(b)))
	}
	c2, err := New(c.DB, c.Factory, c.Market)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c2.Shutdown)
	restarted, err := c2.Get("alice", v.ID)
	if err != nil || restarted.State != runHaltedUnknown {
		t.Fatalf("restart lost the halted state: %+v %v", restarted.State, err)
	}
	done, err := c2.Flatten(context.Background(), "alice", v.ID, "protection deadline")
	if err != nil || done.State != "completed" {
		t.Fatalf("the same exit was not reconciled: %s %v", done.State, err)
	}
	if len(exitOrders(b)) != 1 {
		t.Fatalf("a resolved exit was resubmitted: %d", len(exitOrders(b)))
	}
}

func TestManagedStopAmendRace(t *testing.T) {
	ctx := context.Background()
	t.Run("triggered stop is not replaced and no exit is oversized", func(t *testing.T) {
		c, b, _, v := managedProtected(t, "limit")
		sl := stopOf(t, c, v.ID)
		b.mu.Lock()
		triggered := b.orders[sl.ClientID]
		triggered.Status, triggered.Filled = "FILLED", triggered.Quantity
		b.orders[triggered.ClientID] = triggered
		b.account.Position = "0"
		b.mu.Unlock()
		if _, err := c.MoveStop(ctx, "alice", v.ID, "99500"); err == nil {
			t.Fatal("moved a stop that had already triggered")
		}
		if submitted(b, "sl") != 1 {
			t.Fatal("a triggered stop was replaced by a new protection")
		}
		if len(exitOrders(b)) != 0 {
			t.Fatal("a zero position was closed again")
		}
		// A flat account has nothing left to protect and must not be closed again.
		if _, err := c.MoveStop(ctx, "alice", v.ID, "101500"); err == nil {
			t.Fatal("a flat account accepted a stop change")
		}
		if len(exitOrders(b)) != 0 {
			t.Fatalf("a flat account was closed with %d exits", len(exitOrders(b)))
		}
	})
	t.Run("unsupported amend keeps the original stop", func(t *testing.T) {
		c, b, venue, v := managedProtected(t, "limit")
		venue.capabilities = execution.Capabilities{}
		_, err := c.MoveStop(ctx, "alice", v.ID, "99500")
		if !errors.Is(err, execution.ErrUnsupported) {
			t.Fatalf("unsupported amend returned %v", err)
		}
		if len(venue.amended) != 0 || len(b.cancelled) != 0 {
			t.Fatal("the old protection was cancelled or amended anyway")
		}
		if stopOf(t, c, v.ID).Price != "99000" {
			t.Fatal("the persisted stop changed without a confirmed amend")
		}
	})
	t.Run("widening is refused", func(t *testing.T) {
		c, _, venue, v := managedProtected(t, "limit")
		if _, err := c.MoveStop(ctx, "alice", v.ID, "98500"); err == nil {
			t.Fatal("a wider stop was accepted")
		}
		if len(venue.amended) != 0 {
			t.Fatal("a wider stop reached the venue")
		}
		if stopOf(t, c, v.ID).Price != "99000" {
			t.Fatal("the persisted stop was widened")
		}
	})
	t.Run("crossing the mark reduces the position", func(t *testing.T) {
		c, b, _, v := managedProtected(t, "limit")
		got, err := c.MoveStop(ctx, "alice", v.ID, "101000")
		if err != nil || got.State != "completed" {
			t.Fatalf("crossing stop did not reduce: %s %v", got.State, err)
		}
		exits := exitOrders(b)
		if len(exits) != 1 || !exits[0].ReduceOnly {
			t.Fatalf("crossing stop did not produce one reduce-only exit: %+v", exits)
		}
	})
	t.Run("tightening amends in place", func(t *testing.T) {
		c, b, venue, v := managedProtected(t, "limit")
		got, err := c.MoveStop(ctx, "alice", v.ID, "99500")
		if err != nil {
			t.Fatal(err)
		}
		if len(venue.amended) != 1 || len(b.cancelled) != 0 || submitted(b, "sl") != 1 {
			t.Fatalf("stop was not amended in place: amended=%d cancelled=%d sl=%d", len(venue.amended), len(b.cancelled), submitted(b, "sl"))
		}
		sl := stopOf(t, c, v.ID)
		if sl.Price != "99500" || got.State != "position_open" {
			t.Fatalf("amended stop was not persisted: %+v %s", sl.Price, got.State)
		}
	})
}

// An exit whose intent could not be stored is never sent.
func TestManagedExitDBFailure(t *testing.T) {
	c, b, _, v := managedProtected(t, "limit")
	if err := c.DB.Callback().Update().Before("gorm:update").Register("fail_exit_save", func(tx *gorm.DB) {
		m, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		if raw, ok := m["orders_json"].(string); ok && strings.Contains(raw, `"role":"exit"`) {
			tx.AddError(errors.New("disk full"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("an exit was reported without a durable intent")
	}
	if len(exitOrders(b)) != 0 {
		t.Fatal("an exit was sent before its intent was stored")
	}
	got, _ := c.Get("alice", v.ID)
	for _, o := range got.Orders {
		if o.Role == "exit" {
			t.Fatalf("unpersisted exit was recorded: %+v", o)
		}
	}
}

// A position that cannot be read is unknown, never flat.
func TestManagedExitPositionReadFailure(t *testing.T) {
	c, b, venue, v := managedProtected(t, "limit")
	venue.accountErr = errors.New("gateway timeout")
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("an unreadable position was treated as flat")
	}
	if len(exitOrders(b)) != 0 {
		t.Fatal("an exit was sent for an unknown position")
	}
	got, _ := c.Get("alice", v.ID)
	if got.State == "completed" {
		t.Fatal("an unreadable position completed the run")
	}
}

func TestMoveStopRequiresAnOpenRun(t *testing.T) {
	c, _, _, p, _ := managedSetup(t, "limit")
	v := approved(t, c, p)
	if _, err := c.MoveStop(context.Background(), "alice", v.ID, "99500"); err == nil {
		t.Fatal("a stop was moved on an unapproved-live run")
	}
}

// managedRestingEntry leaves the first entry partially filled with the rest
// still live at the venue, together with its protection pair.
func managedRestingEntry(t *testing.T) (*Controller, *testBroker, *managedTestVenue, View) {
	t.Helper()
	c, b, venue, p, _ := managedSetup(t, "limit")
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID)
	entryFilled(t, c, b, v.ID, "PARTIALLY_FILLED", "0.003", "0.003")
	c.step(context.Background(), "alice", v.ID) // submits the stop and target
	c.step(context.Background(), "alice", v.ID) // confirms them at the venue
	v, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c, b, venue, v
}

// The venue position may hold size this run never opened. Only the run's own
// exposure may be exited, and the persisted order must stay decodable.
func TestManagedExitClampsToRunExposure(t *testing.T) {
	c, b, _, v := managedProtected(t, "limit")
	basket := v.Plan.Quantity
	b.mu.Lock()
	b.account.Position = "0.05"
	b.mu.Unlock()
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("a position above this run's exposure was reported flat")
	}
	exits := exitOrders(b)
	if len(exits) != 1 || cmp(exits[0].Quantity, basket) != 0 {
		t.Fatalf("exit closed %v instead of this run's exposure %v", exits, basket)
	}
	if _, err := c.Get("alice", v.ID); err != nil {
		t.Fatal("the persisted exit no longer decodes:", err)
	}
	if _, err := c.ListContext(context.Background(), "alice"); err != nil {
		t.Fatal("one clamped exit broke every run of the user:", err)
	}
}

// A live entry would fill after the exit with no supervisor left to cancel it.
func TestManagedExitCancelsEntries(t *testing.T) {
	t.Run("an unconfirmed entry cancellation blocks the exit", func(t *testing.T) {
		c, b, venue, v := managedRestingEntry(t)
		venue.cancelEntErr = errors.New("cancel timeout")
		if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
			t.Fatal("an exit was sent while an entry was still live")
		}
		if len(exitOrders(b)) != 0 {
			t.Fatal("the exit reached the venue before the entry was cancelled")
		}
		got, err := c.Get("alice", v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != runHaltedUnknown {
			t.Fatalf("run is %q, not halted", got.State)
		}
		var leases int64
		c.DB.Model(&Lease{}).Count(&leases)
		if leases != 1 {
			t.Fatal("the halted run released its account")
		}
	})
	t.Run("an acknowledged cancellation releases the exit", func(t *testing.T) {
		c, b, _, v := managedRestingEntry(t)
		entryID := v.Orders[0].ClientID
		got, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline")
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "completed" {
			t.Fatalf("run is %q, not completed", got.State)
		}
		b.mu.Lock()
		cancelled := strings.Join(b.cancelled, ",")
		entry := b.orders[entryID]
		b.mu.Unlock()
		if !strings.Contains(cancelled, entryID) || entry.Status != "CANCELED" {
			t.Fatalf("the live entry was not cancelled: %s %+v", cancelled, entry)
		}
		if exits := exitOrders(b); len(exits) != 1 || cmp(exits[0].Quantity, "0.003") != 0 {
			t.Fatalf("exit did not close this run's exposure: %v", exits)
		}
	})
}

// A resting entry with no venue position yet is still a future unprotected
// fill: it must be cancelled before the run reports flat.
func TestManagedExitCancelsEntryWhenFlat(t *testing.T) {
	c, b, _, p, _ := managedSetup(t, "limit")
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID) // entry is live, nothing filled
	b.mu.Lock()
	entryID := v.Orders[0].ClientID
	live := b.orders[entryID]
	position := b.account.Position
	b.mu.Unlock()
	if live.Status != "NEW" || cmp(position, "0") != 0 {
		t.Fatalf("fixture is not a resting entry on a flat account: %s %s", live.Status, position)
	}
	got, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline")
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	cancelled := strings.Join(b.cancelled, ",")
	entry := b.orders[entryID]
	b.mu.Unlock()
	if !strings.Contains(cancelled, entryID) || entry.Status != "CANCELED" {
		t.Fatalf("the resting entry was left live: %s %+v", cancelled, entry)
	}
	if len(exitOrders(b)) != 0 {
		t.Fatal("a flat account was closed again")
	}
	if got.State != "completed" {
		t.Fatalf("a cancelled flat run is %q, not completed", got.State)
	}
}

// A venue position this run never opened cannot be closed by its exit.
func TestManagedExitRefusesForeignExposure(t *testing.T) {
	c, b, _, p, _ := managedSetup(t, "limit")
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID) // entry is live, nothing filled
	b.mu.Lock()
	b.account.Position = "0.02"
	b.mu.Unlock()
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("a position above this run's exposure was closed")
	}
	if len(exitOrders(b)) != 0 {
		t.Fatal("an exit was sent for a position this run never opened")
	}
	got, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != runHaltedUnknown {
		t.Fatalf("run is %q, not halted", got.State)
	}
	for _, o := range got.Orders {
		if o.Role == "exit" {
			t.Fatalf("a refused exit was recorded: %+v", o)
		}
	}
}

// An unreadable entry is unknown, never cancelled or skipped.
func TestManagedExitEntryLookupFailure(t *testing.T) {
	c, b, _, v := managedRestingEntry(t)
	b.mu.Lock()
	b.lookupErr = true
	b.mu.Unlock()
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("an unreadable entry was treated as cancelled")
	}
	if len(exitOrders(b)) != 0 {
		t.Fatal("an exit was sent while the entry state was unknown")
	}
	got, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != runHaltedUnknown {
		t.Fatalf("run is %q, not halted", got.State)
	}
	if got.Orders[0].Status == "CANCELED" {
		t.Fatal("an unreadable entry was marked cancelled")
	}
}

// A flat account whose exit the venue has not finished resolving is not done:
// the run halts, leaves its protection live and never resends the exit.
func TestManagedExitPendingAfterFlat(t *testing.T) {
	c, b, venue, v := managedProtected(t, "limit")
	venue.exitAckLost = true
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("an unconfirmed exit was reported as done")
	}
	venue.exitAckLost = false
	got, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	var exitID, tpID string
	for _, o := range got.Orders {
		switch o.Role {
		case "exit":
			exitID = o.ClientID
		case "tp":
			tpID = o.ClientID
		}
	}
	if exitID == "" || tpID == "" {
		t.Fatalf("fixture lost its exit or target: %+v", got.Orders)
	}
	// The account is flat but the venue still reports the exit in progress.
	b.mu.Lock()
	observed := b.orders[exitID]
	observed.Status, observed.Filled = "PARTIALLY_FILLED", "0.005"
	b.orders[exitID] = observed
	b.account.Position = "0"
	b.mu.Unlock()
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "protection deadline"); err == nil {
		t.Fatal("a flat account with an unresolved exit was reported done")
	}
	if n := len(exitOrders(b)); n != 1 {
		t.Fatalf("the unresolved exit was resubmitted: %d", n)
	}
	b.mu.Lock()
	tp := b.orders[tpID]
	cancelled := strings.Join(b.cancelled, ",")
	b.mu.Unlock()
	if tp.Status != "NEW" || strings.Contains(cancelled, tpID) {
		t.Fatalf("protection was cancelled while the exit was unresolved: %s %s", tp.Status, cancelled)
	}
	got, err = c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != runHaltedUnknown {
		t.Fatalf("run is %q, not halted", got.State)
	}
}
