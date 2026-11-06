package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nofx/planning/core"
	"nofx/planning/market"
	"nofx/planning/model"
	"testing"
	"time"
)

type loopFixture struct {
	reads int
	chats int
}

func TestModelPlanResultsDoNotRepeatOrderBooksOrHistory(t *testing.T) {
	plan := core.Plan{ID: "draft", Quantity: "0.1", Risk: "10", Reward: "15", RewardRisk: "1.5", Idea: core.Idea{Name: "Entry", Entry: "100", Stop: "90", Target: "115"}, Snapshot: core.Snapshot{Venue: "okx", Symbol: "BTC-USDT-SWAP", QuoteAsset: "USDT", Bid: "99", Ask: "100"}, Orders: []core.OrderSpec{{Index: 1, Price: "100", Quantity: "0.1"}}}
	result := map[string]any{"plans": []core.Plan{plan}, "rejected": []string{"TWAP: below minimum quantity"}, "approval_required": true}
	compact := modelToolResult("propose_plans", result).(map[string]any)
	raw, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Plans    []map[string]any
		Rejected []string
		Approval bool `json:"approval_required"`
	}
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	p := decoded.Plans[0]
	if p["snapshot"] != nil || p["analysis"] != nil || p["reward_risk"] != "1.5" || p["idea"] == nil || p["orders"] == nil || !decoded.Approval || len(decoded.Rejected) != 1 {
		t.Fatal("lost decision data or repeated full plan", string(raw))
	}
	if result["plans"].([]core.Plan)[0].Snapshot.Bid != "99" {
		t.Fatal("persisted or UI plan was mutated")
	}
}

func (f *loopFixture) Snapshot(context.Context, string, string) (core.Snapshot, error) {
	return core.Snapshot{}, nil
}
func (f *loopFixture) Candles(_ context.Context, q market.CandleQuery) ([]market.Candle, error) {
	f.reads++
	return []market.Candle{{Time: time.Now().Add(-time.Hour).Unix(), Open: "100", High: "101", Low: "99", Close: "100", Volume: "1"}}, nil
}
func (f *loopFixture) Chat(_ context.Context, _ []model.Message, tools []model.Tool) (model.Message, error) {
	f.chats++
	if len(tools) == 0 {
		return model.Message{Role: "assistant", Content: "No validated plan was saved. No trade."}, nil
	}
	c := model.ToolCall{ID: fmt.Sprint("call_", f.chats), Type: "function"}
	c.Function.Name = "get_candles"
	if f.chats%2 == 0 {
		c.Function.Arguments = `{ "limit": 1, "interval":"1h" }`
	} else {
		c.Function.Arguments = `{"interval":"1h","limit":1}`
	}
	return model.Message{Role: "assistant", ToolCalls: []model.ToolCall{c}}, nil
}
func TestRepeatedReadLoopTerminatesWithOutcome(t *testing.T) {
	f := &loopFixture{}
	engine := Engine{Market: f, Charts: f, Model: f, Limits: core.Constraints{Equity: "1000", MaxRisk: "10", MaxNotional: "100", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}}
	events := []string{}
	err := engine.Run(context.Background(), Input{Message: "plan a trade", Venue: "binance", Symbol: "BTCUSDT", Interval: "1h"}, func(e Event) error { events = append(events, e.Type); return nil })
	if err != nil {
		t.Fatal(err)
	}
	// Six context reads (including pagination checks), then one additional raw request; duplicate requests
	// with reordered JSON must not fetch again or consume all eight model rounds.
	if f.reads != 7 || f.chats != 4 {
		t.Fatalf("unexpected loop: reads=%d chats=%d", f.reads, f.chats)
	}
	if len(events) < 2 || events[len(events)-1] != "outcome" {
		t.Fatal("missing terminal explanation", events)
	}
}

type stalledModel struct{}

func (stalledModel) Chat(ctx context.Context, _ []model.Message, _ []model.Tool) (model.Message, error) {
	<-ctx.Done()
	return model.Message{}, ctx.Err()
}
func TestStalledModelRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := chatWithProgress(ctx, stalledModel{}, nil, nil, func(Event) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
