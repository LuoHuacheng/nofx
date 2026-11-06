package live

import (
	"context"
	"testing"
	"time"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

// TestRiskBreakerReduceAllowed pins the asymmetry of a latch: it stops entries,
// never reductions. An operator must stay able to close a live position.
func TestRiskBreakerReduceAllowed(t *testing.T) {
	c, b, _, p, _ := managedSetup(t, "limit")
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID) // submits the entry
	entryFilled(t, c, b, v.ID, "FILLED", "0.00998", "0.00998")
	c.step(context.Background(), "alice", v.ID) // persists the fill, submits protection
	entries := submitted(b, "entry")
	if entries == 0 {
		t.Fatal("fixture placed no entry")
	}
	if err := c.Risk.Pause(context.Background(), "alice", "account", "operator pause"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	c.step(context.Background(), "alice", v.ID)
	if got := submitted(b, "entry"); got != entries {
		t.Fatalf("a latch must stop new entries: %d placed, %d before the latch", got, entries)
	}
	if _, err := c.Flatten(context.Background(), "alice", v.ID, "operator pause"); err != nil {
		t.Fatalf("a reduction must stay legal under a latch: %v", err)
	}
	if n := len(exitOrders(b)); n != 1 {
		t.Fatalf("expected one exit order under the latch, got %d", n)
	}
}

// TestRiskBreakerExecuteReleases pins the Execute refusal: the hold is committed
// before the check, so the refusal must hand it back in the same transaction.
func TestRiskBreakerExecuteReleases(t *testing.T) {
	c, b, _, p, store := riskSetup(t, "market")
	if err := store.Pause(context.Background(), "alice", "account", "operator pause"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	v, err := runRisk(t, c, p)
	if err == nil {
		t.Fatal("expected the latched account to refuse execution")
	}
	if v.State != "needs_attention" {
		t.Fatalf("expected needs_attention, got %q", v.State)
	}
	if len(b.placed) != 0 {
		t.Fatalf("a refused run must place no orders, placed %d", len(b.placed))
	}
	if state := reservationState(t, store, v.ID); state != risk.StateReleased {
		t.Fatalf("the refused hold must be released, got %q", state)
	}
}

// TestRiskBreakerStepFlattensExposure pins the in-tick flatten: a latched run
// that still holds exposure is closed by the supervisor itself, and never
// resumes adding on a later tick.
func TestRiskBreakerStepFlattensExposure(t *testing.T) {
	c, b, _, v := managedProtected(t, "limit")
	pol, err := c.Risk.Policy(context.Background(), "alice", "account")
	if err != nil {
		t.Fatal(err)
	}
	pol.DailyLossBPS = 1
	if pol.Version, err = risk.PolicyVersion(pol); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Risk.PutPolicy(context.Background(), pol); err != nil {
		t.Fatal(err)
	}
	// One basis point below the day start trips the 1 bps limit exactly.
	acct := execution.ManagedAccount{AccountKey: "account", Venue: "binance", Environment: execution.EnvironmentLive, Equity: "9999", Available: "9999"}
	if err = c.Risk.Observe(context.Background(), acct, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	before, entries := len(exitOrders(b)), submitted(b, "entry")
	c.step(context.Background(), "alice", v.ID)
	if got := len(exitOrders(b)); got <= before {
		t.Fatalf("a latched run with exposure placed no exit: %d, %d before", got, before)
	}
	if got := submitted(b, "entry"); got != entries {
		t.Fatalf("a latched run added an entry: %d, %d before", got, entries)
	}
	c.step(context.Background(), "alice", v.ID)
	if got := submitted(b, "entry"); got != entries {
		t.Fatalf("a latched run resumed entries on the next tick: %d, %d before", got, entries)
	}
}
