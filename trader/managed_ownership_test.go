package trader

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/planning/live"
	"nofx/store"
	"nofx/trader/execution"
)

func managedOwnershipStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ownership.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	if err = db.AutoMigrate(&store.Exchange{}, &execution.Binding{}, &live.Run{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestManagedOwnershipBlocksLegacy covers the two runtime entries the legacy
// bypass could use: construction and Run (which the grid path shares).
func TestManagedOwnershipBlocksLegacy(t *testing.T) {
	st := managedOwnershipStore(t)
	ex := store.Exchange{ID: "managed", UserID: "alice", ExchangeType: "okx", Enabled: true}
	if err := st.GormDB().Create(&ex).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.GormDB().Create(&execution.Binding{ID: "binding-1", UserID: "alice", ExchangeID: "managed", Venue: "okx", Environment: execution.EnvironmentLive, VenueAccountID: "uid-1", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	// The refusal must come from the managed-ownership guard before the venue
	// switch: "unsupported-venue" would otherwise fail with a platform error.
	_, err := NewAutoTrader(AutoTraderConfig{ExchangeID: "managed", Exchange: "unsupported-venue", InitialBalance: 100}, st, "alice")
	if err == nil || !strings.Contains(err.Error(), "legacy autotrader") {
		t.Fatalf("managed account accepted a legacy construction: %v", err)
	}

	at := &AutoTrader{
		store:      st,
		userID:     "alice",
		exchangeID: "managed",
		exchange:   "unsupported-venue",
		config: AutoTraderConfig{StrategyConfig: &store.StrategyConfig{
			StrategyType: "grid_trading",
			GridConfig:   &store.GridStrategyConfig{Symbol: "BTCUSDT"},
		}},
	}
	if err = at.Run(); err == nil || !strings.Contains(err.Error(), "legacy autotrader") {
		t.Fatalf("managed account accepted a legacy run: %v", err)
	}
	if at.isRunning || at.gridState != nil || at.stopMonitorCh != nil {
		t.Fatal("legacy run mutated runtime state before the managed check")
	}
}

func TestManagedOwnershipBlocksPendingRun(t *testing.T) {
	st := managedOwnershipStore(t)
	ex := store.Exchange{ID: "approved", UserID: "alice", ExchangeType: "hyperliquid", HyperliquidWalletAddr: "owner", Enabled: true}
	if err := st.GormDB().Create(&ex).Error; err != nil {
		t.Fatal(err)
	}
	// ruling: an approved run may still launch, so it must block too.
	if err := st.GormDB().Create(&live.Run{ID: "run-1", UserID: "alice", ExchangeID: "approved", AccountKey: "binding-x", State: "approved"}).Error; err != nil {
		t.Fatal(err)
	}
	at := &AutoTrader{store: st, userID: "alice", exchangeID: "approved", exchange: "hyperliquid"}
	if err := at.Run(); err == nil || !strings.Contains(err.Error(), "approved") {
		t.Fatalf("approved managed run did not block the legacy run: %v", err)
	}
	if at.isRunning {
		t.Fatal("legacy run started while a managed run was approved")
	}
}
