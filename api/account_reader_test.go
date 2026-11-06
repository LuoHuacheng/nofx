package api

import (
	"context"
	"nofx/store"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAccountInspectionDoesNotNeedAutopilotOrModel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "account.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err := db.AutoMigrate(&store.Exchange{}, &store.Trader{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{
		&store.Exchange{ID: "alice-account", UserID: "alice", ExchangeType: "binance", Enabled: true},
		&store.Exchange{ID: "bob-account", UserID: "bob", ExchangeType: "binance", Enabled: true},
		&store.Exchange{ID: "standalone-account", UserID: "alice", ExchangeType: "binance", Enabled: true},
		&store.Trader{ID: "alice-trader", UserID: "alice", ExchangeID: "alice-account", AIModelID: "missing-model"},
		&store.Trader{ID: "bob-trader", UserID: "bob", ExchangeID: "bob-account", AIModelID: "missing-model"},
		// Even a corrupt cross-user exchange reference cannot grant access.
		&store.Trader{ID: "bad-reference", UserID: "alice", ExchangeID: "bob-account", AIModelID: "missing-model"},
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: st} // No autonomous trader manager, model or strategy.
	standalone, err := s.accountReadConfig("alice", "", "standalone-account")
	if err != nil || standalone.ID != "standalone-account" {
		t.Fatalf("saved account required an Autopilot: %v %v", standalone, err)
	}
	for _, tc := range [][3]string{{"alice", "", "bob-account"}, {"", "", "standalone-account"}, {"alice", "alice-trader", "standalone-account"}, {"alice", "", "unknown"}} {
		if _, err := s.accountReadConfig(tc[0], tc[1], tc[2]); err == nil {
			t.Fatalf("direct account ownership bypass: %v", tc)
		}
	}
	ex, err := s.accountReadConfig("alice", "alice-trader")
	if err != nil || ex.ID != "alice-account" {
		t.Fatalf("own account: %v %v", ex, err)
	}
	for _, tc := range [][2]string{{"alice", "bob-trader"}, {"alice", "bad-reference"}, {"", "alice-trader"}, {"alice", "unknown"}} {
		if _, err := s.accountReadConfig(tc[0], tc[1]); err == nil {
			t.Fatalf("ownership bypass: %v", tc)
		}
	}
	if err := db.Model(&store.Exchange{}).Where("id = ?", "alice-account").Update("testnet", true).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.accountReadConfig("alice", "alice-trader"); err == nil {
		t.Fatal("testnet inspection fell back to mainnet")
	}
	if _, err := s.accountReadConfig("alice", "", "alice-account"); err == nil {
		t.Fatal("direct testnet inspection fell back to mainnet")
	}
	if _, _, err := s.planningBroker(context.Background(), "alice", "alice-account"); err == nil {
		t.Fatal("testnet execution fell back to mainnet")
	}
	if err := db.Model(&store.Exchange{}).Where("id = ?", "alice-account").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.accountReadConfig("alice", "alice-trader"); err == nil {
		t.Fatal("disabled account was readable")
	}
	if _, err := s.accountReadConfig("alice", "", "alice-account"); err == nil {
		t.Fatal("disabled account was directly readable")
	}
}
