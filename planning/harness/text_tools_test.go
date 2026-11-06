package harness

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nofx/mcp"
	"nofx/planning/core"
	"nofx/planning/market"
	"nofx/planning/model"
)

type textGatewayFixture struct {
	mcp.AIClient
	calls int
}

func (f *textGatewayFixture) CallWithRequestFull(req *mcp.Request) (*mcp.LLMResponse, error) {
	if len(req.Tools) > 0 || req.ToolChoice != "" {
		panic("text-only endpoint rejects native tool fields")
	}
	f.calls++
	if f.calls > 1 {
		return &mcp.LLMResponse{Content: `{"content":"Four drafts saved for review; none approved or executed.","tool_calls":[]}`}, nil
	}
	ideas := []core.Idea{
		{Name: "Market", Side: "long", Entry: "950", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: "market"}},
		{Name: "Limit", Side: "long", Entry: "1000", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: "limit"}},
		{Name: "Ladder", Side: "long", Entry: "1000", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: "ladder", Levels: []core.Level{{Price: "998", Weight: "0.5"}, {Price: "1000", Weight: "0.5"}}}},
		{Name: "TWAP", Side: "long", Entry: "1000", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: "twap", Slices: 2, IntervalSeconds: 30}},
	}
	raw, _ := json.Marshal(map[string]any{"content": "Validate alternatives", "tool_calls": []any{map[string]any{"name": "propose_plans", "arguments": map[string]any{"ideas": ideas}}}})
	return &mcp.LLMResponse{Content: string(raw)}, nil
}

type textMarketFixture struct{}

func (textMarketFixture) Snapshot(context.Context, string, string) (core.Snapshot, error) {
	return core.Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual", Bid: "999", Ask: "1000", BidSize: "100", AskSize: "100", Tick: "1", Step: "0.01", MinQty: "0.01", MinNotional: "10", Observed: time.Now().UTC()}, nil
}
func (textMarketFixture) Candles(_ context.Context, q market.CandleQuery) ([]market.Candle, error) {
	to := q.To
	if to == 0 {
		to = time.Now().Unix()
	}
	return []market.Candle{{Time: to - 3600, Open: "999", High: "1001", Low: "998", Close: "1000", Volume: "100"}}, nil
}

type textDraftFixture struct{ plans []core.Plan }

func (s *textDraftFixture) Records() map[string]core.Record { return map[string]core.Record{} }
func (s *textDraftFixture) Add(plans []core.Plan) error {
	s.plans = append(s.plans, plans...)
	return nil
}
func (s *textDraftFixture) Revise(_ string, plans []core.Plan, _ time.Time) error {
	return s.Add(plans)
}
func TestTextOnlyGatewayProducesFourValidatedEntryStructures(t *testing.T) {
	gateway := &textGatewayFixture{}
	store := &textDraftFixture{}
	source := textMarketFixture{}
	engine := Engine{Model: &model.NOFXAdapter{Client: gateway, TextTools: true}, Store: store, Market: source, Charts: source, Limits: core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}}
	var events []Event
	err := engine.Run(context.Background(), Input{CompareStructures: true, Message: "Compare entry structures", Venue: "binance", Symbol: "BTCUSDT", Interval: "1h"}, func(event Event) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(store.plans) != 4 || gateway.calls != 2 {
		t.Fatalf("plans=%d calls=%d events=%+v", len(store.plans), gateway.calls, events)
	}
	for index, kind := range []string{"market", "limit", "ladder", "twap"} {
		plan := store.plans[index]
		if plan.Idea.Execution.Kind != kind || plan.Hash == "" || len(plan.Orders) == 0 {
			t.Fatalf("invalid draft: %+v", plan)
		}
	}
	if events[len(events)-1].Type != "outcome" {
		t.Fatal("missing completed outcome")
	}
}
