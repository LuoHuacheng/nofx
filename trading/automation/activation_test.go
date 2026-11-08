package automation

import (
	"context"
	"testing"
	"time"

	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/trading/risk"
)

type delayedCompileMarket struct{ rig *automationRig }

func (m delayedCompileMarket) Snapshot(context.Context, string, string) (core.Snapshot, error) {
	m.rig.advance(2 * time.Second)
	book := m.rig.book
	book.Observed = *m.rig.clock
	return book, nil
}

func TestCompileIdeaSamplesClockAfterMarketRead(t *testing.T) {
	r := rig(t, nil)
	r.ctrl.Market = delayedCompileMarket{rig: r}
	rt := r.runtime(t, nil)
	limits, err := live.ManagedAccountLimits(r.policy, r.venue.snapshot, *r.clock)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := rt.compileIdea(context.Background(), r.policy, limits, Signal{Symbol: "BTCUSDT", Idea: r.idea()})
	if err != nil {
		t.Fatalf("a freshly returned book must not be future-dated: %v", err)
	}
	if plan.Created != *r.clock {
		t.Fatal("plan clock was sampled before the market read")
	}
}

type delayedIdeaSource struct{ rig *automationRig }

func (s delayedIdeaSource) Next(context.Context, risk.Policy) (Signal, error) {
	created := *s.rig.clock
	// The controller also samples wall time, so advance both clocks as a real model call would.
	time.Sleep(6 * time.Second)
	*s.rig.clock = time.Now().UTC()
	return Signal{ID: "slow-model", Symbol: "BTCUSDT", Idea: s.rig.idea(), CreatedAt: created, ExpiresAt: created.Add(SignalExpiry)}, nil
}

func TestSignalRefreshesEquityAfterModelLatency(t *testing.T) {
	r := rig(t, nil)
	r.ctrl.Interval = -1
	rt := r.runtime(t, delayedIdeaSource{rig: r})
	if err := rt.Tick(context.Background(), "alice", r.binding.ID); err != nil {
		t.Fatalf("a valid signal after six seconds must use fresh account equity: %v", err)
	}
	runs := runStates(t, r.db)
	if len(runs) != 1 || runs[0].State != "running" {
		t.Fatalf("valid signal was not started: %+v", runs)
	}
}

func TestStartAccountRefreshesStaleEquity(t *testing.T) {
	r := rig(t, nil)
	rt := r.runtime(t, nil)
	r.advance(8 * time.Second)
	if err := rt.StartAccount(context.Background(), "alice", r.binding.ID, r.policy.Version, r.binding.Environment); err != nil {
		t.Fatalf("a healthy account between REST ticks should start after a fresh read: %v", err)
	}
	if r.venue.snapCalls == 0 {
		t.Fatal("startup did not read the venue")
	}
	if r.venue.placeCount() != 0 {
		t.Fatal("startup replayed an entry")
	}
}

func TestStartAccountRetainsOperatorPause(t *testing.T) {
	r := rig(t, nil)
	rt := r.runtime(t, nil)
	if err := rt.PauseAccount(context.Background(), "alice", r.binding.ID, "operator pause"); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartAccount(context.Background(), "alice", r.binding.ID, r.policy.Version, r.binding.Environment); err == nil {
		t.Fatal("startup cleared a durable operator pause")
	}
	if r.venue.placeCount() != 0 {
		t.Fatal("paused startup submitted an entry")
	}
}

func TestRuntimeWithoutAlertTarget(t *testing.T) {
	r := rig(t, nil)
	rt := r.runtime(t, nil)
	if err := rt.DeliverAlerts(context.Background()); err != nil {
		t.Fatalf("unconfigured alert target must be a clean no-op: %v", err)
	}
}
