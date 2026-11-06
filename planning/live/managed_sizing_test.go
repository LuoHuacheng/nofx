package live

import (
	"math/big"
	"nofx/planning/core"
	"nofx/trader/execution"
	"nofx/trading/risk"
	"strings"
	"testing"
	"time"
)

var managedNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func managedPolicy() risk.Policy {
	p := risk.DefaultPolicy("alice", "binance:main", "portfolio-main", "binance", execution.EnvironmentSandbox)
	p.Symbols = []string{"BTCUSDT"}
	p.Version, _ = risk.PolicyVersion(p)
	return p
}

func managedAccount(now time.Time) execution.ManagedAccount {
	return execution.ManagedAccount{AccountKey: "binance:main", Venue: "binance", Environment: execution.EnvironmentSandbox, PositionMode: "one_way", MarginMode: "isolated", Equity: "100", Available: "100", MarginUsed: "0", ObservedAt: now}
}

func managedSnapshot(now time.Time) core.Snapshot {
	return core.Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual", Bid: "99", Ask: "100", BidSize: "100", AskSize: "100", Tick: "1", Step: "0.001", MinQty: "0.001", MinNotional: "10", Observed: now}
}

func managedLimits(t *testing.T) core.Constraints {
	t.Helper()
	c, err := ManagedAccountLimits(managedPolicy(), managedAccount(managedNow), managedNow)
	if err != nil {
		t.Fatal(err)
	}
	if c.SizingPolicy != "" {
		t.Fatalf("managed sizing must not reuse account-margin sizing: %q", c.SizingPolicy)
	}
	if c.Leverage != 3 || c.MinRewardRisk != "2" || c.FundingReserveBPS != "10" {
		t.Fatalf("managed constraints = %d/%s/%q", c.Leverage, c.MinRewardRisk, c.FundingReserveBPS)
	}
	return c
}

func TestManagedSizingUsesStopRisk(t *testing.T) {
	c := managedLimits(t)
	limit, err := execution.Number(string(c.MaxRisk))
	if err != nil {
		t.Fatal(err)
	}
	if limit.Cmp(big.NewRat(1, 2)) != 0 {
		t.Fatalf("max risk=%s, want 0.5 USDT", c.MaxRisk)
	}
	p, err := core.Compile(managedSnapshot(managedNow), c, core.Idea{Name: "managed", Side: "long", Entry: "100", Stop: "98", Target: "106"}, managedNow)
	if err != nil {
		t.Fatal(err)
	}
	loss, err := execution.Number(string(p.Risk))
	if err != nil {
		t.Fatal(err)
	}
	if loss.Cmp(big.NewRat(1, 2)) > 0 {
		t.Fatalf("risk=%s exceeds 0.5 USDT", p.Risk)
	}
	if loss.Cmp(big.NewRat(498576, 1000000)) != 0 {
		t.Fatalf("risk=%s, want 0.498576 (0.208 * 2.397)", p.Risk)
	}
	q, err := execution.Number(string(p.Quantity))
	if err != nil {
		t.Fatal(err)
	}
	if q.Cmp(big.NewRat(208, 1000)) != 0 {
		t.Fatalf("quantity=%s, want 0.208 (floored to the 0.001 step)", p.Quantity)
	}
	if !execution.Aligned(string(p.Quantity), "0.001") {
		t.Fatalf("quantity %s is not step aligned", p.Quantity)
	}
	if q.Cmp(big.NewRat(4918, 1000)) == 0 {
		t.Fatal("managed sizing still produced the account-margin quantity 4.918")
	}
	if p.SizingLimit != "risk" {
		t.Fatalf("sizing limit=%q, want risk", p.SizingLimit)
	}
	policy := managedPolicy()
	if err := VerifyManagedBudget(p, policy); err != nil {
		t.Fatal(err)
	}
	oversized := p
	oversized.Risk = "0.6"
	if err := VerifyManagedBudget(oversized, policy); err == nil {
		t.Fatal("oversized plan passed the managed budget check")
	}
	inflated := p
	inflated.Constraints.MaxRisk = "1"
	inflated.Constraints.MaxNotional = "1000"
	if err := VerifyManagedBudget(inflated, policy); err == nil {
		t.Fatal("inflated constraints passed the managed budget check")
	}
	other := p
	other.Snapshot.Symbol = "ETHUSDT"
	if err := VerifyManagedBudget(other, policy); err == nil {
		t.Fatal("symbol outside the policy whitelist passed the managed budget check")
	}
	malformed := p
	malformed.Quantity = "1.0.2"
	if err := VerifyManagedBudget(malformed, policy); err == nil {
		t.Fatal("malformed quantity passed the managed budget check")
	}
}

func TestManagedSizingRejectsWeakRewardRisk(t *testing.T) {
	c := managedLimits(t)
	s := managedSnapshot(managedNow)
	s.Tick = "0.1"
	_, err := core.Compile(s, c, core.Idea{Name: "weak", Side: "long", Entry: "100", Stop: "98", Target: "100.5"}, managedNow)
	if err == nil {
		t.Fatal("net reward/risk 0.0414 was accepted")
	}
	if !strings.Contains(err.Error(), "Net reward/risk") {
		t.Fatalf("weak reward/risk rejected for the wrong reason: %v", err)
	}
}

func TestManagedSizingNoMinimumUpRound(t *testing.T) {
	c := managedLimits(t)
	s := managedSnapshot(managedNow)
	s.MinQty = "0.5"
	_, err := core.Compile(s, c, core.Idea{Name: "min", Side: "long", Entry: "100", Stop: "98", Target: "106"}, managedNow)
	if err == nil {
		t.Fatal("quantity 0.208 below the venue minimum was rounded up")
	}
	if !strings.Contains(err.Error(), "below venue minimum") {
		t.Fatalf("minimum quantity rejected for the wrong reason: %v", err)
	}
}

func TestManagedAccountLimitsRejectsStaleSnapshot(t *testing.T) {
	a := managedAccount(managedNow.Add(-10 * time.Second))
	if _, err := ManagedAccountLimits(managedPolicy(), a, managedNow); err == nil {
		t.Fatal("stale managed snapshot was accepted")
	}
	a = managedAccount(managedNow.Add(5 * time.Second))
	if _, err := ManagedAccountLimits(managedPolicy(), a, managedNow); err == nil {
		t.Fatal("managed snapshot from the future was accepted")
	}
	a = managedAccount(managedNow)
	a.AccountKey = "okx:other"
	if _, err := ManagedAccountLimits(managedPolicy(), a, managedNow); err == nil {
		t.Fatal("snapshot for a different account was accepted")
	}
	a = managedAccount(managedNow)
	a.Available = "0"
	if _, err := ManagedAccountLimits(managedPolicy(), a, managedNow); err == nil {
		t.Fatal("managed sizing accepted an account without available margin")
	}
}
