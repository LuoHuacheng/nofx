package automation

import (
	"context"
	"nofx/trading/risk"
	"sync/atomic"
	"testing"
	"time"
)

type blockingIdeaSource struct {
	started chan struct{}
	calls   atomic.Int32
}

func (s *blockingIdeaSource) Next(ctx context.Context, _ risk.Policy) (Signal, error) {
	if s.calls.Add(1) == 1 {
		close(s.started)
	}
	<-ctx.Done()
	return Signal{}, ctx.Err()
}

func TestSupervisorReconcilesWhileModelIsPending(t *testing.T) {
	source := &blockingIdeaSource{started: make(chan struct{})}
	r := rig(t, nil)
	rt := r.runtime(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	if err := rt.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		cancel()
		stop, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		if err := rt.Shutdown(stop); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}
	before := r.venue.snapshotCalls()
	r.advance(3 * time.Second)
	done := make(chan error, 1)
	go func() { done <- rt.Tick(ctx, "alice", "account") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("pending model blocks account reconciliation")
	}
	if r.venue.snapshotCalls() <= before {
		t.Fatal("no fresh account read while model is pending")
	}
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if source.calls.Load() != 1 {
		t.Fatalf("started %d concurrent model calls", source.calls.Load())
	}
}

type releasedIdeaSource struct {
	started, release chan struct{}
	signal           Signal
}

func (s *releasedIdeaSource) Next(ctx context.Context, _ risk.Policy) (Signal, error) {
	close(s.started)
	select {
	case <-s.release:
		return s.signal, nil
	case <-ctx.Done():
		return Signal{}, ctx.Err()
	}
}

func TestPendingModelCannotBypassLaterSafetyFreeze(t *testing.T) {
	r := rig(t, nil)
	r.ctrl.Interval = -1
	source := &releasedIdeaSource{started: make(chan struct{}), release: make(chan struct{}), signal: Signal{ID: "pending-freeze", Symbol: "BTCUSDT", Idea: r.idea(), CreatedAt: r.now, ExpiresAt: r.now.Add(SignalExpiry)}}
	rt := r.runtime(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.tick(ctx, "alice", "account", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	rt.freeze("account", "unowned position", freezeSafety)
	close(source.release)
	done := make(chan struct{})
	go func() { rt.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source worker did not finish")
	}
	if len(runStates(t, r.db)) != 0 || r.venue.placeCount() != 0 {
		t.Fatal("late model result bypassed the safety freeze")
	}
}

func TestReconciliationFitsEquityFreshnessWindow(t *testing.T) {
	if ReconcileInterval >= risk.PortfolioSnapshotMaxAge {
		t.Fatalf("reconciliation every %v exceeds equity freshness %v", ReconcileInterval, risk.PortfolioSnapshotMaxAge)
	}
}
