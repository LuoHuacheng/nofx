package store

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"nofx/crypto"
)

func TestExchangeStartupPreservesDisabledAccount(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "exchange.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	s := NewExchangeStore(db)
	if err := s.initTables(); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		id := "disabled"
		if enabled {
			id = "enabled"
		}
		row := Exchange{ID: id, UserID: "owner", ExchangeType: "binance", AccountName: id, APIKey: crypto.EncryptedString("test-key"), SecretKey: crypto.EncryptedString("test-secret"), Testnet: true, Enabled: enabled}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.initTables(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"disabled", "enabled"} {
		row, err := s.GetByID("owner", id)
		if err != nil {
			t.Fatalf("startup deleted account %s: %v", id, err)
		}
		if row.Enabled != (id == "enabled") {
			t.Errorf("startup changed enabled status for %s to %v", id, row.Enabled)
		}
		if row.APIKey != "test-key" || row.SecretKey != "test-secret" || !row.Testnet {
			t.Errorf("startup changed saved configuration for %s", id)
		}
	}
}
