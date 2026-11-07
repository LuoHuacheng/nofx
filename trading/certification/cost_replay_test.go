package certification

// Cost replay: the fixture produces a confirmed fill bill, the costing function
// prices that same bill, and the difference against the venue-side change is
// compared exactly. A non-zero difference stays visible; nothing here adjusts
// an amount to make the two sides meet.

import (
	"math/big"
	"testing"
	"time"

	"nofx/backtest"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// certDecimalAdd returns a+b as an execution decimal, or fails.
func certDecimalAdd(t *testing.T, a, b execution.Decimal) execution.Decimal {
	t.Helper()
	x, err := execution.Number(string(a))
	if err != nil {
		t.Fatalf("certification: %s is not a number", a)
	}
	y, err := execution.Number(string(b))
	if err != nil {
		t.Fatalf("certification: %s is not a number", b)
	}
	value, err := execution.ExactDecimal(new(big.Rat).Add(x, y))
	if err != nil {
		t.Fatalf("certification: cost sum lost precision: %v", err)
	}
	return value
}

// certDecimalSubtract returns a-b as an execution decimal, or fails.
func certDecimalSubtract(t *testing.T, a, b execution.Decimal) execution.Decimal {
	t.Helper()
	x, err := execution.Number(string(a))
	if err != nil {
		t.Fatalf("certification: %s is not a number", a)
	}
	y, err := execution.Number(string(b))
	if err != nil {
		t.Fatalf("certification: %s is not a number", b)
	}
	value, err := execution.ExactDecimal(new(big.Rat).Sub(x, y))
	if err != nil {
		t.Fatalf("certification: cost difference lost precision: %v", err)
	}
	return value
}

// certVenueEquityChange aggregates the venue's confirmed bill the way its own
// statement does: realised fill PnL minus the taker fee on both legs. The
// simulator reports fill prices and a commission rate, not a wallet balance, so
// this is the venue-side record available to reconcile against.
func certVenueEquityChange(t *testing.T, in backtest.ManagedCostInput) execution.Decimal {
	t.Helper()
	quantity, err := execution.Number(string(in.Quantity))
	if err != nil {
		t.Fatalf("certification: quantity %s is not a number", in.Quantity)
	}
	entry, err := execution.Number(string(in.Entry))
	if err != nil {
		t.Fatalf("certification: entry %s is not a number", in.Entry)
	}
	exit, err := execution.Number(string(in.Exit))
	if err != nil {
		t.Fatalf("certification: exit %s is not a number", in.Exit)
	}
	rate, err := execution.Number(string(in.FeeBPS))
	if err != nil {
		t.Fatalf("certification: fee rate %s is not a number", in.FeeBPS)
	}
	gross := new(big.Rat).Mul(new(big.Rat).Sub(exit, entry), quantity)
	fees := new(big.Rat).Quo(new(big.Rat).Mul(new(big.Rat).Add(entry, exit), new(big.Rat).Mul(quantity, rate)), big.NewRat(10_000, 1))
	value, err := execution.ExactDecimal(new(big.Rat).Sub(gross, fees))
	if err != nil {
		t.Fatalf("certification: venue change lost precision: %v", err)
	}
	return value
}

// confirmedBill reads the fill prices and quantities the fixture venue accepted
// and filled. Slippage is zero on purpose: a fill-based replay already carries
// the price the venue actually filled at, so adding a second slippage charge
// would count it twice.
func confirmedBill(t *testing.T, f *exchangeFixture) backtest.ManagedCostInput {
	t.Helper()
	entries := f.fake.byRole("entry")
	if len(entries) != 1 || entries[0].filled.Sign() <= 0 {
		t.Fatalf("%s: venue holds %d confirmed entry fills", f.venue, len(entries))
	}
	exits := f.fake.byRole("exit")
	if len(exits) != 1 || exits[0].filled.Sign() <= 0 {
		t.Fatalf("%s: venue holds %d confirmed exit fills", f.venue, len(exits))
	}
	var exit execution.Order
	for _, order := range f.run().Orders {
		if order.Role == "exit" {
			exit = order
		}
	}
	if !execution.Positive(exit.Average) {
		t.Fatalf("%s: the confirmed exit carries no venue average fill price", f.venue)
	}
	return backtest.ManagedCostInput{
		Side:        "long",
		Quantity:    execution.Decimal(dec(entries[0].filled)),
		Entry:       execution.Decimal(dec(entries[0].price)),
		Exit:        exit.Average,
		FeeBPS:      "5", // both fixtures publish a 5 bps taker rate
		SlippageBPS: "0",
	}
}

func TestManagedCostReplay(t *testing.T) {
	for _, venue := range []string{venueBinance, venueOKX} {
		venue := venue
		t.Run(venue, func(t *testing.T) {
			f := newExchangeFixture(t, venue)
			f.Run()
			if f.executeErr != nil {
				t.Fatalf("%s: execute: %v", venue, f.executeErr)
			}
			f.Steps(3)
			if err := f.Flatten(); err != nil {
				t.Fatalf("%s: flatten: %v", venue, err)
			}
			bill := confirmedBill(t, f)

			result, err := backtest.CalculateManagedCosts(bill)
			if err != nil {
				t.Fatalf("%s: calculate costs: %v", venue, err)
			}
			if !result.Complete {
				t.Fatalf("%s: a fully settled USDT bill was reported incomplete", venue)
			}
			change := certVenueEquityChange(t, bill)
			if difference := certDecimalSubtract(t, result.Net, change); !execution.Equal(string(difference), "0") {
				t.Fatalf("%s: replayed net %s differs from the venue change %s by %s", venue, result.Net, change, difference)
			}

			// A funding payment the venue booked but the replay bill left out must
			// remain a reported difference, not be balanced away.
			funding := execution.AccountEvent{
				ID: "venue-funding-1", Kind: risk.KindFunding, Amount: "-0.025", Currency: "USDT",
				OccurredAt: f.now().Add(-time.Hour),
			}
			withFunding := bill
			withFunding.Funding = []execution.AccountEvent{funding}
			settled, err := backtest.CalculateManagedCosts(withFunding)
			if err != nil {
				t.Fatalf("%s: calculate costs with funding: %v", venue, err)
			}
			settledChange := certDecimalAdd(t, change, funding.Amount)
			if difference := certDecimalSubtract(t, settled.Net, settledChange); !execution.Equal(string(difference), "0") {
				t.Fatalf("%s: settled funding net %s differs from venue change %s by %s", venue, settled.Net, settledChange, difference)
			}

			unsettled := certDecimalSubtract(t, result.Net, settledChange)
			if execution.Equal(string(unsettled), "0") {
				t.Fatalf("%s: an unmodelled venue funding charge was silently balanced", venue)
			}
			if !execution.Equal(string(unsettled), "0.025") {
				t.Fatalf("%s: reported difference is %s, want the unmodelled 0.025", venue, unsettled)
			}
			t.Logf("%s: unmodelled venue movement reported as %s (not balanced)", venue, unsettled)
		})
	}
}
