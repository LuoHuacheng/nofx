package harness

import (
	"context"
	"nofx/planning/core"
	"nofx/planning/model"
	"testing"
)

func TestHarnessHasNoLiveSideEffectTool(t *testing.T) {
	allowed := map[string]bool{"get_market": true, "get_candles": true, "propose_plans": true, "draw_chart": true}
	for _, tool := range Tools() {
		if !allowed[tool.Function.Name] {
			t.Fatal("unexpected capability", tool.Function.Name)
		}
	}
	e := Engine{}
	proposed := false
	for _, name := range []string{"execute", "approve", "cancel", "shell", "place_order"} {
		var call model.ToolCall
		call.Function.Name = name
		call.Function.Arguments = `{}`
		if _, err := e.invoke(context.Background(), Input{}, core.Constraints{}, nil, call, &proposed, func(Event) error { return nil }); err == nil {
			t.Fatal("side effect exposed to AI", name)
		}
	}
}
func TestChartInputUsesChosenTimeframeAndRejectsInvalidRange(t *testing.T) {
	in := Input{Message: "plan", Venue: "binance", Symbol: "BTCUSDT", Interval: "4h", VisibleFrom: 100, VisibleTo: 200}
	if e := in.Validate(); e != nil {
		t.Fatal(e)
	}
	in.VisibleTo = 99
	if in.Validate() == nil {
		t.Fatal("backward visible range accepted")
	}
	in.VisibleFrom = 0
	in.VisibleTo = 0
	in.Interval = "unlisted"
	if in.Validate() == nil {
		t.Fatal("invalid timeframe accepted")
	}
}
