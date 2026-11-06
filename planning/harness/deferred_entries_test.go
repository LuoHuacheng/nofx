package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nofx/planning/core"
	"nofx/planning/model"
)

type pullbackComparisonModel struct {
	calls                      int
	receivedAssessmentFeedback bool
}

func (m *pullbackComparisonModel) Chat(_ context.Context, messages []model.Message, _ []model.Tool) (model.Message, error) {
	m.calls++
	if m.calls > 2 {
		return model.Message{Role: "assistant", Content: "Two pullback drafts saved; market and TWAP deferred. Nothing executed."}, nil
	}
	args := map[string]any{"ideas": []core.Idea{}}
	if m.calls == 2 {
		for _, message := range messages {
			m.receivedAssessmentFeedback = m.receivedAssessmentFeedback || message.Role == "tool" && strings.Contains(message.Content, "Assess AI500 entry types independently")
		}
		args["ideas"] = []core.Idea{
			{Name: "Pullback limit", Side: "long", Entry: "998", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: "limit"}},
			{Name: "Pullback ladder", Side: "long", Entry: "998", Stop: "990", Target: "1040", Execution: &core.Execution{Kind: "ladder", Levels: []core.Level{{Price: "996", Weight: "0.5"}, {Price: "998", Weight: "0.5"}}}},
		}
		args["deferred"] = []deferredEntry{
			{Kind: "market", Reason: "Immediate entry is extended; reassess after a pullback."},
			{Kind: "twap", Reason: "Timed releases do not wait for a support retest; prefer the price-based pullback orders."},
		}
	}
	raw, _ := json.Marshal(args)
	call := model.ToolCall{ID: fmt.Sprintf("pullback_%d", m.calls), Type: "function"}
	call.Function.Name, call.Function.Arguments = "propose_plans", string(raw)
	return model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, nil
}

func TestAI500EmptyComparisonCanRecoverWithPullbackDraftsAndDeferredEntries(t *testing.T) {
	store := &textDraftFixture{}
	source := textMarketFixture{}
	planner := &pullbackComparisonModel{}
	engine := Engine{Store: store, Model: planner, Market: source, Charts: source, Limits: core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}}
	assessments := map[string]string{}
	var final outcome
	err := engine.Run(context.Background(), Input{Message: "Plan AI500 long entries", Venue: "binance", Symbol: "BTCUSDT", MarketGroup: "ai500", Interval: "1h", CompareStructures: true}, func(event Event) error {
		if event.Type == "candidate_result" {
			data := event.Data.(map[string]any)
			assessments[data["kind"].(string)] = data["status"].(string)
		}
		if event.Type == "outcome" {
			final = event.Data.(outcome)
		}
		return nil
	})
	if err != nil || len(store.plans) != 2 || planner.calls != 3 || !planner.receivedAssessmentFeedback {
		t.Fatalf("err=%v drafts=%d calls=%d feedback=%v", err, len(store.plans), planner.calls, planner.receivedAssessmentFeedback)
	}
	if len(assessments) != 4 || assessments["market"] != "deferred" || assessments["twap"] != "deferred" || final.Waiting || final.Drafts != 2 || final.StopReason != "completed" {
		t.Fatalf("incorrect comparison: %v final=%+v", assessments, final)
	}
	for _, plan := range store.plans {
		if plan.Idea.Side != "long" || plan.Constraints.EntryPolicy != core.EntryAI500TrendLong || (plan.Idea.Execution.Kind != "limit" && plan.Idea.Execution.Kind != "ladder") {
			t.Fatal("saved an unsupported entry")
		}
	}
}

func TestDeferredEntriesDoNotSaveInvalidOrConflictingAssessments(t *testing.T) {
	for _, raw := range []string{
		`{"ideas":[],"deferred":[{"kind":"stop_market","reason":"Watch"}]}`,
		`{"ideas":[],"deferred":[{"kind":"market","reason":"Watch"},{"kind":"market","reason":"Other"}]}`,
		`{"ideas":[],"deferred":[{"kind":"limit","reason":" "}]}`,
		`{"ideas":[{"side":"long","execution":{"kind":"limit"}}],"deferred":[{"kind":"limit","reason":"Watch"}]}`,
	} {
		store := &textDraftFixture{}
		engine := Engine{Store: store, Market: textMarketFixture{}}
		call := model.ToolCall{}
		call.Function.Name, call.Function.Arguments = "propose_plans", raw
		proposed := false
		_, err := engine.invoke(context.Background(), Input{}, core.Constraints{}, nil, call, &proposed, func(Event) error { return nil })
		if err == nil || proposed || len(store.plans) != 0 {
			t.Fatalf("invalid assessment accepted: %v", err)
		}
	}
}
