package certification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestManagedContractFaultMatrix runs the same ten fault contracts against both
// venues. Every case drives the real adapter, the real controller and the real
// risk store; only the venue itself is simulated.
func TestManagedContractFaultMatrix(t *testing.T) {
	for _, venue := range []string{venueBinance, venueOKX} {
		venue := venue
		for _, scenario := range contractScenarios {
			scenario := scenario
			t.Run(venue+"/"+scenario.name, func(t *testing.T) {
				f := newExchangeFixture(t, venue).Scenario(scenario.name)
				scenario.check(t, f)
			})
		}
	}
}

type contractScenario struct {
	name  string
	check func(*testing.T, *exchangeFixture)
}

var contractScenarios = []contractScenario{
	{
		name: "accepted_response_lost",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			if f.executeErr != nil {
				t.Fatalf("execute: %v", f.executeErr)
			}
			f.Steps(3)
			f.AssertOneEntry(t, "signal-1")
			if got := f.fake.byRole("entry"); len(got) != 1 {
				t.Fatalf("venue holds %d entry orders, want 1", len(got))
			}
			if position := f.fake.positionNow(); position.Sign() <= 0 {
				t.Fatalf("position is %s after a lost acknowledgement; the fill was not discovered", dec(position))
			}
			// The discovered fill must also be protected, without a resubmission.
			stops := f.fake.byRole("sl")
			if len(stops) != 1 {
				t.Fatalf("venue holds %d protective stops after the reconcile, want 1", len(stops))
			}
			if !stops[0].live() || stops[0].quantity.Cmp(f.fake.positionNow()) != 0 {
				t.Fatalf("stop protects %s in state %s, want the full %s", dec(stops[0].quantity), stops[0].status, dec(f.fake.positionNow()))
			}
			if err := f.Flatten(); err != nil {
				t.Fatalf("flatten after reconcile: %v", err)
			}
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "partial_fill_restart",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			if f.executeErr != nil {
				t.Fatalf("execute: %v", f.executeErr)
			}
			f.Steps(3)
			f.AssertOneEntry(t, "signal-1")
			filled := f.fake.positionNow()
			if filled.Sign() <= 0 {
				t.Fatalf("position is %s after a partial fill", dec(filled))
			}
			for _, order := range f.fake.byRole("sl") {
				if order.quantity.Cmp(filled) != 0 {
					t.Fatalf("stop protects %s but the venue filled %s", dec(order.quantity), dec(filled))
				}
			}
			// A restart must reconcile the existing run instead of replaying it.
			before, _, _, _ := f.fake.countsNow()
			f.Restart()
			if got := f.run().State; got != "needs_attention" {
				t.Fatalf("restarted run state is %q, want needs_attention", got)
			}
			f.Steps(2)
			if after, _, _, _ := f.fake.countsNow(); after != before {
				t.Fatalf("restart submitted %d further entry orders", after-before)
			}
			if state := f.ReservationState(); state != "reserved" && state != "exposed" {
				t.Fatalf("risk reservation is %q after the restart, want it held", state)
			}
			if err := f.Flatten(); err != nil {
				t.Fatalf("flatten: %v", err)
			}
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "sl_accepted_response_lost",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			f.Steps(4)
			f.AssertOneEntry(t, "signal-1")
			if got := f.fake.byRole("sl"); len(got) != 1 {
				t.Fatalf("venue holds %d protective stops, want 1 (no resubmission)", len(got))
			}
			if got := f.fake.byRole("tp"); len(got) != 1 {
				t.Fatalf("venue holds %d targets, want 1", len(got))
			}
			if err := f.Flatten(); err != nil {
				t.Fatalf("flatten: %v", err)
			}
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "sl_rejected",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			f.Steps(2)
			f.Advance(6 * 60e9)
			f.Steps(1)
			if got := f.run().ProtectionState; got != "exit_required" {
				t.Fatalf("protection state is %q, want exit_required after the deadline", got)
			}
			// The plan asks for a reduce-only exit once the deadline passes. The
			// controller freezes instead (protection.go: reducing belongs to the
			// operator), so the exit is driven here and must report why it stops
			// rather than fail silently.
			err := f.Flatten()
			if err == nil || !strings.Contains(err.Error(), "Check exchange orders") {
				t.Fatalf("flatten error is %v, want the unconfirmed-protection halt", err)
			}
			f.AssertNoReversePosition(t)
			f.AssertOneEntry(t, "signal-1")
			// The rejected stop was never acknowledged, so its state cannot be
			// confirmed and the trade cannot be proven closed: the run halts on
			// the venue record and keeps the lease, which blocks the next entry.
			f.AssertHalted(t, "halted_unknown")
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "tp_rejected",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			if f.executeErr != nil {
				t.Fatalf("execute: %v", f.executeErr)
			}
			f.Steps(2)
			stops := f.fake.byRole("sl")
			if len(stops) != 1 {
				t.Fatalf("venue holds %d protective stops, want 1", len(stops))
			}
			f.Advance(6 * 60e9)
			f.Steps(1)
			// Only the stop protects capital, so a rejected target never starts
			// the exit deadline; it must stop new entries instead.
			if got := f.run().ProtectionState; got == "exit_required" {
				t.Fatal("a rejected target started the protection deadline")
			}
			if len(f.fake.byRole("sl")) != 1 || !f.fake.byRole("sl")[0].live() {
				t.Fatal("the protective stop was withdrawn when the target was rejected")
			}
			if _, _, exits, _ := f.fake.countsNow(); exits != 0 {
				t.Fatalf("%s sent %d exit orders; a missing target is not an exit reason", f.venue, exits)
			}
			_ = f.Flatten()
			f.AssertNoReversePosition(t)
			f.AssertOneEntry(t, "signal-1")
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "cancel_races_fill",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			f.Steps(1)
			if position := f.fake.positionNow(); position.Sign() != 0 {
				t.Fatalf("position is %s before the cancel; the entry was not resting", dec(position))
			}
			if err := f.Stop(); err != nil {
				t.Fatalf("stop: %v", err)
			}
			f.Steps(3)
			f.AssertOneEntry(t, "signal-1")
			if position := f.fake.positionNow(); position.Sign() <= 0 {
				t.Fatalf("position is %s; the fill that raced the cancel was lost", dec(position))
			}
			_ = f.Flatten()
			f.AssertNoReversePosition(t)
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "manual_close_races_sl",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			f.Steps(2)
			// Someone closes the position outside this run while the stop is live.
			f.ClosePosition()
			f.Steps(3)
			f.AssertNoReversePosition(t)
			f.AssertOneEntry(t, "signal-1")
			if position := f.fake.positionNow(); position.Sign() != 0 {
				t.Fatalf("position is %s after an external close", dec(position))
			}
			// The operator closes out of band; the exits must then be provably gone.
			if err := f.FinishManualClose(); err != nil {
				t.Fatalf("finish manual close: %v", err)
			}
			f.AssertNoOpenOrders(t)
			f.AssertNoReversePosition(t)
		},
	},
	{
		name: "stale_snapshot_and_ws_gap",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			if f.executeErr == nil {
				t.Fatal("execute accepted a stale book")
			}
			if entry, _, _, _ := f.fake.countsNow(); entry != 0 {
				t.Fatalf("%d entry orders were sent against a stale book", entry)
			}
			// A fresh read is the only thing that may release the entry again:
			// this stands in for the REST snapshot a stream gap must await.
			f.book.setStale(false)
			f.Replan()
			f.Steps(1)
			if entry, _, _, _ := f.fake.countsNow(); entry != 1 {
				t.Fatalf("%d entry orders after a fresh snapshot, want 1", entry)
			}
			if err := f.Flatten(); err != nil {
				t.Fatalf("flatten: %v", err)
			}
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "rate_limit_and_clock_skew",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			if f.executeErr == nil {
				t.Fatal("execute ignored a rejected account read")
			}
			if entry, _, _, _ := f.fake.countsNow(); entry != 0 {
				t.Fatalf("%d entry orders were sent while reads were failing", entry)
			}
			// Binance reports skew (-1021), OKX reports the limit; both must
			// surface the venue reason and nothing may be written meanwhile.
			message := f.executeErr.Error()
			if !strings.Contains(message, "clock") && !strings.Contains(message, "50011") {
				t.Fatalf("execute error %q does not name the venue read failure", message)
			}
			// Once the reads recover the same plan may run again: no write was
			// attempted blindly and nothing is stuck.
			f.ClearReads()
			f.Replan()
			f.Steps(1)
			if entry, _, _, _ := f.fake.countsNow(); entry != 1 {
				t.Fatalf("%d entry orders after the reads recovered, want 1", entry)
			}
			if err := f.Flatten(); err != nil {
				t.Fatalf("flatten: %v", err)
			}
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
	{
		name: "db_commit_failure",
		check: func(t *testing.T, f *exchangeFixture) {
			f.Run()
			if f.executeErr == nil {
				t.Fatal("execute reported success although the intent was not durable")
			}
			if entry, _, _, _ := f.fake.countsNow(); entry != 0 {
				t.Fatalf("%d entry orders were sent without a durable intent", entry)
			}
			f.AssertFlatAndNoRiskOrders(t)
		},
	},
}

// TestReceiptsMatchVenueShapes pins the simulator to the recorded venue
// receipts so a wire change cannot silently drift away from the adapters.
func TestReceiptsMatchVenueShapes(t *testing.T) {
	for _, venue := range []string{venueBinance, venueOKX} {
		venue := venue
		t.Run(venue, func(t *testing.T) {
			f := newExchangeFixture(t, venue).Scenario("accepted_response_lost")
			f.Run()
			f.Steps(3)

			recorded := map[string]map[string]any{}
			raw, err := os.ReadFile(filepath.Join("testdata", venue+"_receipts.json"))
			if err != nil {
				t.Fatalf("read receipts: %v", err)
			}
			if err := json.Unmarshal(raw, &recorded); err != nil {
				t.Fatalf("decode receipts: %v", err)
			}
			for _, sample := range []struct {
				name string
				body string
			}{
				{"entry", f.fake.entryReceipt()},
				{"protection", f.fake.protectionReceipt()},
			} {
				want, ok := recorded[sample.name]
				if !ok {
					t.Fatalf("receipts file has no %q sample", sample.name)
				}
				if sample.body == "" {
					t.Fatalf("%s never answered a %s request", venue, sample.name)
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(sample.body), &got); err != nil {
					t.Fatalf("%s %s receipt is not a JSON object: %v", venue, sample.name, err)
				}
				if keys := sortedKeys(got); !equalStrings(keys, sortedKeys(want)) {
					t.Fatalf("%s %s receipt keys drifted\n got: %v\nwant: %v", venue, sample.name, keys, sortedKeys(want))
				}
			}
		})
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
