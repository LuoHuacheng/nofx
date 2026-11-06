package manager

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

// TestManagedOwnershipBlocksLegacyRestore covers the restart path: a trader
// marked running in the database must not be rebuilt for a managed account.
func TestManagedOwnershipBlocksLegacyRestore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "manager.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	if err = db.AutoMigrate(&store.Exchange{}, &store.Trader{}, &store.AIModel{}, &store.Strategy{}, &execution.Binding{}, &live.Run{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	rows := []any{
		&store.Exchange{ID: "managed", UserID: "alice", ExchangeType: "hyperliquid", HyperliquidWalletAddr: "owner", HyperliquidBuilderApproved: true, Enabled: true},
		&store.AIModel{ID: "model", UserID: "alice", Provider: "deepseek", Enabled: true},
		&store.Strategy{ID: "strategy", UserID: "alice", Name: "strategy", Config: "{}"},
		&store.Trader{ID: "trader-1", UserID: "alice", Name: "legacy", AIModelID: "model", ExchangeID: "managed", StrategyID: "strategy", IsRunning: true, InitialBalance: 100, ScanIntervalMinutes: 1},
		&execution.Binding{ID: "binding-1", UserID: "alice", ExchangeID: "managed", Venue: "hyperliquid", Environment: execution.EnvironmentLive, Enabled: true},
	}
	for _, row := range rows {
		if err = db.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}

	tm := NewTraderManager()
	if err = tm.LoadUserTradersFromStore(st, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = tm.GetTrader("trader-1"); err == nil {
		t.Fatal("manager restored a trader for a managed account")
	}
	loadErr := tm.GetLoadError("trader-1")
	if loadErr == nil || !strings.Contains(loadErr.Error(), "legacy autotrader") {
		t.Fatalf("load error lost the managed-ownership refusal: %v", loadErr)
	}
}
