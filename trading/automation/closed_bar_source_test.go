package automation

import (
	"context"
	"nofx/planning/model"
	"strings"
	"testing"
	"time"
)

func TestModelSourceRejectsCrossedScheduleBoundaryBeforePayment(t *testing.T) {
	planner := &stubPlanner{reply: model.Message{Content: `{"ideas":[]}`}}
	feed := &stubMarket{book: testBook(signalTestNow), rows: testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars)}
	source := NewModelSource(planner, feed, func() time.Time { return signalTestNow })
	ctx := context.WithValue(context.Background(), signalBarCloseKey{}, signalTestNow.Add(-15*time.Minute))
	if _, err := source.Next(ctx, testPolicy()); err == nil || !strings.Contains(err.Error(), "scheduled candle changed") {
		t.Fatalf("request crossed the paid scan checkpoint: %v", err)
	}
	if planner.calls != 0 {
		t.Fatal("paid for a candle outside the durable checkpoint")
	}
}

func TestModelSourceWaitsForLatestClosedBar(t *testing.T) {
	planner := &stubPlanner{reply: model.Message{Content: `{"ideas":[]}`}}
	feed := &stubMarket{book: testBook(signalTestNow), rows: testCandles(signalTestNow.Unix()-2*SignalBarSeconds, SignalBars)}
	source := NewModelSource(planner, feed, func() time.Time { return signalTestNow })
	if _, err := source.Next(context.Background(), testPolicy()); err == nil || !strings.Contains(err.Error(), "latest closed 15m candle") {
		t.Fatalf("outdated bar was evaluated: %v", err)
	}
	if planner.calls != 0 {
		t.Fatal("paid for a repeated older candle")
	}
}
