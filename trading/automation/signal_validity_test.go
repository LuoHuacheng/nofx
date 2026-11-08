package automation

import (
	"context"
	"testing"
	"time"

	"nofx/planning/model"
)

func TestReviewProductionSourceClock(t *testing.T) {
	_, planner, feed := testSource(model.Message{Content: `{"ideas":[]}`}, testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars))
	source := NewModelSource(planner, feed, nil) // same arguments as api.managedSignalSource
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("production source with default clock panicked: %v", p)
		}
	}()
	_, _ = source.Next(context.Background(), testPolicy())
}

func TestReviewExpiredSignalRejected(t *testing.T) {
	fixture := rig(t, nil)
	fixture.ctrl.Interval = -1 // deterministic: inspect authorization, no background order worker
	fixture.source.signals = []Signal{{ID: "expired-review-signal", Symbol: "BTCUSDT", Idea: fixture.idea(), CreatedAt: fixture.now.Add(-2 * time.Minute), ExpiresAt: fixture.now.Add(-time.Minute)}}
	rt := fixture.runtime(t, fixture.source)
	err := rt.Tick(context.Background(), "alice", "account")
	runs := runStates(t, fixture.db)
	if len(runs) != 0 {
		t.Fatalf("expired signal created %d authorized run(s), state=%s, error=%v", len(runs), runs[0].State, err)
	}
}

func TestReviewManagedEntryDeadline(t *testing.T) {
	fixture := rig(t, nil)
	fixture.ctrl.Interval = -1
	fixture.source.signals = []Signal{{ID: "deadline-review-signal", Symbol: "BTCUSDT", Idea: fixture.idea(), CreatedAt: fixture.now, ExpiresAt: fixture.now.Add(SignalExpiry)}}
	rt := fixture.runtime(t, fixture.source)
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	runs := runStates(t, fixture.db)
	if len(runs) != 1 || runs[0].Deadline == nil || runs[0].StartedAt == nil {
		t.Fatalf("missing run deadline: %+v", runs)
	}
	window := runs[0].Deadline.Sub(*runs[0].StartedAt)
	if window != EntryFillWindow {
		t.Fatalf("managed entry expires after %s, want %s", window, EntryFillWindow)
	}
}
