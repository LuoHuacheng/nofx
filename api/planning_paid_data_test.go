package api

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"nofx/crypto"
	"nofx/planning/core"
	"nofx/planning/plugins"
	"nofx/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanningProfitNeverBypassesPaidProvider(t *testing.T) {
	// No store or credentials are required to reject an unpublished paid route.
	reader := (&Server{}).planningPaidData("alice")
	_, err := reader(context.Background(), "hyperdata_profit", plugins.Request{Venue: "hyperliquid", Symbol: "BTC"})
	if err == nil || !strings.Contains(err.Error(), "Public-origin fallback is disabled") {
		t.Fatal(err)
	}
}
func TestPlanningAI500PaidCacheAndUserIsolation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ai500.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err = db.AutoMigrate(&store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"alice", "bob"} {
		if err = db.Create(&store.AIModel{ID: u + "-model", UserID: u, Provider: "claw402", Enabled: true, APIKey: crypto.EncryptedString(u + "-fixture-key")}).Error; err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]int{}
	p := &planningServices{ai500Read: func(_ context.Context, key string) ([]byte, error) {
		reads[key]++
		if key == "bob-fixture-key" {
			return nil, errors.New("ambiguous paid request")
		}
		return []byte(`{"success":true,"data":{"coins":[{"pair":"BTCUSDT","score":85},{"pair":"ETHUSDT","score":82},{"pair":"bad/symbol","score":99}]}}`), nil
	}}
	s := &Server{store: st, planning: p}
	s.planningOnce.Do(func() {})
	request := func(user string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user_id", user)
		c.Request = httptest.NewRequest("GET", "/api/planning/ai500", nil)
		s.handlePlanningAI500(c)
		return w
	}
	for i := 0; i < 2; i++ {
		w := request("alice")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ETHUSDT") || strings.Contains(w.Body.String(), "bad/symbol") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	for i := 0; i < 2; i++ {
		w := request("bob")
		if w.Code != 400 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if reads["alice-fixture-key"] != 1 || reads["bob-fixture-key"] != 1 {
		t.Fatal("paid requests replayed or crossed user scope", reads)
	}
	for _, tc := range []struct {
		user, venue, symbol, group string
		restricted                 bool
	}{
		{"alice", "binance", "ETHUSDT", "", true},
		{"alice", "binance", "XMRUSDT", "", false},
		{"bob", "binance", "ETHUSDT", "", false},
		{"alice", "hyperliquid", "ETH", "", false},
		{"alice", "binance", "QUSDT", "ai500", true},
	} {
		limits := p.planningEntryPolicy(tc.user, tc.venue, tc.symbol, tc.group, core.Constraints{})
		if (limits.EntryPolicy == core.EntryAI500TrendLong) != tc.restricted {
			t.Fatalf("unexpected entry policy for %+v: %+v", tc, limits)
		}
	}
	if reads["alice-fixture-key"] != 1 || reads["bob-fixture-key"] != 1 {
		t.Fatal("entry policy caused another paid request")
	}
}
