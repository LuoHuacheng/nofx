package core

import (
	"strings"
	"testing"
)

func TestMarketDepthUsesFullBookWithinApprovedBound(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		t.Run(side, func(t *testing.T) {
			s, c, i, now := fixture()
			s.Kind = "perpetual"
			c.SizingPolicy = SizingAccountMargin
			i.Side = side
			i.Execution = &Execution{Kind: "market"}
			s.BidSize, s.AskSize = "0.00001", "0.00001"
			s.Bids = []BookLevel{{s.Bid, s.BidSize}, {"99950", "10"}}
			s.Asks = []BookLevel{{s.Ask, s.AskSize}, {"100050", "10"}}
			if side == "short" {
				i.Stop, i.Target = "101000", "95000"
			}
			p, err := Compile(s, c, i, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = CheckBook(p, s, now); err != nil {
				t.Fatal(err)
			}
			if err = p.Verify(now); err != nil {
				t.Fatal(err)
			}
			if side == "long" {
				s.Asks[1].Price = "100101"
			} else {
				s.Bids[1].Price = "99899"
			}
			if err = CheckBook(p, s, now); err == nil || !strings.Contains(err.Error(), "Size was not reduced") {
				t.Fatal("depth outside approved bound counted", err)
			}
			if _, err = Compile(s, c, i, now); err == nil {
				t.Fatal("infeasible market draft saved")
			}
			s.Bids, s.Asks = nil, nil
			if err = CheckBook(p, s, now); err == nil {
				t.Fatal("missing depth fabricated")
			}
			// Resting orders do not need the whole basket on the current top quote.
			i.Execution.Kind = "limit"
			p, err = Compile(s, c, i, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = CheckBook(p, s, now); err != nil {
				t.Fatal("resting entry capped by top quote", err)
			}
			// Saved legacy plans retain their original liquidity check.
			p.Constraints.SizingPolicy = ""
			if err = CheckBook(p, s, now); err == nil {
				t.Fatal("legacy execution check changed")
			}
		})
	}
}

func TestRejectInvalidDepthAndSizingPolicy(t *testing.T) {
	for _, change := range []func(*Snapshot){
		func(s *Snapshot) { s.Asks[0].Quantity = "0" },
		func(s *Snapshot) { s.Asks[0].Quantity = "1" },
		func(s *Snapshot) { s.Asks[0].Price = "99999" },
		func(s *Snapshot) { s.Asks = append(s.Asks, s.Asks[0]) },
		func(s *Snapshot) { s.Asks = append(s.Asks, BookLevel{"99999", "1"}) },
		func(s *Snapshot) { s.Bids = []BookLevel{{s.Bid, s.BidSize}, {"100001", "1"}} },
		func(s *Snapshot) { s.Asks = append(s.Asks, BookLevel{"invalid", "1"}) },
		func(s *Snapshot) { s.Asks = make([]BookLevel, 101) },
	} {
		s, _, _, now := fixture()
		s.Asks = []BookLevel{{s.Ask, s.AskSize}}
		change(&s)
		if s.Validate(now) == nil {
			t.Fatal("invalid depth accepted")
		}
	}
	_, c, _, _ := fixture()
	c.SizingPolicy = "ignore_all_limits"
	if c.Validate() == nil {
		t.Fatal("unknown sizing policy accepted")
	}
}
