package trader

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/planning"
	"nofx/planning/live"
	"nofx/store"
	"path/filepath"
	"testing"
)

func TestPlanningLeaseBlocksConstructorAndRestoration(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "guard.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if e = db.AutoMigrate(&store.Exchange{}, &live.Lease{}); e != nil {
		t.Fatal(e)
	}
	ex := store.Exchange{ID: "ex", UserID: "alice", ExchangeType: "hyperliquid", HyperliquidWalletAddr: "owner", Enabled: true}
	if e = db.Create(&ex).Error; e != nil {
		t.Fatal(e)
	}
	if e = db.Create(&live.Lease{AccountKey: planning.AccountIdentity("hyperliquid", "", "owner"), RunID: "locked", UserID: "alice", ExchangeID: "ex"}).Error; e != nil {
		t.Fatal(e)
	}
	st, e := store.NewFromGorm(db)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewAutoTrader(AutoTraderConfig{ExchangeID: "ex"}, st, "alice"); e == nil {
		t.Fatal("constructor escaped unresolved lease")
	}
	at := &AutoTrader{store: st, userID: "alice", exchangeID: "ex"}
	if e = at.Run(); e == nil {
		t.Fatal("restoration escaped unresolved lease")
	}
	if at.isRunning {
		t.Fatal("marked runtime active on rejected launch")
	}
}
