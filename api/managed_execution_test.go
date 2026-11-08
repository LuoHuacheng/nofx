package api

import (
	"context"
	"testing"
	"time"

	"nofx/crypto"
	"nofx/store"
	"nofx/trader/execution"
)

func TestManagedSignalSourceSupportsCustomModel(t *testing.T) {
	f := newManagedFixture(t)
	if err := f.db.AutoMigrate(&store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	ai := store.AIModel{ID: "alice-custom", UserID: "alice", Provider: "custom", Enabled: true, APIKey: crypto.EncryptedString("test-model-key"), CustomAPIURL: "http://127.0.0.1:1", CustomModelName: "test-model"}
	if err := f.db.Create(&ai).Error; err != nil {
		t.Fatal(err)
	}
	source, err := f.server.managedSignalSource("alice")
	if err != nil || source == nil {
		t.Fatalf("configured custom model rejected: %v", err)
	}
}

func TestPlanningBrokerUsesBoundSandboxEnvironment(t *testing.T) {
	for _, tc := range []struct{ exchange, venue string }{{"alice-okx-demo", "okx"}, {"alice-binance-demo", "binance"}} {
		t.Run(tc.venue, func(t *testing.T) {
			f := newManagedFixture(t)
			f.okx.account.Environment = execution.EnvironmentSandbox
			f.binance.account.Environment = execution.EnvironmentSandbox
			b, err := execution.BindManagedAccount(context.Background(), f.db, execution.Binding{UserID: "alice", ExchangeID: tc.exchange, Venue: tc.venue, Environment: execution.EnvironmentSandbox, VenueAccountID: "test-uid", VerifiedAt: time.Now().UTC(), Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			broker, key, err := f.server.planningBroker(context.Background(), "alice", tc.exchange)
			if err != nil {
				t.Fatalf("bound sandbox broker rejected: %v", err)
			}
			if key != b.ID || broker.Venue() != tc.venue {
				t.Fatalf("incorrect bound broker: key=%s venue=%s", key, broker.Venue())
			}
			if got := f.environments[tc.exchange]; got != execution.EnvironmentSandbox {
				t.Fatalf("adapter environment=%s", got)
			}
			provider, ok := broker.(interface {
				Managed() (execution.ManagedTrader, bool)
			})
			if !ok {
				t.Fatal("broker lost managed interface")
			}
			snapshotter, ok := provider.Managed()
			if !ok {
				t.Fatal("broker lost managed trader")
			}
			snapshot, err := snapshotter.ManagedSnapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.AccountKey != b.ID || snapshot.Environment != execution.EnvironmentSandbox {
				t.Fatalf("snapshot escaped binding: %+v", snapshot)
			}
		})
	}
}
