package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"nofx/planning/core"
	"nofx/planning/model"
)

type repairDraftStore struct {
	textDraftFixture
	revisions int
}

func (s *repairDraftStore) Revise(_ string, plans []core.Plan, _ time.Time) error {
	s.revisions++
	return s.Add(plans)
}
func TestPartialProposalCanRepairRejectedStructuresWithoutDuplicates(t *testing.T) {
	source := textMarketFixture{}
	store := &repairDraftStore{}
	engine := Engine{Market: source, Store: store}
	limits := core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}
	input := Input{proposals: &proposalState{saved: map[string]string{}}}
	selected := &core.Record{State: "draft", Plan: core.Plan{ID: "original"}}
	limit := core.Idea{Name: "Limit", Side: "long", Entry: "1000", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: "limit"}}
	ladder := limit
	ladder.Name = "Ladder"
	ladder.Execution = &core.Execution{Kind: "ladder", Levels: []core.Level{{Price: "998", Weight: "0.5"}, {Price: "1020", Weight: "0.5"}}}
	twap := limit
	twap.Name = "TWAP"
	twap.Execution = &core.Execution{Kind: "twap", Slices: 2, IntervalSeconds: 30}
	proposed := false
	var events []Event
	invoke := func(ideas ...core.Idea) (map[string]any, error) {
		args, _ := json.Marshal(map[string]any{"ideas": ideas})
		call := model.ToolCall{}
		call.Function.Name = "propose_plans"
		call.Function.Arguments = string(args)
		value, err := engine.invoke(context.Background(), input, limits, selected, call, &proposed, func(event Event) error { events = append(events, event); return nil })
		if err != nil {
			return nil, err
		}
		return value.(map[string]any), nil
	}
	first, err := invoke(limit, ladder, twap)
	if err != nil || len(first["plans"].([]core.Plan)) != 2 || len(first["rejected"].([]string)) != 1 {
		t.Fatalf("partial batch: %v %+v", err, first)
	}
	ladder.Execution.Levels[1].Price = "1000"
	second, err := invoke(ladder, limit)
	if err != nil || len(second["plans"].([]core.Plan)) != 1 || len(store.plans) != 3 || store.revisions != 1 {
		t.Fatalf("repair failed: %v %+v store=%+v", err, second, store)
	}
	limit.Name = "Renamed duplicate"
	limit.Rationale = "Different wording"
	third, err := invoke(limit)
	if err != nil || len(third["plans"].([]core.Plan)) != 0 || len(store.plans) != 3 || third["no_trade"] != false {
		t.Fatal("duplicate resaved or accepted drafts lost", err, third)
	}
	if fourth, err := invoke(twap); err != nil || len(fourth["plans"].([]core.Plan)) != 0 {
		t.Fatal("a later correction must use the existing turn budget, without resaving duplicates", err)
	}
	for _, plan := range store.plans {
		if plan.Constraints != limits {
			t.Fatal("risk limits changed")
		}
	}
	rejectedEvents := 0
	for _, event := range events {
		if event.Type == "candidate_result" && event.Data.(map[string]any)["status"] == "rejected" {
			rejectedEvents++
		}
	}
	if rejectedEvents != 1 {
		t.Fatal("rejection was not streamed", rejectedEvents)
	}
}

type lateRepairModel struct{ calls int }

func (f *lateRepairModel) Chat(_ context.Context, _ []model.Message, tools []model.Tool) (model.Message, error) {
	f.calls++
	if len(tools) == 0 {
		return model.Message{Role: "assistant", Content: "Saved one validated draft. Other proposals failed risk checks."}, nil
	}
	target := "1005"
	if f.calls >= 4 {
		target = "1040"
	}
	args, _ := json.Marshal(map[string]any{"ideas": []core.Idea{{Name: "Corrected idea", Side: "long", Entry: "1000", Stop: "990", Target: core.Decimal(target), Execution: &core.Execution{Kind: "limit"}}}})
	call := model.ToolCall{ID: fmt.Sprintf("proposal_%d", f.calls), Type: "function"}
	call.Function.Name = "propose_plans"
	call.Function.Arguments = string(args)
	return model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, nil
}
func TestProposalCorrectionUsesExistingTurnBudgetBeyondThreeAttempts(t *testing.T) {
	source := textMarketFixture{}
	store := &textDraftFixture{}
	planner := &lateRepairModel{}
	engine := Engine{Model: planner, Store: store, Market: source, Charts: source, Budget: Budget{MaxRounds: 5, MaxToolCalls: 5}, Limits: core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}}
	var last Event
	if err := engine.Run(context.Background(), Input{Message: "Compare trade plans", Venue: "binance", Symbol: "BTCUSDT", Interval: "1h"}, func(event Event) error { last = event; return nil }); err != nil {
		t.Fatal(err)
	}
	if planner.calls != 5 || len(store.plans) != 1 || last.Type != "outcome" {
		t.Fatalf("correction was cut off or exceeded budget: calls=%d plans=%d last=%+v", planner.calls, len(store.plans), last)
	}
}

type incompleteComparisonModel struct {
	calls            int
	sawMissingMarket bool
}

func (f *incompleteComparisonModel) Chat(_ context.Context, messages []model.Message, _ []model.Tool) (model.Message, error) {
	f.calls++
	if f.calls == 2 || f.calls >= 4 {
		return model.Message{Role: "assistant", Content: "Drafts saved."}, nil
	}
	kinds := []string{"limit", "ladder", "twap"}
	if f.calls == 3 {
		f.sawMissingMarket = strings.Contains(messages[len(messages)-1].Content, "missing structures with propose_plans: market")
		kinds = []string{"market"}
	}
	ideas := []core.Idea{}
	for _, kind := range kinds {
		idea := core.Idea{Name: kind, Side: "long", Entry: "1000", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: kind}}
		if kind == "ladder" {
			idea.Execution.Levels = []core.Level{{Price: "998", Weight: "0.5"}, {Price: "1000", Weight: "0.5"}}
		}
		if kind == "twap" {
			idea.Execution.Slices = 2
			idea.Execution.IntervalSeconds = 30
		}
		ideas = append(ideas, idea)
	}
	raw, _ := json.Marshal(map[string]any{"ideas": ideas})
	call := model.ToolCall{ID: fmt.Sprintf("comparison_%d", f.calls), Type: "function"}
	call.Function.Name = "propose_plans"
	call.Function.Arguments = string(raw)
	return model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, nil
}
func TestFourWayComparisonCompletesMissingMarketWithinSameTurn(t *testing.T) {
	for _, compare := range []bool{true, false} {
		store := &textDraftFixture{}
		source := textMarketFixture{}
		planner := &incompleteComparisonModel{}
		engine := Engine{Model: planner, Store: store, Market: source, Charts: source, Limits: core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}}
		err := engine.Run(context.Background(), Input{CompareStructures: compare, Message: "Plan entry alternatives", Venue: "binance", Symbol: "BTCUSDT", Interval: "1h"}, func(Event) error { return nil })
		want := 3
		calls := 2
		if compare {
			want = 4
			calls = 4
		}
		if err != nil || len(store.plans) != want || planner.calls != calls || planner.sawMissingMarket != compare {
			t.Fatalf("compare=%v plans=%d calls=%d missing=%v err=%v", compare, len(store.plans), planner.calls, planner.sawMissingMarket, err)
		}
	}
}
