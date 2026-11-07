package backtest

import (
	"testing"
	"time"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

func managedFunding(id, amount, currency string, at time.Time) execution.AccountEvent {
	return execution.AccountEvent{
		ID:         id,
		Kind:       risk.KindFunding,
		Amount:     execution.Decimal(amount),
		Currency:   currency,
		OccurredAt: at,
	}
}

func TestManagedCostsExact(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	result, err := CalculateManagedCosts(ManagedCostInput{
		Side: "long", Quantity: "1", Entry: "100", Exit: "110",
		FeeBPS: "5", SlippageBPS: "0",
		Funding: []execution.AccountEvent{managedFunding("fund-1", "-0.01", "USDT", t0)},
	})
	if err != nil {
		t.Fatalf("calculate: %v", err)
	}
	for _, want := range []struct {
		name  string
		value execution.Decimal
		want  string
	}{
		{"gross", result.Gross, "10"},
		{"fees", result.Fees, "0.105"},
		{"slippage", result.Slippage, "0"},
		{"funding", result.Funding, "-0.01"},
		{"net", result.Net, "9.885"},
	} {
		if !execution.Equal(string(want.value), want.want) {
			t.Fatalf("%s is %s, want %s", want.name, want.value, want.want)
		}
	}
	if !result.Complete {
		t.Fatal("a USDT funding payment was reported incomplete")
	}

	// A re-read of the same venue event must not charge the trade twice.
	duplicated, err := CalculateManagedCosts(ManagedCostInput{
		Side: "long", Quantity: "1", Entry: "100", Exit: "110",
		FeeBPS: "5", SlippageBPS: "0",
		Funding: []execution.AccountEvent{
			managedFunding("fund-1", "-0.01", "USDT", t0),
			managedFunding("fund-1", "-0.01", "USDT", t0.Add(time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("calculate duplicate: %v", err)
	}
	if !execution.Equal(string(duplicated.Funding), "-0.01") || !execution.Equal(string(duplicated.Net), "9.885") {
		t.Fatalf("duplicate event double counted: funding=%s net=%s", duplicated.Funding, duplicated.Net)
	}
	if !duplicated.Complete {
		t.Fatal("a duplicate event was mistaken for an unknown movement")
	}

	// A settled funding charge in another coin cannot be added to USDT costs,
	// and must never be silently treated as USDT.
	foreign, err := CalculateManagedCosts(ManagedCostInput{
		Side: "long", Quantity: "1", Entry: "100", Exit: "110",
		FeeBPS: "5", SlippageBPS: "0",
		Funding: []execution.AccountEvent{managedFunding("fund-btc", "-0.01", "BTC", t0)},
	})
	if err != nil {
		t.Fatalf("calculate foreign funding: %v", err)
	}
	if foreign.Complete {
		t.Fatal("a non-USDT funding charge was reported as complete")
	}
	if !execution.Equal(string(foreign.Funding), "0") {
		t.Fatalf("non-USDT funding was converted without a rate: %s", foreign.Funding)
	}

	// An unknown currency is not proof of USDT either.
	unknown, err := CalculateManagedCosts(ManagedCostInput{
		Side: "long", Quantity: "1", Entry: "100", Exit: "110",
		FeeBPS: "5", SlippageBPS: "0",
		Funding: []execution.AccountEvent{managedFunding("fund-empty", "-0.01", "", t0)},
	})
	if err != nil {
		t.Fatalf("calculate unknown currency: %v", err)
	}
	if unknown.Complete {
		t.Fatal("an unknown funding currency was reported as complete")
	}
}

func TestManagedBarExitConservative(t *testing.T) {
	cases := []struct {
		name   string
		side   string
		stop   string
		target string
		open   string
		high   string
		low    string
		price  string
		reason string
	}{
		// The same bar touches both levels: the stop wins.
		{"long both levels", "long", "98", "110", "100", "115", "95", "98", "stop_loss"},
		// A gap through the stop fills at the open, not at the stop.
		{"long stop gap", "long", "98", "110", "95", "97", "94", "95", "stop_loss"},
		{"long target", "long", "98", "110", "100", "111", "99", "110", "take_profit"},
		{"long untouched", "long", "98", "110", "100", "105", "99", "0", ""},
		{"short both levels", "short", "102", "90", "100", "103", "89", "102", "stop_loss"},
		{"short stop gap", "short", "102", "90", "105", "106", "104", "105", "stop_loss"},
		{"short target", "short", "102", "90", "100", "101", "89", "90", "take_profit"},
		{"short untouched", "short", "102", "90", "100", "101", "95", "0", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			price, reason, err := ResolveManagedBarExit(c.side,
				execution.Decimal(c.stop), execution.Decimal(c.target), execution.Decimal(c.open),
				execution.Decimal(c.high), execution.Decimal(c.low))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if !execution.Equal(string(price), c.price) {
				t.Fatalf("exit price is %s, want %s", price, c.price)
			}
			if reason != c.reason {
				t.Fatalf("exit reason is %q, want %q", reason, c.reason)
			}
		})
	}
}

func TestManagedFundingIntervals(t *testing.T) {
	// The venue changed its funding cadence inside one trade: three settlements
	// one hour and five hours apart, at the venue's own timestamps.
	t0 := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	events := []execution.AccountEvent{
		managedFunding("fund-a", "-0.01", "USDT", t0),
		managedFunding("fund-b", "-0.02", "USDT", t0.Add(time.Hour)),
		managedFunding("fund-c", "0.005", "USDT", t0.Add(5*time.Hour)),
	}
	result, err := CalculateManagedCosts(ManagedCostInput{
		Side: "long", Quantity: "1", Entry: "100", Exit: "110",
		FeeBPS: "5", SlippageBPS: "0", Funding: events,
	})
	if err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if !execution.Equal(string(result.Funding), "-0.025") {
		t.Fatalf("funding is %s, want -0.025", result.Funding)
	}
	if !execution.Equal(string(result.Net), "9.87") {
		t.Fatalf("net is %s, want 9.87", result.Net)
	}
	if !result.Complete {
		t.Fatal("USDT funding settlements were reported incomplete")
	}
}
