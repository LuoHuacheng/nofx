package execution

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func managedBindingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "binding.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	if err = db.AutoMigrate(&Binding{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestManagedBindingIdentity(t *testing.T) {
	ctx := context.Background()
	db := managedBindingDB(t)
	verified := time.Now().UTC()
	first, err := BindManagedAccount(ctx, db, Binding{UserID: "alice", ExchangeID: "alice-primary", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-1", VerifiedAt: verified, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Enabled || first.VerifiedAt.IsZero() || first.ID == "" {
		t.Fatalf("verified binding not usable: %+v", first)
	}

	// Rotating the credentials of the same configuration keeps the binding.
	rotated, err := BindManagedAccount(ctx, db, Binding{UserID: "alice", ExchangeID: "alice-primary", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-1", VerifiedAt: verified.Add(time.Minute), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != rotated.ID {
		t.Fatal("rotation changed physical account binding")
	}
	if !rotated.VerifiedAt.After(first.VerifiedAt) {
		t.Fatal("rotation did not refresh the verification time")
	}

	// A second credential configuration for the same physical account is rejected.
	duplicateErr := func() error {
		_, err := BindManagedAccount(ctx, db, Binding{UserID: "alice", ExchangeID: "alice-secondary", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-1", VerifiedAt: verified, Enabled: true})
		return err
	}()
	if duplicateErr == nil {
		t.Fatal("second credential binding accepted")
	}
	if strings.Contains(duplicateErr.Error(), "alice") {
		t.Fatalf("duplicate error leaked the owner: %v", duplicateErr)
	}

	// Another NOFX user cannot take a verified physical account.
	_, err = BindManagedAccount(ctx, db, Binding{UserID: "bob", ExchangeID: "bob-primary", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-1", VerifiedAt: verified, Enabled: true})
	if err == nil {
		t.Fatal("cross-owner binding accepted")
	}
	if strings.Contains(err.Error(), "alice") {
		t.Fatalf("cross-owner error leaked the owner: %v", err)
	}

	// The uniqueness guarantee is database-level, not only application-level.
	if err := db.Create(&Binding{ID: "raw", UserID: "bob", ExchangeID: "bob-primary", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-1", VerifiedAt: verified, Enabled: true}).Error; err == nil {
		t.Fatal("database accepted a duplicate physical account")
	}

	// One credential configuration cannot be moved to another physical account.
	if _, err := BindManagedAccount(ctx, db, Binding{UserID: "alice", ExchangeID: "alice-primary", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-2", VerifiedAt: verified, Enabled: true}); err == nil {
		t.Fatal("credential configuration rebound to another physical account")
	}

	// The same physical account in another environment is a distinct binding.
	demo, err := BindManagedAccount(ctx, db, Binding{UserID: "alice", ExchangeID: "alice-primary-demo", Venue: "okx", Environment: EnvironmentSandbox, VenueAccountID: "uid-1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if demo.ID == first.ID || demo.Enabled || !demo.VerifiedAt.IsZero() {
		t.Fatalf("unverified binding was not kept disabled and distinct: %+v", demo)
	}

	// Unverified identities are always disabled.
	unverified, err := BindManagedAccount(ctx, db, Binding{UserID: "alice", ExchangeID: "alice-binance", Venue: "binance", Environment: EnvironmentSandbox, VenueAccountID: "uid-binance", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if unverified.Enabled || !unverified.VerifiedAt.IsZero() {
		t.Fatalf("unverified identity was enabled: %+v", unverified)
	}

	// Invalid input is rejected.
	for name, in := range map[string]Binding{
		"empty account":   {UserID: "alice", ExchangeID: "x", Venue: "okx", Environment: EnvironmentLive},
		"empty exchange":  {UserID: "alice", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-9"},
		"bad environment": {UserID: "alice", ExchangeID: "x", Venue: "okx", Environment: "demo", VenueAccountID: "uid-9"},
	} {
		if _, err := BindManagedAccount(ctx, db, in); err == nil {
			t.Fatalf("%s: invalid binding accepted", name)
		}
	}

	got, err := ResolveBinding(ctx, db, "alice", "alice-primary")
	if err != nil || got.ID != first.ID {
		t.Fatalf("resolve = %+v, %v", got, err)
	}
	if _, err := ResolveBinding(ctx, db, "bob", "alice-primary"); err == nil {
		t.Fatal("resolved another user's binding")
	}
	if _, err := ResolveBinding(ctx, db, "alice", "missing"); err == nil {
		t.Fatal("resolved an unbound configuration")
	}
}

type fakeBoundTrader struct {
	route    string
	leverage int
	prepares int
}

func (f *fakeBoundTrader) Venue() string { return "okx" }
func (f *fakeBoundTrader) AccountSnapshot(context.Context, string) (Account, error) {
	return Account{}, nil
}
func (f *fakeBoundTrader) PrepareExecution(context.Context, string) error {
	f.prepares++
	return nil
}
func (f *fakeBoundTrader) SubmitOrder(_ context.Context, o Order) (Order, error) { return o, nil }
func (f *fakeBoundTrader) LookupOrder(_ context.Context, o Order) (Order, error) { return o, nil }
func (f *fakeBoundTrader) CancelExecutionOrder(context.Context, Order) error     { return nil }
func (f *fakeBoundTrader) RouteClientID(id string) (string, error) {
	f.route = "routed-" + id
	return f.route, nil
}
func (f *fakeBoundTrader) PrepareExecutionLeverage(_ context.Context, _ string, leverage int) error {
	f.leverage = leverage
	return nil
}
func (f *fakeBoundTrader) ManagedSnapshot(context.Context) (ManagedAccount, error) {
	return ManagedAccount{AccountKey: "leaked-venue-key"}, nil
}
func (f *fakeBoundTrader) VerifyManagedSettings(context.Context, string, int) error { return nil }
func (f *fakeBoundTrader) ManagedCapabilities() Capabilities                        { return Capabilities{} }
func (f *fakeBoundTrader) AmendStop(_ context.Context, o Order, _ Decimal) (Order, error) {
	return o, ErrUnsupported
}
func (f *fakeBoundTrader) Events(_ context.Context, fn func(AccountEvent) error) error {
	for _, e := range []AccountEvent{{ID: "1", AccountKey: "leaked-venue-key"}} {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func TestManagedBindingWithBinding(t *testing.T) {
	inner := &fakeBoundTrader{}
	bound := WithBinding(inner, Binding{ID: "binding-1"})

	snap, err := bound.ManagedSnapshot(context.Background())
	if err != nil || snap.AccountKey != "binding-1" {
		t.Fatalf("snapshot key = %q, %v", snap.AccountKey, err)
	}
	var seen []string
	if err := bound.Events(context.Background(), func(e AccountEvent) error {
		seen = append(seen, e.AccountKey)
		return nil
	}); err != nil || len(seen) != 1 || seen[0] != "binding-1" {
		t.Fatalf("event keys = %v, %v", seen, err)
	}
	if id, err := bound.RouteClientID("abc"); err != nil || id != "routed-abc" || inner.route != "routed-abc" {
		t.Fatalf("route = %q, %v", id, err)
	}
	if err := bound.PrepareExecutionLeverage(context.Background(), "BTC-USDT-SWAP", 3); err != nil || inner.leverage != 3 || inner.prepares != 0 {
		t.Fatalf("leverage = %d, prepares = %d, %v", inner.leverage, inner.prepares, err)
	}
	if bound.Venue() != "okx" {
		t.Fatal("wrapper lost the venue")
	}
}
