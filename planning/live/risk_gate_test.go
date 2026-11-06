package live

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"nofx/planning"
	"nofx/planning/core"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// riskSetup binds the run account to a managed venue and installs a risk store,
// so approval takes the managed path and Execute must reserve before any order.
func riskSetup(t *testing.T, kind string) (*Controller, *testBroker, *testMarket, core.Plan, *risk.Store) {
	t.Helper()
	return riskSetupVenue(t, kind, "binance")
}

// riskSetupVenue builds the same managed fixture for a policy that names
// another venue, which is how a mismatched policy reaches the approval gate.
func riskSetupVenue(t *testing.T, kind, policyVenue string) (*Controller, *testBroker, *testMarket, core.Plan, *risk.Store) {
	t.Helper()
	c, b, m, p := setup(t, kind)
	c.Factory = func(context.Context, string, string) (Broker, string, error) {
		return NOFXBroker{Trader: testNOFXTrader{b}}, "account", nil
	}
	binding := execution.Binding{ID: "account", UserID: "alice", ExchangeID: "exchange", Venue: "binance", Environment: execution.EnvironmentLive, VenueAccountID: "venue-account", VerifiedAt: time.Now().UTC(), Enabled: true}
	if e := c.DB.Create(&binding).Error; e != nil {
		t.Fatal(e)
	}
	return c, b, m, p, managedRisk(t, c, policyVenue, "10000")
}

// runRisk approves the plan and starts it, which is where the reservation commits.
func runRisk(t *testing.T, c *Controller, p core.Plan) (View, error) {
	t.Helper()
	v := approved(t, c, p)
	return c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash)
}

func reservationState(t *testing.T, store *risk.Store, runID string) string {
	t.Helper()
	r, e := store.Reservation(context.Background(), runID)
	if e != nil {
		t.Fatal(e)
	}
	return r.State
}

func TestRiskGateStopsBeforePOST(t *testing.T) {
	t.Run("per-trade", func(t *testing.T) {
		c, b, _, p, store := riskSetup(t, "limit")
		pol, e := store.Policy(context.Background(), "alice", "account")
		if e != nil {
			t.Fatal(e)
		}
		pol.PerTradeRiskBPS = 1
		if pol.Version, e = risk.PolicyVersion(pol); e != nil {
			t.Fatal(e)
		}
		if _, e = store.PutPolicy(context.Background(), pol); e != nil {
			t.Fatal(e)
		}
		_, e = runRisk(t, c, p)
		if e == nil {
			t.Fatal("a trade above the per-trade limit passed the risk gate")
		}
		if !strings.Contains(e.Error(), "per-trade risk") {
			t.Fatalf("gate stopped for an unexpected reason: %v", e)
		}
		if len(b.placed) != 0 {
			t.Fatalf("%d orders reached the venue", len(b.placed))
		}
	})

	t.Run("stale", func(t *testing.T) {
		c, b, _, p, store := riskSetup(t, "limit")
		// Move the newest baseline backwards: recompute keeps the newest
		// observation, so the fresh one has to go first.
		stale := time.Now().UTC().Add(-10 * time.Second)
		var obs []risk.EquityObservation
		if e := c.DB.Where("account_key = ?", "account").Find(&obs).Error; e != nil {
			t.Fatal(e)
		}
		for _, o := range obs {
			if o.ObservedAt.After(stale) {
				if e := c.DB.Delete(&risk.EquityObservation{}, o.ID).Error; e != nil {
					t.Fatal(e)
				}
			}
		}
		acct := execution.ManagedAccount{AccountKey: "account", Venue: "binance", Environment: execution.EnvironmentLive, Equity: "10000", Available: "10000"}
		if e := store.Observe(context.Background(), acct, nil, stale); e != nil {
			t.Fatal(e)
		}
		_, e := runRisk(t, c, p)
		if e == nil {
			t.Fatal("a stale equity baseline passed the risk gate")
		}
		if !strings.Contains(e.Error(), "stale") {
			t.Fatalf("gate stopped for an unexpected reason: %v", e)
		}
		if len(b.placed) != 0 {
			t.Fatalf("%d orders reached the venue", len(b.placed))
		}
	})

	t.Run("version", func(t *testing.T) {
		c, b, _, p, store := riskSetup(t, "limit")
		v := approved(t, c, p)
		pol, e := store.Policy(context.Background(), "alice", "account")
		if e != nil {
			t.Fatal(e)
		}
		pol.PerTradeRiskBPS = 5
		if pol.Version, e = risk.PolicyVersion(pol); e != nil {
			t.Fatal(e)
		}
		if _, e = store.PutPolicy(context.Background(), pol); e != nil {
			t.Fatal(e)
		}
		if _, e = c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); e == nil {
			t.Fatal("a re-authorized policy did not stop the approved run")
		} else if !strings.Contains(e.Error(), "policy version changed") {
			t.Fatalf("gate stopped for an unexpected reason: %v", e)
		}
		if len(b.placed) != 0 {
			t.Fatalf("%d orders reached the venue", len(b.placed))
		}
	})

	t.Run("db", func(t *testing.T) {
		c, b, _, p, _ := riskSetup(t, "limit")
		v := approved(t, c, p)
		sql, _ := c.DB.DB()
		if e := sql.Close(); e != nil {
			t.Fatal(e)
		}
		if _, e := c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); e == nil {
			t.Fatal("a run started without a readable risk store")
		}
		if len(b.placed) != 0 {
			t.Fatalf("%d orders reached the venue", len(b.placed))
		}
	})
}

// The reservation cannot be written without the store, so the run must stop
// before the first order instead of trading unreserved.
func TestRiskGateManagedRequiresStore(t *testing.T) {
	c, b, _, p, _ := riskSetup(t, "limit")
	v := approved(t, c, p)
	c.Risk = nil
	_, e := c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash)
	if e == nil || !strings.Contains(e.Error(), "managed run requires the risk store") {
		t.Fatalf("managed run started without the risk store: %v", e)
	}
	if len(b.placed) != 0 {
		t.Fatalf("%d orders reached the venue", len(b.placed))
	}
}

func TestRiskExposeOnFill(t *testing.T) {
	c, b, _, p, store := riskSetup(t, "limit")
	v, e := runRisk(t, c, p)
	if e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	b.fillEntryImmediately = true
	b.mu.Unlock()
	c.step(context.Background(), "alice", v.ID)
	if s := reservationState(t, store, v.ID); s != risk.StateExposed {
		t.Fatalf("a filled entry left the hold in %q", s)
	}
}

func TestRiskReleaseNeedsTerminalProof(t *testing.T) {
	ctx := context.Background()
	c, b, _, p, store := riskSetup(t, "limit")
	v, e := runRisk(t, c, p)
	if e != nil {
		t.Fatal(e)
	}
	if s := reservationState(t, store, v.ID); s != risk.StateReserved {
		t.Fatalf("a started run is not reserved: %q", s)
	}

	b.mu.Lock()
	b.fillEntryImmediately = true
	b.mu.Unlock()
	c.step(ctx, "alice", v.ID)
	if s := reservationState(t, store, v.ID); s != risk.StateExposed {
		t.Fatalf("a fill with an open position did not expose the hold: %q", s)
	}

	// The venue now reports the position; the next tick submits protection.
	b.mu.Lock()
	b.account.Position = "0.00998"
	b.mu.Unlock()
	c.step(ctx, "alice", v.ID)
	if s := reservationState(t, store, v.ID); s == risk.StateReleased {
		t.Fatal("a pending protection released the hold")
	}

	// Authoritative flat account and fully terminal orders are the only proof.
	run, e := c.Get("alice", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	for _, o := range run.Orders {
		switch o.Role {
		case "sl":
			ord := b.orders[o.ClientID]
			ord.Status, ord.Filled = "FILLED", ord.Quantity
			b.orders[o.ClientID] = ord
		case "tp":
			ord := b.orders[o.ClientID]
			ord.Status = "CANCELED"
			b.orders[o.ClientID] = ord
		}
	}
	b.account.Position = "0"
	b.mu.Unlock()
	if _, e = c.CheckStatus(ctx, "alice", v.ID); e != nil {
		t.Fatal(e)
	}
	if s := reservationState(t, store, v.ID); s != risk.StateReleased {
		t.Fatalf("a flat, terminal run did not release the hold: %q", s)
	}

	// A leftover live entry order keeps the hold: the run is not terminal yet.
	c2, b2, _, p2, store2 := riskSetup(t, "ladder")
	v2, e := runRisk(t, c2, p2)
	if e != nil {
		t.Fatal(e)
	}
	c2.step(ctx, "alice", v2.ID)
	b2.mu.Lock()
	placed := len(b2.placed)
	b2.mu.Unlock()
	if placed != 2 {
		t.Fatalf("fixture left %d open entry orders, want 2", placed)
	}
	if s := reservationState(t, store2, v2.ID); s == risk.StateReleased {
		t.Fatal("an open entry order released the hold")
	}
}

// A run can finish flat with an unproven trade: an exit filled to zero exposure
// while a protection order is still resting. That branch completes the run too,
// so it must release the reservation exactly like the confirmed-trade path.
func TestRiskReleaseOnSecondCompletionBranch(t *testing.T) {
	ctx := context.Background()
	c, b, _, p, store := riskSetup(t, "limit")
	v, e := runRisk(t, c, p)
	if e != nil {
		t.Fatal(e)
	}
	if s := reservationState(t, store, v.ID); s != risk.StateReserved {
		t.Fatalf("a started run is not reserved: %q", s)
	}

	b.mu.Lock()
	b.fillEntryImmediately = true
	b.mu.Unlock()
	c.step(ctx, "alice", v.ID)

	// Confirm the position so the protection pair is submitted.
	run, e := c.Get("alice", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	b.account.Position = run.Orders[0].Filled
	b.mu.Unlock()
	c.step(ctx, "alice", v.ID)

	// The stop filled for the whole entry: exposure is zero, but the resting
	// target means the trade is not proven closed.
	run, e = c.Get("alice", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	b.account.Position = "0"
	for _, o := range run.Orders {
		ord := b.orders[o.ClientID]
		if o.Role == "sl" {
			ord.Status, ord.Filled = "FILLED", ord.Quantity
			b.orders[o.ClientID] = ord
		}
	}
	b.mu.Unlock()

	c.step(ctx, "alice", v.ID)
	if s := reservationState(t, store, v.ID); s != risk.StateReleased {
		t.Fatalf("a flat run finished without releasing its hold: %q", s)
	}
	got, e := c.Get("alice", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.State != "completed" {
		t.Fatalf("the second completion branch left the run in %q", got.State)
	}
}

// TestRiskReservationMarginAtLeverage pins the margin hold at a leverage whose
// quotient is not a finite decimal: notional/3 used to fail ExactDecimal, so
// every managed run at 3x could not start at all.
func TestRiskReservationMarginAtLeverage(t *testing.T) {
	for _, lev := range []int{1, 3} {
		t.Run(fmt.Sprint(lev), func(t *testing.T) {
			notional, ok := new(big.Rat).SetString("100")
			if !ok {
				t.Fatal("bad fixture")
			}
			margin := new(big.Rat).Quo(notional, big.NewRat(int64(lev), 1))
			held, e := ceilExact(margin)
			if e != nil {
				t.Fatalf("a %dx margin hold is not representable: %v", lev, e)
			}
			back, e := execution.Number(string(held))
			if e != nil {
				t.Fatal(e)
			}
			if back.Cmp(margin) < 0 {
				t.Fatalf("the hold %s rounds below the venue margin %s", held, margin.FloatString(12))
			}
		})
	}
}

// leveragedFixture is a managed trader that accepts any approved leverage.
type leveragedFixture struct{ testNOFXTrader }

func (leveragedFixture) PrepareExecutionLeverage(context.Context, string, int) error { return nil }

// TestRiskReservationStartsAtLeverage drives the whole path: with a plain
// ExactDecimal margin, Execute at 3x used to release the approval instead of
// reserving, so no managed run could ever start at that leverage.
func TestRiskReservationStartsAtLeverage(t *testing.T) {
	c, b, m, p, store := riskSetupVenue(t, "limit", "binance")
	// The plain fixture broker cannot set leverage; a managed account can.
	c.Factory = func(context.Context, string, string) (Broker, string, error) {
		return NOFXBroker{Trader: leveragedFixture{testNOFXTrader{b}}}, "account", nil
	}
	limits := p.Constraints
	limits.Leverage = 3
	plan, e := core.Compile(m.book, limits, core.Idea{Name: "lev", Side: "long", Entry: "100000", Stop: "99000", Target: "104000"}, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	ds := planning.DraftStore{DB: c.DB, UserID: "alice", ExchangeID: "exchange", Venue: "binance"}
	if e = ds.Add([]core.Plan{plan}); e != nil {
		t.Fatal(e)
	}
	v := approved(t, c, plan)
	if _, e = c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); e != nil {
		t.Fatalf("a 3x managed run could not start: %v", e)
	}
	held, e := store.Reservation(context.Background(), v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if held.State != risk.StateReserved {
		t.Fatalf("run started without a hold: %q", held.State)
	}
}
