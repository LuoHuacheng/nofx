package execution

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func ownershipRunsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := managedBindingDB(t)
	if err := db.AutoMigrate(&managedRunRef{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestManagedOwnershipRequireLegacyAllowed(t *testing.T) {
	ctx := context.Background()
	db := ownershipRunsDB(t)

	if err := RequireLegacyAllowed(ctx, db, "alice", "unbound"); err != nil {
		t.Fatalf("unbound configuration refused: %v", err)
	}
	// ruling: a binding row without an enabled proof (Binance has no signed UID)
	// does not own the account by itself.
	if err := db.Create(&Binding{ID: "b-binance", UserID: "alice", ExchangeID: "binance-config", Venue: "binance", Environment: EnvironmentLive, VenueAccountID: "unverified"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireLegacyAllowed(ctx, db, "alice", "binance-config"); err != nil {
		t.Fatalf("disabled binding blocked legacy trading: %v", err)
	}
	if err := db.Create(&Binding{ID: "b-managed", UserID: "alice", ExchangeID: "managed", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-1", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	err := RequireLegacyAllowed(ctx, db, "alice", "managed")
	if err == nil || !strings.Contains(err.Error(), "legacy autotrader") {
		t.Fatalf("enabled binding did not block legacy trading: %v", err)
	}

	for _, state := range []string{"approved", "starting", "running", "stopping", "position_open", "needs_attention", "halted_unknown"} {
		if err := db.Create(&managedRunRef{ID: "run-" + state, UserID: "alice", ExchangeID: "plan-" + state, State: state}).Error; err != nil {
			t.Fatal(err)
		}
		refused := RequireLegacyAllowed(ctx, db, "alice", "plan-"+state)
		if refused == nil || !strings.Contains(refused.Error(), state) {
			t.Fatalf("state %s did not block legacy trading: %v", state, refused)
		}
	}
	for _, state := range terminalManagedRunStates {
		if err := db.Create(&managedRunRef{ID: "done-" + state, UserID: "alice", ExchangeID: "done-" + state, State: state}).Error; err != nil {
			t.Fatal(err)
		}
		if err := RequireLegacyAllowed(ctx, db, "alice", "done-"+state); err != nil {
			t.Fatalf("terminal state %s still blocked legacy trading: %v", state, err)
		}
	}
	if err := db.Create(&managedRunRef{ID: "other-user", UserID: "bob", ExchangeID: "bob-plan", State: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireLegacyAllowed(ctx, db, "alice", "bob-plan"); err != nil {
		t.Fatalf("another user's managed run blocked legacy trading: %v", err)
	}
	if err := RequireLegacyAllowed(ctx, db, "alice", ""); err != nil {
		t.Fatalf("configuration without an exchange ID refused: %v", err)
	}
}

type fakeIdentity struct {
	uid   string
	err   error
	reads int
}

func (f *fakeIdentity) VenueAccountID(context.Context) (string, error) {
	f.reads++
	return f.uid, f.err
}

func TestManagedOwnershipIdentity(t *testing.T) {
	ctx := context.Background()
	db := managedBindingDB(t)
	if err := db.Create(&Binding{ID: "b-bob", UserID: "bob", ExchangeID: "bobs-okx", Venue: "okx", Environment: EnvironmentLive, VenueAccountID: "uid-1", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	other := &fakeIdentity{uid: "uid-1"}
	if err := RequireLegacyIdentity(ctx, db, "alices-okx", "okx", other); err == nil {
		t.Fatal("second configuration of the managed physical account was allowed")
	}
	if other.reads != 1 {
		t.Fatalf("identity reads = %d, want 1", other.reads)
	}
	if err := RequireLegacyIdentity(ctx, db, "alices-okx", "okx", &fakeIdentity{uid: " uid-2 "}); err != nil {
		t.Fatalf("unrelated physical account refused: %v", err)
	}
	if err := RequireLegacyIdentity(ctx, db, "bobs-okx", "okx", &fakeIdentity{uid: "uid-1"}); err != nil {
		t.Fatalf("the bound configuration refused itself: %v", err)
	}
	// ruling: identity read failures are fail-closed.
	if err := RequireLegacyIdentity(ctx, db, "alices-okx", "okx", &fakeIdentity{err: context.DeadlineExceeded}); err == nil {
		t.Fatal("identity read failure was treated as unmanaged")
	}
	if err := RequireLegacyIdentity(ctx, db, "alices-okx", "okx", &fakeIdentity{uid: "  "}); err == nil {
		t.Fatal("blank venue identity was treated as unmanaged")
	}
	// Binance never implements AccountIdentity, so no read happens.
	if err := RequireLegacyIdentity(ctx, db, "alices-binance", "binance", nil); err != nil {
		t.Fatalf("venue without an identity reader refused: %v", err)
	}
	unbound := &fakeIdentity{uid: "uid-1"}
	if err := RequireLegacyIdentity(ctx, db, "alices-binance", "binance", unbound); err != nil {
		t.Fatalf("venue without enabled bindings refused: %v", err)
	}
	if unbound.reads != 0 {
		t.Fatalf("read identity without a managed binding: %d calls", unbound.reads)
	}
}

func TestManagedOwnershipReadFailure(t *testing.T) {
	ctx := context.Background()
	db := ownershipRunsDB(t)
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sql.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RequireLegacyAllowed(ctx, db, "alice", "managed"); err == nil {
		t.Fatal("unreadable database was treated as unmanaged")
	}
	if err := RequireLegacyIdentity(ctx, db, "alice", "okx", &fakeIdentity{uid: "uid-1"}); err == nil {
		t.Fatal("unreadable database was treated as unmanaged by the identity check")
	}
}
