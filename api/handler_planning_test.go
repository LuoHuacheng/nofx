package api

import (
	"context"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http"
	"net/http/httptest"
	"nofx/planning/market"
	"nofx/planning/plugins"
	"nofx/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanningPrivateRoutesRequireLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{router: gin.New()}
	s.setupRoutes()
	for _, r := range []struct{ method, path string }{{"GET", "/api/planning/setup-wallet"}, {"POST", "/api/planning/setup-wallet"}, {"GET", "/api/planning/state"}, {"GET", "/api/planning/account"}, {"POST", "/api/planning/approve"}, {"POST", "/api/planning/execute"}, {"POST", "/api/planning/chat"}, {"POST", "/api/planning/compile"}, {"POST", "/api/planning/stop"}, {"POST", "/api/planning/reconcile"}, {"GET", "/api/planning/data-plugins"}, {"POST", "/api/planning/data-plugins"}, {"GET", "/api/planning/plugin-data"}, {"GET", "/api/planning/ai500"}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want401", r.method, r.path, w.Code)
		}
	}
}

func TestPlanningShutdownDoesNotInitializeService(t *testing.T) {
	s := &Server{}
	if e := s.Shutdown(); e != nil {
		t.Fatal(e)
	}
	if p, e := s.planningService(); e == nil || p != nil {
		t.Fatal("service initialized after shutdown")
	}
}

func TestPlanningApprovalRequiresExplicitClick(t *testing.T) {
	s := &Server{}
	router := gin.New()
	router.POST("/approve", s.handlePlanningApprove)
	for _, body := range []string{`{}`, `{"confirmed":false}`, `{"confirmation":"APPROVE LIVE"}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/approve", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "explicit click") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestPlanningPluginSettingsAndDataAreOwnerScoped(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "plugin.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if e = db.AutoMigrate(&store.Exchange{}); e != nil {
		t.Fatal(e)
	}
	if e = db.Create(&store.Exchange{ID: "alice-account", UserID: "alice", ExchangeType: "binance", Enabled: true}).Error; e != nil {
		t.Fatal(e)
	}
	st, e := store.NewFromGorm(db)
	if e != nil {
		t.Fatal(e)
	}
	p := &planningServices{source: market.New()}
	s := &Server{store: st, planning: p}
	s.planningOnce.Do(func() {})
	reads := 0
	definition := plugins.Definition{Manifest: plugins.Manifest{ID: "funding", Name: "Funding", Version: "1", Capability: "public.read", Enabled: true}, Read: func(_ context.Context, q plugins.Request) (plugins.Result, error) {
		reads++
		return plugins.Result{Venue: q.Venue, Symbol: q.Symbol, Source: "local fixture", Observed: time.Now().UTC(), Mode: "public", Metrics: []plugins.Metric{{Key: "funding_rate", Label: "Funding", Value: "0.0001", Unit: "fraction"}}}, nil
	}}
	alice, _ := plugins.Open("", definition)
	bob, _ := plugins.Open("", definition)
	p.dataPlugins.Store("alice", alice)
	p.dataPlugins.Store("bob", bob)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", c.GetHeader("Test-User")) })
	router.POST("/plugins", s.handlePlanningDataPlugins)
	router.GET("/data", s.handlePlanningPluginData)
	request := func(method, path, user, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Test-User", user)
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		return w
	}
	path := "/data?exchange_id=alice-account&symbol=BTCUSDT&plugin=funding"
	if w := request("GET", path, "bob", ""); w.Code != 400 || reads != 0 {
		t.Fatal("cross-user data/account access", w.Code, reads)
	}
	if w := request("GET", path, "alice", ""); w.Code != 200 || reads != 1 {
		t.Fatal(w.Body.String())
	}
	if w := request("POST", "/plugins", "alice", `{"id":"funding","enabled":false}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request("GET", path, "alice", ""); w.Code != 400 || reads != 1 {
		t.Fatal("disabled plugin invoked source", w.Code, reads)
	}
	if !bob.List()[0].Enabled {
		t.Fatal("one user's toggle changed another user")
	}
}

func TestPlanningMarketDoesNotRequireExecutionAccount(t *testing.T) {
	s := &Server{}
	for _, venue := range []string{"binance", "hyperliquid"} {
		workspace, err := s.planningWorkspace("alice", "", venue)
		if err != nil || workspace.ID != "" || workspace.ExchangeType != venue {
			t.Fatalf("%+v %v", workspace, err)
		}
	}
	if _, err := s.planningWorkspace("alice", "", "unknown"); err == nil {
		t.Fatal("unsupported venue accepted")
	}
	// Execution still requires a real account; the empty planning binding is
	// not promoted to an enabled account or an execution broker.
	workspace, _ := s.planningWorkspace("alice", "", "binance")
	if workspace.Enabled || workspace.APIKey != "" {
		t.Fatal("planning workspace has trading authority")
	}
}
