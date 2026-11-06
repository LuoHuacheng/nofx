package trader

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/planning/live"
	"nofx/store"
	"nofx/trader/execution"
	"nofx/trader/okx"
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

type recordingTransport struct{ calls []string }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	const okxAccountConfig = `{"code":"0","msg":"","data":[{"uid":"uid-1","posMode":"net_mode"}]}`
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(okxAccountConfig)),
		Request:    req,
	}, nil
}

// TestManagedOwnershipIdentityPrecedesVenueWrite proves the OKX ownership check
// reads the UID with a credential-only client: the legacy constructor rewrites
// the position mode, which must not happen for a refused configuration.
func TestManagedOwnershipIdentityPrecedesVenueWrite(t *testing.T) {
	st := managedOwnershipStore(t)
	ex := store.Exchange{ID: "second-okx", UserID: "alice", ExchangeType: "okx", APIKey: "second-key", SecretKey: "second-secret", Passphrase: "second-pass", Enabled: true}
	if err := st.GormDB().Create(&ex).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.GormDB().Create(&execution.Binding{ID: "binding-bob", UserID: "bob", ExchangeID: "bobs-okx", Venue: "okx", Environment: execution.EnvironmentLive, VenueAccountID: "uid-1", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	transport := &recordingTransport{}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = original }()

	_, err := NewAutoTrader(AutoTraderConfig{
		ExchangeID: "second-okx", Exchange: "okx",
		OKXAPIKey: "second-key", OKXSecretKey: "second-secret", OKXPassphrase: "second-pass",
		InitialBalance: 100,
	}, st, "alice")
	if err == nil || !strings.Contains(err.Error(), "managed under another") {
		t.Fatalf("duplicate physical account accepted: %v", err)
	}
	if len(transport.calls) != 1 || transport.calls[0] != "GET /api/v5/account/config" {
		t.Fatalf("pre-switch ownership check touched the venue: %v", transport.calls)
	}
	for _, call := range transport.calls {
		if strings.HasPrefix(call, "POST") {
			t.Fatalf("legacy constructor wrote to the managed account before the refusal: %v", transport.calls)
		}
	}

	// The fixture does observe the legacy constructor's write, so the assertion
	// above would catch a regression.
	transport.calls = nil
	okx.NewOKXTrader("second-key", "second-secret", "second-pass")
	wrote := false
	for _, call := range transport.calls {
		if call == "POST /api/v5/account/set-position-mode" {
			wrote = true
		}
	}
	if !wrote {
		t.Fatalf("fixture did not observe the legacy position-mode write: %v", transport.calls)
	}
}

// TestManagedOwnershipRequiresExchangeConfig pins the fail-closed behaviour for
// configurations that cannot be tied to an exchange row.
func TestManagedOwnershipRequiresExchangeConfig(t *testing.T) {
	st := managedOwnershipStore(t)
	if _, err := NewAutoTrader(AutoTraderConfig{Exchange: "okx", InitialBalance: 100}, st, "alice"); err == nil || !strings.Contains(err.Error(), "no exchange configuration") {
		t.Fatalf("constructor accepted a configuration without an exchange: %v", err)
	}
	at := &AutoTrader{store: st, userID: "alice", exchange: "okx"}
	if err := at.Run(); err == nil || !strings.Contains(err.Error(), "no exchange configuration") {
		t.Fatalf("run accepted a configuration without an exchange: %v", err)
	}
	if at.isRunning {
		t.Fatal("run started without an exchange configuration")
	}
}
