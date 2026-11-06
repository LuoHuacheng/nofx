package risk

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"nofx/trader/execution"
)

const (
	breakerUser      = "breaker-user"
	breakerAccount   = "breaker:account"
	breakerPortfolio = "breaker-portfolio"
)

// breakerSetup stores a default-limit policy on a file-backed store. The limits
// stay at their defaults (200 bps daily loss, 500 bps drawdown): PutPolicy only
// allows tightening, so the tests drive the thresholds with equity instead.
func breakerSetup(t *testing.T) (*Store, *gorm.DB, string) {
	t.Helper()
	store, db, path := ledgerStore(t)
	p := DefaultPolicy(breakerUser, breakerAccount, breakerPortfolio, "binance", execution.EnvironmentSandbox)
	p.Symbols = []string{"BTCUSDT"}
	v, err := PolicyVersion(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Version = v
	if _, err = store.PutPolicy(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return store, db, path
}

func breakerObserve(t *testing.T, store *Store, equity string, at time.Time) {
	t.Helper()
	observe(t, store, ledgerAccount(breakerAccount, execution.EnvironmentSandbox, equity), nil, at)
}

func breakerDecision(t *testing.T, store *Store, now time.Time) Decision {
	t.Helper()
	decision, err := store.Evaluate(context.Background(), breakerUser, breakerAccount, now)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func breakerLatch(t *testing.T, db *gorm.DB) Breaker {
	t.Helper()
	var latch Breaker
	if err := db.Where("scope = ? AND scope_id = ?", ScopeAccount, breakerAccount).First(&latch).Error; err != nil {
		t.Fatal(err)
	}
	return latch
}

// TestRiskBreakerThresholds pins the exact decimal boundaries: 100 to 98 trips
// a 200 bps daily loss, 98.001 does not, 97.999 does, and 100 to 95 trips a
// 500 bps drawdown while 95.001 does not.
func TestRiskBreakerThresholds(t *testing.T) {
	day := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		equity string
		want   string
	}{
		{"daily loss at the limit", "98", ActionFlatten},
		{"daily loss above the limit", "98.001", ActionAllow},
		{"daily loss below the limit", "97.999", ActionFlatten},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _ := breakerSetup(t)
			breakerObserve(t, store, "100", day)
			breakerObserve(t, store, tc.equity, day.Add(time.Minute))
			got := breakerDecision(t, store, day.Add(time.Minute+2*time.Second))
			if got.Action != tc.want {
				t.Fatalf("equity %s against day start 100 at 200 bps returned %q (%s)", tc.equity, got.Action, got.Reason)
			}
		})
	}

	next := time.Date(2026, 11, 6, 0, 0, 5, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		equity string
		want   string
	}{
		{"drawdown at the limit", "95", ActionFlatten},
		{"drawdown above the limit", "95.001", ActionAllow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _ := breakerSetup(t)
			breakerObserve(t, store, "100", day)
			breakerObserve(t, store, tc.equity, next)
			got := breakerDecision(t, store, next)
			if got.Action != tc.want {
				t.Fatalf("equity %s against peak 100 at 500 bps returned %q (%s)", tc.equity, got.Action, got.Reason)
			}
		})
	}
}

// TestRiskBreakerRestart proves the latch is durable: a second store opened on
// the same file returns the same verdict without any in-memory state.
func TestRiskBreakerRestart(t *testing.T) {
	store, _, path := breakerSetup(t)
	day := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	breakerObserve(t, store, "100", day)
	breakerObserve(t, store, "98", day.Add(time.Minute))
	now := day.Add(time.Minute + 2*time.Second)
	first := breakerDecision(t, store, now)
	if first.Action != ActionFlatten {
		t.Fatalf("the fixture did not latch: %q", first.Action)
	}

	reopened, err := NewStore(ledgerDB(t, path))
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Evaluate(context.Background(), breakerUser, breakerAccount, now)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != first.Action || again.Reason != first.Reason {
		t.Fatalf("reopened store returned %q (%s), want %q (%s)", again.Action, again.Reason, first.Action, first.Reason)
	}
}

// TestRiskBreakerResetGuard covers the four refusals of the Addendum mapping,
// then proves a correct reset is auditable without erasing the incident, and
// that a new UTC day never clears a latch.
func TestRiskBreakerResetGuard(t *testing.T) {
	ctx := context.Background()
	store, db, _ := breakerSetup(t)
	day := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	breakerObserve(t, store, "100", day)
	breakerObserve(t, store, "98", day.Add(time.Minute))
	now := day.Add(time.Minute + 2*time.Second)
	if decision := breakerDecision(t, store, now); decision.Action != ActionFlatten {
		t.Fatalf("the fixture did not latch: %q", decision.Action)
	}
	latch := breakerLatch(t, db)
	peak := ledgerState(t, store, breakerAccount, "2026-11-05").Peak

	if err := store.Reset(ctx, "someone-else", breakerAccount, latch.Version, now); err == nil {
		t.Fatal("reset accepted a foreign owner")
	}
	if err := store.Reset(ctx, breakerUser, breakerAccount, "deadbeef", now); err == nil {
		t.Fatal("reset accepted a stale version")
	}
	held := ReservationRecord{
		RunID: "run-x", UserID: breakerUser, PolicyID: "policy-x", AccountKey: breakerAccount,
		PortfolioID: breakerPortfolio, Symbol: "BTCUSDT", Side: "long", PolicyVersion: "v",
		Risk: "0.3", Notional: "30", Margin: "10", State: StateReserved, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&held).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Reset(ctx, breakerUser, breakerAccount, latch.Version, now); err == nil {
		t.Fatal("reset accepted a held reservation")
	}
	if err := db.Where("run_id = ?", "run-x").Delete(&ReservationRecord{}).Error; err != nil {
		t.Fatal(err)
	}
	state := db.Model(&EquityState{}).Where("account_key = ? AND day = ?", breakerAccount, "2026-11-05")
	if err := state.Update("complete", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Reset(ctx, breakerUser, breakerAccount, latch.Version, now); err == nil {
		t.Fatal("reset accepted an incomplete baseline")
	}
	if err := state.Update("complete", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("account_key = ? AND day = ?", breakerAccount, "2026-11-05").Delete(&EquityState{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Reset(ctx, breakerUser, breakerAccount, latch.Version, now); err == nil {
		t.Fatal("reset accepted a missing baseline")
	}
	// Re-observing rebuilds the day's baseline from the retained observations.
	breakerObserve(t, store, "98", day.Add(2*time.Minute))
	now = day.Add(2 * time.Minute)

	if err := store.Reset(ctx, breakerUser, breakerAccount, latch.Version, now); err != nil {
		t.Fatalf("a valid reset was refused: %v", err)
	}
	var audit []ResetRecord
	if err := db.Where("scope = ? AND scope_id = ?", ScopeAccount, breakerAccount).Find(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 || audit[0].BreakerVersion != latch.Version || audit[0].BaselineEquity != "98" || audit[0].BaselineDay != "2026-11-05" {
		t.Fatalf("reset audit row is %+v", audit)
	}
	after := breakerLatch(t, db)
	if after.ResetAt.IsZero() || after.ResetBy != breakerUser || after.ResetVersion != latch.Version {
		t.Fatalf("latch was not marked as reset: %+v", after)
	}
	if got := ledgerState(t, store, breakerAccount, "2026-11-05").Peak; got != peak {
		t.Fatalf("reset changed peak %s to %s", peak, got)
	}
	if decision := breakerDecision(t, store, now); decision.Action != ActionAllow {
		t.Fatalf("reset latch still blocks entries: %q (%s)", decision.Action, decision.Reason)
	}

	// A second incident rewrites the latch and must be latchable again.
	breakerObserve(t, store, "90", day.Add(3*time.Minute))
	now = day.Add(3*time.Minute + 2*time.Second)
	if decision := breakerDecision(t, store, now); decision.Action != ActionFlatten {
		t.Fatalf("a reset latch did not re-latch on a new breach: %q", decision.Action)
	}
	// A new UTC day never clears the latch: only Reset does.
	next := time.Date(2026, 11, 6, 0, 0, 5, 0, time.UTC)
	breakerObserve(t, store, "100", next)
	if decision := breakerDecision(t, store, next); decision.Action != ActionFlatten {
		t.Fatalf("a new UTC day cleared the latch: %q", decision.Action)
	}
}

// TestRiskBreakerPauseAndOwner covers the owner check of Pause and that a pause
// latch keeps blocking entries.
func TestRiskBreakerPauseAndOwner(t *testing.T) {
	ctx := context.Background()
	store, _, _ := breakerSetup(t)
	day := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	breakerObserve(t, store, "100", day)
	if err := store.Pause(ctx, "someone-else", breakerAccount, "foreign pause"); err == nil {
		t.Fatal("pause accepted a foreign owner")
	}
	if err := store.Pause(ctx, breakerUser, breakerAccount, "operator pause"); err != nil {
		t.Fatal(err)
	}
	if decision := breakerDecision(t, store, day.Add(2*time.Second)); decision.Action != ActionPauseEntries || decision.Reason != "operator pause" {
		t.Fatalf("pause latch returned %q (%s)", decision.Action, decision.Reason)
	}
	// Evaluate reads the latch before the thresholds, so a pause latch is never
	// upgraded to flatten by a later breach; it keeps blocking entries.
	breakerObserve(t, store, "90", day.Add(time.Minute))
	if decision := breakerDecision(t, store, day.Add(time.Minute)); decision.Action != ActionPauseEntries {
		t.Fatalf("a paused account reported %q", decision.Action)
	}
}

// TestRiskBreakerPauseAfterReset pins that a reset row is inert: the operator's
// pause must still latch, or an acknowledged incident would silently leave the
// account free to submit entries.
func TestRiskBreakerPauseAfterReset(t *testing.T) {
	ctx := context.Background()
	store, db, _ := breakerSetup(t)
	day := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	breakerObserve(t, store, "100", day)
	breakerObserve(t, store, "98", day.Add(time.Minute))
	now := day.Add(time.Minute + 2*time.Second)
	if decision := breakerDecision(t, store, now); decision.Action != ActionFlatten {
		t.Fatalf("the fixture did not latch: %q", decision.Action)
	}
	if err := store.Reset(ctx, breakerUser, breakerAccount, breakerLatch(t, db).Version, now); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if err := store.Pause(ctx, breakerUser, breakerAccount, "operator pause"); err != nil {
		t.Fatalf("pause after a reset: %v", err)
	}
	if decision := breakerDecision(t, store, now); decision.Action != ActionPauseEntries || decision.Reason != "operator pause" {
		t.Fatalf("pause after a reset returned %q (%s)", decision.Action, decision.Reason)
	}
	if err := store.Pause(ctx, "someone-else", breakerAccount, "foreign pause"); err == nil {
		t.Fatal("pause accepted a foreign owner")
	}
}
