package automation

import (
	"context"
	"testing"
	"time"

	"nofx/planning"
	"nofx/planning/core"
	"nofx/planning/live"
)

func TestStartupRetiresExpiredAutomaticApproval(t *testing.T) {
	f := rig(t, nil)
	f.ctrl.Interval = -1
	limits, err := live.ManagedAccountLimits(f.policy, f.venue.snapshot, f.now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := core.Compile(f.book, limits, f.idea(), f.now)
	if err != nil {
		t.Fatal(err)
	}
	drafts := &planning.DraftStore{DB: f.db, UserID: "alice", ExchangeID: "exchange", Venue: "binance"}
	if err := drafts.Add([]core.Plan{plan}); err != nil {
		t.Fatal(err)
	}
	v, err := f.ctrl.AuthorizePolicy(context.Background(), "alice", plan.ID, plan.Hash, f.policy.Version, "expired-before-restart", f.now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	f.advance(31 * time.Second)
	rt := f.runtime(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, cancelStop := context.WithTimeout(context.Background(), time.Second)
		defer cancelStop()
		if err := rt.Shutdown(stop); err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := f.ctrl.Get("alice", v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == "cancelled" {
			if f.venue.placeCount() != 0 {
				t.Fatal("startup replayed an entry")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("startup did not retire the expired automatic approval")
}
