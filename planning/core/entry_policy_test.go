package core

import (
	"strings"
	"testing"
)

func TestAI500LongOnlyAcrossEveryEntryStructure(t *testing.T) {
	for _, kind := range []string{"market", "limit", "ladder", "twap"} {
		t.Run(kind, func(t *testing.T) {
			s, limits, idea, now := fixture()
			s.Kind = "perpetual"
			limits.EntryPolicy = EntryAI500TrendLong
			idea.Execution = &Execution{Kind: kind}
			if kind == "ladder" {
				idea.Execution.Levels = []Level{{Price: "100000", Weight: "0.5"}, {Price: "99900", Weight: "0.5"}}
			}
			if kind == "twap" {
				idea.Execution.Slices, idea.Execution.IntervalSeconds = 4, 30
			}
			plan, err := Compile(s, limits, idea, now)
			if err != nil || plan.Verify(now) != nil {
				t.Fatalf("valid long failed: %v", err)
			}
			plan.Constraints.EntryPolicy = ""
			if plan.Verify(now) == nil {
				t.Fatal("entry policy can be removed from a signed plan")
			}
			idea.Side, idea.Stop, idea.Target = "short", "101000", "96000"
			if _, err = Compile(s, limits, idea, now); err == nil || !strings.Contains(err.Error(), "long-only") {
				t.Fatalf("short entry escaped the policy: %v", err)
			}
			limits.EntryPolicy = ""
			if _, err = Compile(s, limits, idea, now); err != nil {
				t.Fatalf("unrestricted market was affected: %v", err)
			}
		})
	}
	_, limits, _, _ := fixture()
	limits.EntryPolicy = "unknown"
	if limits.Validate() == nil {
		t.Fatal("unknown entry policy accepted")
	}
}
