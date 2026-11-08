package automation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"nofx/trading/risk"
)

type scheduledSource struct {
	symbols [][]string
	err     error
}

func (s *scheduledSource) Next(_ context.Context, p risk.Policy) (Signal, error) {
	s.symbols = append(s.symbols, append([]string(nil), p.Symbols...))
	if s.err != nil {
		return Signal{}, s.err
	}
	return Signal{}, ErrNoSignal
}
func scheduledRig(t *testing.T, symbols []string) *automationRig {
	t.Helper()
	r := rig(t, nil)
	r.policy.Symbols = symbols
	r.policy.Version = ""
	version, err := risk.PolicyVersion(r.policy)
	if err != nil {
		t.Fatal(err)
	}
	r.policy.Version = version
	body, err := json.Marshal(r.policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.db.Table("managed_risk_policies").Where("account_key = ?", "account").Updates(map[string]any{"version": version, "body": string(body)}).Error; err != nil {
		t.Fatal(err)
	}
	target := r.now.Truncate(15 * time.Minute).Add(5 * time.Minute)
	if target.Before(r.now) {
		target = target.Add(15 * time.Minute)
	}
	r.advance(target.Sub(r.now))
	return r
}
func TestClosedBarEvaluatesEachSymbolOnceAndAlignsToNextClose(t *testing.T) {
	r := scheduledRig(t, []string{"BTCUSDT", "ETHUSDT"})
	source := &scheduledSource{}
	rt := r.runtime(t, source)
	tick := func() {
		t.Helper()
		if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	r.advance(2 * time.Second)
	tick()
	want := [][]string{{"BTCUSDT"}, {"ETHUSDT"}}
	if !reflect.DeepEqual(source.symbols, want) {
		t.Fatalf("each symbol must be evaluated independently: %v", source.symbols)
	}
	for i := 0; i < 3; i++ {
		r.advance(time.Minute)
		tick()
	}
	if len(source.symbols) != 2 {
		t.Fatalf("same closed bar caused %d model calls", len(source.symbols))
	}
	next := (*r.clock).Truncate(15 * time.Minute).Add(15 * time.Minute)
	r.advance(next.Add(-time.Second).Sub(*r.clock))
	tick()
	if len(source.symbols) != 2 {
		t.Fatal("requested another signal before the next candle closed")
	}
	r.advance(time.Second)
	tick()
	r.advance(2 * time.Second)
	tick()
	if !reflect.DeepEqual(source.symbols, append(want, want...)) {
		t.Fatalf("new close did not evaluate both symbols: %v", source.symbols)
	}
	if r.venue.placeCount() != 0 {
		t.Fatal("empty signals placed orders")
	}
}
func TestClosedBarAuthorizationKeepsFullPolicyVersion(t *testing.T) {
	r := scheduledRig(t, []string{"BTCUSDT", "ETHUSDT"})
	r.advance(r.now.Sub(*r.clock))
	r.ctrl.Interval = -1
	signal := Signal{ID: "closed-bar-authorization", Symbol: "BTCUSDT", Idea: r.idea(), CreatedAt: *r.clock, ExpiresAt: r.clock.Add(SignalExpiry)}
	rt := r.runtime(t, &automationSource{signals: []Signal{signal}})
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	runs := runStates(t, r.db)
	if len(runs) != 1 || runs[0].PolicyVersion != r.policy.Version {
		t.Fatal("single-symbol generation changed the authorized policy")
	}
}

func TestClosedBarCheckpointSurvivesRuntimeRestart(t *testing.T) {
	r := scheduledRig(t, []string{"BTCUSDT"})
	source := &scheduledSource{}
	if err := r.runtime(t, source).Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if err := r.runtime(t, source).Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if len(source.symbols) != 1 {
		t.Fatalf("runtime restart repeated paid evaluation: %v", source.symbols)
	}
}
func TestClosedBarDoesNotRepeatFailedModelRequest(t *testing.T) {
	r := scheduledRig(t, []string{"BTCUSDT"})
	source := &scheduledSource{err: errors.New("model unavailable")}
	rt := r.runtime(t, source)
	if err := rt.Tick(context.Background(), "alice", "account"); err == nil {
		t.Fatal("model error was hidden")
	}
	r.advance(time.Minute)
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if len(source.symbols) != 1 {
		t.Fatal("retried the failed request on the same closed bar")
	}
}
func TestClosedBarCheckpointFailurePreventsModelRequest(t *testing.T) {
	r := scheduledRig(t, []string{"BTCUSDT"})
	source := &scheduledSource{}
	rt := r.runtime(t, source)
	callback := "fail_signal_checkpoint"
	if err := r.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "managed_signal_scans" {
			tx.AddError(errors.New("checkpoint unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer r.db.Callback().Create().Remove(callback)
	if err := rt.Tick(context.Background(), "alice", "account"); err == nil {
		t.Fatal("checkpoint error was hidden")
	}
	if len(source.symbols) != 0 {
		t.Fatal("model request proceeded without a durable checkpoint")
	}
	if err := r.db.Callback().Create().Remove(callback); err != nil {
		t.Fatal(err)
	}
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if len(source.symbols) != 1 {
		t.Fatal("failed checkpoint skipped the unprocessed symbol")
	}
}
