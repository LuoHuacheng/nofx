package risk

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nofx/trader/execution"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	reservationUserA     = "acct-a"
	reservationUserB     = "acct-b"
	reservationPortfolio = "pf-1"
)

func reservationPolicy(accountKey string) Policy {
	p := DefaultPolicy(reservationUserA, accountKey, reservationPortfolio, "binance", execution.EnvironmentLive)
	p.Symbols = []string{"BTCUSDT"}
	p.Enabled = true
	p.PerTradeRiskBPS = 50
	p.AccountOpenRiskBPS = 100
	p.PortfolioOpenRiskBPS = 25
	p.MaxMarginBPS = 3000
	p.MaxGrossBPS = 15000
	p.MaxDirectionalBPS = 10000
	return p
}

// reservationStore opens a file-backed SQLite store with two observed accounts
// of 100 equity each in one portfolio, and a stored policy for both.
func reservationStore(t *testing.T) (*Store, *gorm.DB, string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reservations.db")
	db := reservationDB(t, path)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	observe(t, store, ledgerAccount(reservationUserA, execution.EnvironmentSandbox, "100"), nil, now)
	observe(t, store, ledgerAccount(reservationUserB, execution.EnvironmentSandbox, "100"), nil, now)
	for _, key := range []string{reservationUserA, reservationUserB} {
		p := versioned(t, reservationPolicy(key))
		if _, err := store.PutPolicy(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	return store, db, path, now
}

func reservationDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sql.Close() })
	return db
}

func reservationPolicyVersion(t *testing.T, store *Store, accountKey string) string {
	t.Helper()
	p, err := store.Policy(context.Background(), reservationUserA, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	return p.Version
}

// reservationFor builds the standard hold of the brief: risk 0.3, notional 30,
// margin 10 on the long side.
func reservationFor(t *testing.T, store *Store, runID, accountKey string) Reservation {
	t.Helper()
	return Reservation{
		UserID:        reservationUserA,
		PolicyID:      policyID(reservationUserA, accountKey),
		RunID:         runID,
		AccountKey:    accountKey,
		PortfolioID:   reservationPortfolio,
		Symbol:        "BTCUSDT",
		Side:          "long",
		PolicyVersion: reservationPolicyVersion(t, store, accountKey),
		Risk:          "0.3",
		Notional:      "30",
		Margin:        "10",
		State:         StateReserved,
	}
}

// insertHeld writes a hold straight into the table, bypassing the limits. It
// stands in for risk an earlier run already carries.
func insertHeld(t *testing.T, store *Store, r Reservation, runID, risk string, now time.Time) {
	t.Helper()
	held := ReservationRecord{
		RunID:         runID,
		UserID:        r.UserID,
		PolicyID:      r.PolicyID,
		AccountKey:    r.AccountKey,
		PortfolioID:   r.PortfolioID,
		Symbol:        r.Symbol,
		Side:          r.Side,
		PolicyVersion: r.PolicyVersion,
		Risk:          execution.Decimal(risk),
		Notional:      r.Notional,
		Margin:        r.Margin,
		State:         StateReserved,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.db.Create(&held).Error; err != nil {
		t.Fatal(err)
	}
}

func reserveInTx(t *testing.T, store *Store, r Reservation, now time.Time) error {
	t.Helper()
	return store.db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
		return store.ReserveTx(context.Background(), tx, r, now)
	})
}

func TestRiskReservationConcurrent(t *testing.T) {
	store, _, _, now := reservationStore(t)
	// Built before the goroutines: t.Fatal is only legal on the test goroutine.
	holds := []Reservation{
		reservationFor(t, store, "run-"+reservationUserA, reservationUserA),
		reservationFor(t, store, "run-"+reservationUserB, reservationUserB),
	}

	var allowed int64
	var wg sync.WaitGroup
	for _, r := range holds {
		wg.Add(1)
		go func(r Reservation) {
			defer wg.Done()
			if err := reserveInTx(t, store, r, now); err == nil {
				atomic.AddInt64(&allowed, 1)
			}
		}(r)
	}
	wg.Wait()

	// Each hold is 0.3 of a 0.5 portfolio open-risk budget, so only one fits.
	if allowed != 1 {
		t.Fatalf("concurrent reservations accepted=%d, want 1", allowed)
	}
}

func TestRiskReservationDuplicate(t *testing.T) {
	store, db, _, now := reservationStore(t)
	r := reservationFor(t, store, "run-dup", reservationUserA)
	if err := reserveInTx(t, store, r, now); err != nil {
		t.Fatal(err)
	}
	if err := reserveInTx(t, store, r, now); err != nil {
		t.Fatalf("identical retry rejected: %v", err)
	}
	var count int64
	if err := db.Model(&ReservationRecord{}).Where("run_id = ?", r.RunID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate retry left %d rows, want 1", count)
	}

	changed := r
	changed.Risk = "0.31"
	if err := reserveInTx(t, store, changed, now); err == nil {
		t.Fatal("a retry with a different risk was accepted")
	}
}

func TestRiskReservationUnknownPersists(t *testing.T) {
	store, db, path, now := reservationStore(t)
	r := reservationFor(t, store, "run-unknown", reservationUserA)
	if err := reserveInTx(t, store, r, now); err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sql.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := reservationDB(t, path)
	after, err := NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	record, err := after.Reservation(context.Background(), r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State == StateReleased {
		t.Fatal("UNKNOWN lost its reservation")
	}
}

func TestRiskReservationLimitReasons(t *testing.T) {
	cases := []struct {
		name    string
		want    string
		prepare func(t *testing.T, store *Store, now time.Time, r *Reservation)
	}{
		{
			name: "per trade risk",
			want: "per-trade risk",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				r.Risk = "0.6"
			},
		},
		{
			name: "account open risk",
			want: "account open risk",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				// Inserted directly: 0.8 already exceeds the portfolio budget of
				// 0.5, so only a pre-existing hold can reach the account limit.
				insertHeld(t, store, *r, "run-held", "0.8", now)
			},
		},
		{
			name: "account margin",
			want: "account margin",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				r.Margin = "31"
			},
		},
		{
			name: "account gross",
			want: "account gross notional",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				r.Notional = "151"
			},
		},
		{
			name: "account directional",
			want: "account directional notional",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				held := reservationFor(t, store, "run-held", reservationUserA)
				held.Notional = "99"
				if err := reserveInTx(t, store, held, now); err != nil {
					t.Fatal(err)
				}
				r.Notional = "2"
			},
		},
		{
			name: "stale snapshot",
			want: "stale",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				old := now.Add(-10 * time.Second)
				if err := store.db.Model(&EquityState{}).
					Where("account_key = ?", r.AccountKey).
					Update("observed_at", old).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "incomplete ledger",
			want: "incomplete",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				if err := store.db.Model(&EquityState{}).
					Where("account_key = ?", r.AccountKey).
					Update("complete", false).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "policy version",
			want: "policy version changed",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				r.PolicyVersion = "stale-version"
			},
		},
		{
			name: "missing policy",
			want: "no risk policy",
			prepare: func(t *testing.T, store *Store, now time.Time, r *Reservation) {
				r.AccountKey = "acct-missing"
				r.PolicyID = policyID(r.UserID, r.AccountKey)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _, now := reservationStore(t)
			r := reservationFor(t, store, "run-limit", reservationUserA)
			tc.prepare(t, store, now, &r)
			err := reserveInTx(t, store, r, now)
			if err == nil {
				t.Fatalf("%s limit did not reject the reservation", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err.Error(), tc.want)
			}
		})
	}
}

func TestRiskExposeReleaseIdempotent(t *testing.T) {
	store, _, _, now := reservationStore(t)
	r := reservationFor(t, store, "run-expose", reservationUserA)
	if err := reserveInTx(t, store, r, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.db.Transaction(func(tx *gorm.DB) error {
			return store.ExposeTx(context.Background(), tx, r.RunID)
		}); err != nil {
			t.Fatalf("expose %d: %v", i+1, err)
		}
	}
	record, err := store.Reservation(context.Background(), r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateExposed {
		t.Fatalf("state after double expose = %q, want %q", record.State, StateExposed)
	}

	for i := 0; i < 2; i++ {
		if err := store.db.Transaction(func(tx *gorm.DB) error {
			return store.ReleaseTx(context.Background(), tx, r.RunID)
		}); err != nil {
			t.Fatalf("release %d: %v", i+1, err)
		}
	}
	record, err = store.Reservation(context.Background(), r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateReleased {
		t.Fatalf("state after double release = %q, want %q", record.State, StateReleased)
	}
}
