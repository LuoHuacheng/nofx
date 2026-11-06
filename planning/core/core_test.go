package core

import (
	"testing"
	"time"
)

func fixture() (Snapshot, Constraints, Idea, time.Time) {
	n := time.Now().UTC()
	return Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "spot", Bid: "99999", Ask: "100000", BidSize: "100", AskSize: "100", Tick: "1", Step: "0.00001", MinQty: "0.00001", MinNotional: "10", Observed: n}, Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}, Idea{Name: "test", Side: "long", Entry: "100000", Stop: "99000", Target: "104000"}, n
}
func compiled(t *testing.T) (Plan, Snapshot, time.Time) {
	t.Helper()
	s, c, i, n := fixture()
	p, e := Compile(s, c, i, n)
	if e != nil {
		t.Fatal(e)
	}
	return p, s, n
}
func TestExactRiskAndLimits(t *testing.T) {
	p, _, _ := compiled(t)
	if must(p.Risk).Cmp(must(p.Constraints.MaxRisk)) > 0 {
		t.Fatal("risk exceeds budget")
	}
	if !aligned(must(p.Quantity), must(p.Snapshot.Step)) {
		t.Fatal("bad step")
	}
	if p.Quantity != "0.009980000000" {
		t.Fatalf("unexpected conservative size %s", p.Quantity)
	}
	if must(p.RewardRisk).Cmp(must("2")) < 0 {
		t.Fatal("bad net reward/risk")
	}
	if must(p.Quantity).Cmp(div(must(p.Snapshot.AskSize), must("10"))) > 0 {
		t.Fatal("bad liquidity")
	}
}

func TestLeverageLockedAndStopMarginGuard(t *testing.T) {
	s, c, i, now := fixture()
	s.Kind = "perpetual"
	c.Leverage = 5
	p, e := Compile(s, c, i, now)
	if e != nil {
		t.Fatal(e)
	}
	p.Constraints.Leverage = 10
	if p.Verify(now) == nil {
		t.Fatal("changed leverage accepted")
	}
	c.Leverage = 20
	i.Stop = "95000"
	i.Target = "120000"
	if _, e = Compile(s, c, i, now); e == nil {
		t.Fatal("stop beyond margin guard allowed")
	}
}
func TestFailClosedPlans(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Snapshot, *Constraints, *Idea)
	}{
		{"stale", func(s *Snapshot, c *Constraints, i *Idea) { s.Observed = s.Observed.Add(-16 * time.Second) }},
		{"crossed", func(s *Snapshot, c *Constraints, i *Idea) { s.Bid = "100001" }},
		{"spot short", func(s *Snapshot, c *Constraints, i *Idea) { i.Side = "short" }},
		{"zero risk", func(s *Snapshot, c *Constraints, i *Idea) { c.MaxRisk = "0" }},
		{"NaN", func(s *Snapshot, c *Constraints, i *Idea) { i.Entry = "NaN" }},
		{"scientific", func(s *Snapshot, c *Constraints, i *Idea) { i.Entry = "1e5" }},
		{"bad tick", func(s *Snapshot, c *Constraints, i *Idea) { i.Entry = "100000.1" }},
		{"bad stop", func(s *Snapshot, c *Constraints, i *Idea) { i.Stop = "101000" }},
		{"bad reward", func(s *Snapshot, c *Constraints, i *Idea) { i.Target = "100100" }},
		{"too small", func(s *Snapshot, c *Constraints, i *Idea) { c.MaxRisk = "0.0001" }},
		{"tiny liquidity", func(s *Snapshot, c *Constraints, i *Idea) { s.AskSize = "0.00001" }},
		{"leverage", func(s *Snapshot, c *Constraints, i *Idea) { c.MaxNotional = "10001" }},
		{"overprecision", func(s *Snapshot, c *Constraints, i *Idea) { s.Step = "0.0000000000001" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, c, i, n := fixture()
			tc.change(&s, &c, &i)
			if _, e := Compile(s, c, i, n); e == nil {
				t.Fatal("accepted invalid plan")
			}
		})
	}
}
func TestIntegrityAndExpiry(t *testing.T) {
	p, _, n := compiled(t)
	if e := p.Verify(n); e != nil {
		t.Fatal(e)
	}
	q := p
	q.Quantity = "1"
	if q.Verify(n) == nil {
		t.Fatal("tamper accepted")
	}
	if p.Verify(p.Expires) == nil {
		t.Fatal("expiry accepted")
	}
}

func TestFiniteEntryAllocationsAndHash(t *testing.T) {
	for _, kind := range []string{"limit", "ladder", "twap"} {
		t.Run(kind, func(t *testing.T) {
			s, c, i, n := fixture()
			i.Execution = &Execution{Kind: kind}
			if kind == "ladder" {
				i.Execution.Levels = []Level{{Price: "100000", Weight: "0.4"}, {Price: "99800", Weight: "0.35"}, {Price: "99600", Weight: "0.25"}}
			}
			if kind == "twap" {
				i.Execution.Slices = 4
				i.Execution.IntervalSeconds = 30
			}
			p, e := Compile(s, c, i, n)
			if e != nil {
				t.Fatal(e)
			}
			if e = p.Verify(n); e != nil {
				t.Fatal(e)
			}
			total := must("0")
			for _, o := range p.Orders {
				total = add(total, must(o.Quantity))
				if !aligned(must(o.Quantity), must(s.Step)) {
					t.Fatal("child rounding")
				}
			}
			if total.Cmp(must(p.Quantity)) != 0 {
				t.Fatal("allocation lost quantity")
			}
			p.Orders[0].Price = "1"
			if p.Verify(n) == nil {
				t.Fatal("mutated basket accepted")
			}
		})
	}
}
