package automation

import (
	"context"
	"errors"
	"nofx/planning/model"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type serializedPlannerFixture struct {
	started, release chan struct{}
	calls            atomic.Int32
}

func (p *serializedPlannerFixture) Chat(_ context.Context, _ []model.Message, _ []model.Tool) (model.Message, error) {
	if p.calls.Add(1) == 1 {
		close(p.started)
		<-p.release
	}
	return model.Message{Content: `{"ideas":[]}`}, nil
}

func TestModelSourceSerializesSharedModelClient(t *testing.T) {
	_, _, feed := testSource(model.Message{}, testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars))
	planner := &serializedPlannerFixture{started: make(chan struct{}), release: make(chan struct{})}
	source := NewModelSource(planner, feed, func() time.Time { return signalTestNow })
	var release sync.Once
	defer release.Do(func() { close(planner.release) })
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := source.Next(context.Background(), testPolicy()); first <- err }()
	select {
	case <-planner.started:
	case <-time.After(time.Second):
		t.Fatal("first model request never started")
	}
	go func() { _, err := source.Next(context.Background(), testPolicy()); second <- err }()
	select {
	case err := <-second:
		t.Fatalf("shared model client used concurrently: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release.Do(func() { close(planner.release) })
	for _, done := range []chan error{first, second} {
		select {
		case err := <-done:
			if !errors.Is(err, ErrNoSignal) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("source did not finish")
		}
	}
}
