package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nofx/planning/core"
	"nofx/planning/model"
)

type ai500PolicyModel struct {
	calls     int
	wait      bool
	sawPolicy bool
}

func (m *ai500PolicyModel) Chat(_ context.Context, messages []model.Message, _ []model.Tool) (model.Message, error) {
	m.calls++
	for _, message := range messages {
		m.sawPolicy = m.sawPolicy || message.Role == "system" && strings.Contains(message.Content, "AI500 policy:")
	}
	if m.calls > 1 {
		return model.Message{Role: "assistant", Content: "Wait for a clear uptrend and a supported long entry."}, nil
	}
	ideas := []core.Idea{}
	deferred := []deferredEntry{}
	if m.wait {
		for _, kind := range []string{"market", "limit", "ladder", "twap"} {
			deferred = append(deferred, deferredEntry{Kind: kind, Reason: "The broader uptrend has broken; reassess after support is reclaimed."})
		}
	}
	if !m.wait {
		for _, kind := range []string{"market", "limit", "ladder", "twap"} {
			idea := core.Idea{Name: kind, Side: "short", Entry: "1000", Stop: "1010", Target: "960", Execution: &core.Execution{Kind: kind}}
			if kind == "ladder" {
				idea.Execution.Levels = []core.Level{{Price: "998", Weight: "0.5"}, {Price: "1000", Weight: "0.5"}}
			}
			if kind == "twap" {
				idea.Execution.Slices, idea.Execution.IntervalSeconds = 2, 30
			}
			ideas = append(ideas, idea)
		}
	}
	raw, _ := json.Marshal(map[string]any{"ideas": ideas, "deferred": deferred})
	call := model.ToolCall{ID: "candidate", Type: "function"}
	call.Function.Name, call.Function.Arguments = "propose_plans", string(raw)
	return model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, nil
}

func TestAI500PolicyBlocksShortsAndAllowsWaitingWithoutFillingCards(t *testing.T) {
	for _, wait := range []bool{false, true} {
		store := &textDraftFixture{}
		planner := &ai500PolicyModel{wait: wait}
		source := textMarketFixture{}
		engine := Engine{Store: store, Model: planner, Market: source, Charts: source, Limits: core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}}
		notice, rejected, completed, waiting := false, 0, false, false
		err := engine.Run(context.Background(), Input{Message: "Compare four entry structures", Venue: "binance", Symbol: "BTCUSDT", MarketGroup: "ai500", Interval: "1h", CompareStructures: true}, func(event Event) error {
			notice = notice || event.Type == "entry_policy"
			completed = completed || event.Type == "outcome"
			if event.Type == "outcome" && event.Data.(outcome).Waiting != wait {
				t.Fatal("wait decision was lost in the final outcome")
			}
			if event.Type == "plans" {
				waiting = event.Data.(map[string]any)["waiting"] == true
			}
			if event.Type == "candidate_result" && event.Data.(map[string]any)["status"] == "rejected" {
				rejected++
			}
			return nil
		})
		if err != nil || len(store.plans) != 0 || !notice || !planner.sawPolicy || !completed || planner.calls != 2 || waiting != wait {
			t.Fatalf("wait=%v err=%v drafts=%d notice=%v policy=%v outcome=%v calls=%d", wait, err, len(store.plans), notice, planner.sawPolicy, completed, planner.calls)
		}
		if !wait && rejected != 4 {
			t.Fatalf("not all short structures were rejected: %d", rejected)
		}
	}
}

func TestAI500TextGatewayPreservesPolicyWhenRevisingDrafts(t *testing.T) {
	store := &previewDrafts{previous: map[string]core.Record{}}
	source := textMarketFixture{}
	for turn := 0; turn < 3; turn++ {
		gateway := &textGatewayFixture{}
		engine := Engine{Store: store, Model: &model.NOFXAdapter{Client: gateway, TextTools: true}, Market: source, Charts: source, Limits: core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}}
		input := Input{Message: "Compare long entries", Venue: "binance", Symbol: "BTCUSDT", MarketGroup: "ai500", Interval: "1h", CompareStructures: true}
		if turn == 1 {
			previous := store.plans[0]
			previous.Constraints.EntryPolicy = ""
			store.previous[previous.ID] = core.Record{Plan: previous, State: "draft"}
			input.PlanID = previous.ID
			// The server may recognize membership even without a client hint.
			input.MarketGroup = ""
			engine.Limits.EntryPolicy = core.EntryAI500TrendLong
		}
		if turn == 2 {
			// An expired board cache must not strip a saved draft's policy when
			// a new account observation switches sizing to an equity preview.
			previous := store.plans[0]
			store.previous[previous.ID] = core.Record{Plan: previous, State: "draft"}
			input.PlanID, input.MarketGroup = previous.ID, ""
			engine.Limits.PlanningOnly = true
			engine.Limits.SizingPolicy = core.SizingAccountMargin
			engine.Limits.AvailableMargin = "0"
		}
		before := len(store.plans)
		if err := engine.Run(context.Background(), input, func(Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if len(store.plans)-before != 4 || gateway.calls != 2 {
			t.Fatalf("turn=%d saved=%d calls=%d", turn, len(store.plans)-before, gateway.calls)
		}
		for _, plan := range store.plans[before:] {
			if plan.Constraints.EntryPolicy != core.EntryAI500TrendLong || plan.Idea.Side != "long" {
				t.Fatal("AI500 entry policy lost during planning")
			}
			if turn == 2 && (!plan.Constraints.PlanningOnly || plan.Constraints.AvailableMargin != "0") {
				t.Fatal("saved entry policy replaced fresh account sizing")
			}
		}
	}
}
