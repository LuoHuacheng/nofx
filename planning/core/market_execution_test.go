package core

import "testing"

func TestMarketUsesExecutableBookAndConservativeRisk(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		t.Run(side, func(t *testing.T) {
			s, c, i, now := fixture()
			s.Kind = "perpetual"
			i.Side = side
			i.Entry = "1"
			i.Execution = &Execution{Kind: "market"}
			reference, bound := s.Ask, Decimal("100100.000000000000")
			if side == "short" {
				i.Stop = "101000"
				i.Target = "95000"
				reference = s.Bid
				bound = "99900.000000000000"
			}
			p, err := Compile(s, c, i, now)
			if err != nil {
				t.Fatal(err)
			}
			if p.Idea.Entry != reference || len(p.Orders) != 1 || p.Orders[0].Price != bound || p.Orders[0].OffsetSeconds != 0 {
				t.Fatalf("wrong market intent: %+v", p)
			}
			if err = p.Verify(now); err != nil {
				t.Fatal(err)
			}
			estimate := i
			estimate.Execution = nil
			estimate.Entry = bound
			riskPlan, err := Compile(s, c, estimate, now)
			if err != nil || riskPlan.Risk != p.Risk || riskPlan.Quantity != p.Quantity {
				t.Fatal("market risk is not sized from the conservative execution estimate", err)
			}
			p.Idea.Execution.Kind = "limit"
			if p.Verify(now) == nil {
				t.Fatal("execution type changed without invalidating approval")
			}
		})
	}
}
func TestMarketRejectsInvalidContextAndUnsupportedExecution(t *testing.T) {
	for _, change := range []func(*Snapshot, *Constraints, *Idea){
		func(s *Snapshot, c *Constraints, i *Idea) { s.Venue = "unsupported" },
		func(s *Snapshot, c *Constraints, i *Idea) { s.Kind = "spot" },
		func(s *Snapshot, c *Constraints, i *Idea) { s.Ask = "invalid" },
		func(s *Snapshot, c *Constraints, i *Idea) { c.SlippageBPS = "invalid" },
		func(s *Snapshot, c *Constraints, i *Idea) { i.Execution.Slices = 2 },
		func(s *Snapshot, c *Constraints, i *Idea) { i.Stop = "100200" },
	} {
		s, c, i, now := fixture()
		s.Kind = "perpetual"
		i.Execution = &Execution{Kind: "market"}
		change(&s, &c, &i)
		if _, err := Compile(s, c, i, now); err == nil {
			t.Fatal("invalid market execution accepted")
		}
	}
}

func TestNewVenuesSupportAllEntryStructures(t *testing.T) {
	for _, venue := range []string{"okx", "bitget"} {
		for _, kind := range []string{"market", "limit", "ladder", "twap"} {
			t.Run(venue+kind, func(t *testing.T) {
				s, c, i, now := fixture()
				s.Venue = venue
				s.Kind = "perpetual"
				i.Execution = &Execution{Kind: kind}
				if venue == "okx" {
					s.Symbol = "BTC-USDT-SWAP"
					s.ContractValue = "0.01"
				}
				if kind == "ladder" {
					i.Execution.Levels = []Level{{Price: i.Entry, Weight: "0.5"}, {Price: "99900", Weight: "0.5"}}
				}
				if kind == "twap" {
					i.Execution.Slices = 4
					i.Execution.IntervalSeconds = 30
				}
				p, e := Compile(s, c, i, now)
				if e != nil {
					t.Fatal(e)
				}
				if e = p.Verify(now); e != nil {
					t.Fatal(e)
				}
			})
		}
	}
}
