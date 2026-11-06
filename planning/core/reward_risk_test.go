package core

import "testing"

func TestRewardRiskMinimumIsOptionalForAllEntryStructures(t *testing.T) {
	for _, kind := range []string{"market", "limit", "ladder", "twap"} {
		t.Run(kind, func(t *testing.T) {
			s, c, idea, now := fixture()
			s.Kind = "perpetual"
			idea.Target = "102000"
			idea.Execution = &Execution{Kind: kind}
			if kind == "ladder" {
				idea.Execution.Levels = []Level{{Price: "100000", Weight: "0.5"}, {Price: "99900", Weight: "0.5"}}
			}
			if kind == "twap" {
				idea.Execution.Slices, idea.Execution.IntervalSeconds = 4, 30
			}
			if _, err := Compile(s, c, idea, now); err == nil {
				t.Fatal("explicitly configured minimum must remain enforced")
			}
			c.MinRewardRisk = "0"
			p, err := Compile(s, c, idea, now)
			if err != nil {
				t.Fatal(err)
			}
			if !Positive(p.RewardRisk) || must(p.RewardRisk).Cmp(must("2")) >= 0 {
				t.Fatalf("actual ratio not reported: %s", p.RewardRisk)
			}
			if err := p.Verify(now); err != nil {
				t.Fatal(err)
			}
			p.Constraints.MinRewardRisk = "2"
			if p.Verify(now) == nil {
				t.Fatal("a signed plan's constraints cannot be changed")
			}
		})
	}
}

func TestRewardRiskMinimumValidation(t *testing.T) {
	s, c, idea, now := fixture()
	for _, invalid := range []Decimal{"", "-1", "NaN", "1e1"} {
		c.MinRewardRisk = invalid
		if c.Validate() == nil {
			t.Fatalf("invalid minimum accepted: %s", invalid)
		}
	}
	c.MinRewardRisk = "0"
	idea.Target = "100100"
	if _, err := Compile(s, c, idea, now); err == nil {
		t.Fatal("target does not cover costs")
	}
}
