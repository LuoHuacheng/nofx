package core

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

// Historical plan produced before the funding reserve existed. Its digest must
// stay byte-identical: the new field is omitempty and zero means no reserve.
const legacyPlanJSON = `{"id":"a92616b846c61846","hash":"a92616b846c61846665ace91348aa77b5becb23b6d7d5b44faadb8e8a43a6f61","snapshot":{"venue":"binance","symbol":"BTCUSDT","kind":"perpetual","bid":"99999","ask":"100000","bid_size":"100","ask_size":"100","tick":"1","step":"0.001","min_qty":"0.001","min_notional":"10","observed":"2026-11-01T12:00:00Z","source_time":"0001-01-01T00:00:00Z"},"constraints":{"leverage":5,"equity":"100","max_risk":"30","max_notional":"492.610837438424","min_reward_risk":"2","fee_bps":"5","slippage_bps":"10"},"idea":{"name":"legacy","side":"long","entry":"100000","stop":"99800","target":"106000","rationale":""},"quantity":"0.004000000000","sizing_limit":"budget","risk":"1.998800000000","reward":"22.764000000000","reward_risk":"11.388833299980","created":"2026-11-01T12:00:00Z","expires":"2026-11-01T12:01:00Z"}`

const legacyPlanHash = "a92616b846c61846665ace91348aa77b5becb23b6d7d5b44faadb8e8a43a6f61"

func legacyPlan(t *testing.T) Plan {
	t.Helper()
	var p Plan
	if err := json.Unmarshal([]byte(legacyPlanJSON), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFundingReserveLegacyDigest(t *testing.T) {
	p := legacyPlan(t)
	if p.Hash != legacyPlanHash {
		t.Fatalf("historical digest changed: %s", p.Hash)
	}
	if got := p.Digest(); got != legacyPlanHash {
		t.Fatalf("historical digest changed: %s", got)
	}
	if err := p.Verify(time.Date(2026, 11, 1, 12, 0, 30, 0, time.UTC)); err != nil {
		t.Fatalf("historical plan no longer verifies: %v", err)
	}
	if p.Constraints.FundingReserveBPS != "" {
		t.Fatalf("legacy plan gained a funding reserve: %q", p.Constraints.FundingReserveBPS)
	}
	encoded, err := json.Marshal(p.Constraints)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["funding_reserve_bps"]; ok {
		t.Fatal("zero funding reserve must be omitted so historical digests survive")
	}
	reserved := p
	reserved.Constraints.FundingReserveBPS = "10"
	if reserved.Digest() == legacyPlanHash {
		t.Fatal("a plan with a funding reserve produced the historical digest")
	}
}

func TestFundingReserveChargesBothSides(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s := Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual", Bid: "99", Ask: "100", BidSize: "100", AskSize: "100", Tick: "1", Step: "0.001", MinQty: "0.001", MinNotional: "10", Observed: now}
	c := Constraints{Leverage: 3, Equity: "100", MaxRisk: "0.5", MaxNotional: "300", MinRewardRisk: "2", FeeBPS: "5", SlippageBPS: "10", FundingReserveBPS: "10"}
	i := Idea{Name: "funding", Side: "long", Entry: "100", Stop: "98", Target: "106"}
	p, err := Compile(s, c, i, now)
	if err != nil {
		t.Fatal(err)
	}
	// Per-unit loss = 2 + (100+98)*15bps + 100*10bps = 2.397; 0.208 units.
	if must(p.Risk).Cmp(big.NewRat(498576, 1000000)) != 0 {
		t.Fatalf("risk=%s, want 0.498576 including the funding reserve", p.Risk)
	}
	// Per-unit gain = 6 - (100+106)*15bps - 100*10bps = 5.591; net RR 5.591/2.397.
	if p.RewardRisk != "2.332498957030" {
		t.Fatalf("reward/risk=%s, want 2.332498957030 after the funding reserve", p.RewardRisk)
	}
	withoutReserve := c
	withoutReserve.FundingReserveBPS = ""
	looser, err := Compile(s, withoutReserve, i, now)
	if err != nil {
		t.Fatal(err)
	}
	if must(looser.RewardRisk).Cmp(must(p.RewardRisk)) <= 0 {
		t.Fatalf("funding reserve did not reduce net reward/risk: %s vs %s", p.RewardRisk, looser.RewardRisk)
	}
	if must(looser.Quantity).Cmp(must(p.Quantity)) <= 0 {
		t.Fatalf("funding reserve did not reduce the risk-sized quantity: %s vs %s", p.Quantity, looser.Quantity)
	}
	if p.Hash == looser.Hash {
		t.Fatal("funding reserve is missing from the plan digest")
	}
	if err := p.Verify(now.Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestFundingReserveRejectsOutOfRange(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s := Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual", Bid: "99", Ask: "100", BidSize: "100", AskSize: "100", Tick: "1", Step: "0.001", MinQty: "0.001", MinNotional: "10", Observed: now}
	i := Idea{Name: "funding", Side: "long", Entry: "100", Stop: "98", Target: "106"}
	for _, value := range []Decimal{"-1", "1001", "abc"} {
		c := Constraints{Leverage: 3, Equity: "100", MaxRisk: "0.5", MaxNotional: "300", MinRewardRisk: "2", FeeBPS: "5", SlippageBPS: "10", FundingReserveBPS: value}
		if _, err := Compile(s, c, i, now); err == nil {
			t.Fatalf("funding reserve %q accepted", value)
		}
	}
}
