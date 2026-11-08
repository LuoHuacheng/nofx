package automation

import (
	"context"
	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/planning/model"
	"nofx/trading/risk"
	"strings"
	"testing"
)

func TestModelSourceSuppliesExecutableCostContext(t *testing.T) {
	source, planner, _ := testSource(model.Message{Content: `{"ideas":[]}`}, testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars))
	policy := testPolicy()
	policy.MinRewardRisk = "2"
	policy.FundingReserveBPS = 10
	_, _ = source.Next(context.Background(), policy)
	prompt := planner.last[0].Content
	for _, want := range []string{"bid=100", "ask=100.1", "tick_size=0.1", "fee_bps=5", "slippage_bps=10", "funding_reserve_bps=10", "min_reward_risk=2", "abs(target-entry)", "abs(entry-stop)", "Do not invent"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("model lacks execution context %q", want)
		}
	}
}

func TestModelSourcePreservesServerSelectedSymbol(t *testing.T) {
	source, _, _ := testSource(model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}, testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars))
	s, err := source.Next(context.Background(), testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if s.Symbol != "BTCUSDT" {
		t.Fatal("signal loses its server-selected instrument")
	}
}

type instrumentMarket struct {
	book    core.Snapshot
	symbols []string
}

func (m *instrumentMarket) Snapshot(_ context.Context, _, symbol string) (core.Snapshot, error) {
	m.symbols = append(m.symbols, symbol)
	b := m.book
	b.Symbol = symbol
	if symbol == "ETHUSDT" {
		b.Bid = "1999"
		b.Ask = "2000"
	}
	return b, nil
}

func TestCompileIdeaKeepsOriginalInstrumentRejection(t *testing.T) {
	r := rig(t, nil)
	r.policy.Symbols = []string{"BTCUSDT", "ETHUSDT"}
	r.policy.Version = ""
	r.policy.Version, _ = risk.PolicyVersion(r.policy)
	m := &instrumentMarket{book: r.book}
	r.ctrl.Market = m
	rt := r.runtime(t, nil)
	limits, err := live.ManagedAccountLimits(r.policy, r.venue.snapshot, *r.clock)
	if err != nil {
		t.Fatal(err)
	}
	idea := r.idea()
	idea.Target = "100001"
	_, err = rt.compileIdea(context.Background(), r.policy, limits, Signal{Symbol: "BTCUSDT", Idea: idea})
	if err == nil || !strings.Contains(err.Error(), "target return does not cover") {
		t.Fatalf("original BTC rejection hidden: %v", err)
	}
	if len(m.symbols) != 1 || m.symbols[0] != "BTCUSDT" {
		t.Fatalf("tried unrelated instruments: %v", m.symbols)
	}
}

func TestCompileIdeaRejectsMissingOrUnlistedInstrument(t *testing.T) {
	r := rig(t, nil)
	m := &instrumentMarket{book: r.book}
	r.ctrl.Market = m
	rt := r.runtime(t, nil)
	for _, symbol := range []string{"", "ETHUSDT", "btcusdt"} {
		_, err := rt.compileIdea(context.Background(), r.policy, core.Constraints{}, Signal{Symbol: symbol, Idea: r.idea()})
		if err == nil || !strings.Contains(err.Error(), "not whitelisted") {
			t.Fatalf("instrument %q accepted: %v", symbol, err)
		}
	}
	if len(m.symbols) != 0 {
		t.Fatalf("unlisted instruments reached market feed: %v", m.symbols)
	}
}
