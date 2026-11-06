package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"nofx/config"
	"nofx/manager"
	"nofx/planning/live"
	"nofx/planning/market"
	"nofx/store"
	"nofx/trader/execution"
	"path/filepath"
	"strings"
	"testing"
)

type fakeManagedTrader struct {
	account            execution.ManagedAccount
	route              string
	leverage, prepares int
}

func (f *fakeManagedTrader) Venue() string { return f.account.Venue }
func (f *fakeManagedTrader) AccountSnapshot(context.Context, string) (execution.Account, error) {
	return execution.Account{}, nil
}
func (f *fakeManagedTrader) PrepareExecution(context.Context, string) error { f.prepares++; return nil }
func (f *fakeManagedTrader) SubmitOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	return o, nil
}
func (f *fakeManagedTrader) LookupOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	return o, nil
}
func (f *fakeManagedTrader) CancelExecutionOrder(context.Context, execution.Order) error { return nil }
func (f *fakeManagedTrader) RouteClientID(id string) (string, error) {
	f.route = "okx-" + id
	return f.route, nil
}
func (f *fakeManagedTrader) PrepareExecutionLeverage(_ context.Context, _ string, leverage int) error {
	f.leverage = leverage
	return nil
}
func (f *fakeManagedTrader) ManagedSnapshot(context.Context) (execution.ManagedAccount, error) {
	return f.account, nil
}
func (f *fakeManagedTrader) VerifyManagedSettings(context.Context, string, int) error {
	return nil
}
func (f *fakeManagedTrader) ManagedCapabilities() execution.Capabilities {
	return execution.Capabilities{}
}
func (f *fakeManagedTrader) AmendStop(_ context.Context, o execution.Order, _ execution.Decimal) (execution.Order, error) {
	return o, execution.ErrUnsupported
}
func (f *fakeManagedTrader) Events(context.Context, func(execution.AccountEvent) error) error {
	return execution.ErrUnsupported
}

// fakeIdentityTrader simulates an adapter that can read a signed venue UID.
type fakeIdentityTrader struct {
	*fakeManagedTrader
	identity string
	err      error
}

func (f fakeIdentityTrader) VenueAccountID(context.Context) (string, error) {
	return f.identity, f.err
}

type managedFixture struct {
	server       *Server
	db           *gorm.DB
	router       *gin.Engine
	okx          *fakeManagedTrader
	binance      *fakeManagedTrader
	environments map[string]execution.Environment
}

func newManagedFixture(t *testing.T) *managedFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "managed.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	if err = db.AutoMigrate(&store.Exchange{}, &store.Trader{}, &execution.Binding{}); err != nil {
		t.Fatal(err)
	}
	exchanges := []store.Exchange{
		{ID: "alice-okx-demo", UserID: "alice", ExchangeType: "okx", AccountName: "OKX demo", Name: "OKX demo", Type: "cex", Enabled: true, Testnet: true},
		{ID: "alice-okx-live", UserID: "alice", ExchangeType: "okx", AccountName: "OKX live", Name: "OKX live", Type: "cex", Enabled: true, APIKey: "live-key", SecretKey: "live-secret", Passphrase: "live-pass"},
		{ID: "alice-okx-other", UserID: "alice", ExchangeType: "okx", AccountName: "OKX other", Name: "OKX other", Type: "cex", Enabled: true, Testnet: true},
		{ID: "alice-okx-broken", UserID: "alice", ExchangeType: "okx", AccountName: "OKX broken", Name: "OKX broken", Type: "cex", Enabled: true, Testnet: true},
		{ID: "alice-binance-demo", UserID: "alice", ExchangeType: "binance", AccountName: "Binance demo", Name: "Binance demo", Type: "cex", Enabled: true, Testnet: true},
		{ID: "alice-hyperliquid", UserID: "alice", ExchangeType: "hyperliquid", AccountName: "Hyperliquid", Name: "Hyperliquid", Type: "dex", Enabled: true},
		{ID: "bob-okx-demo", UserID: "bob", ExchangeType: "okx", AccountName: "Bob OKX", Name: "Bob OKX", Type: "cex", Enabled: true, Testnet: true},
	}
	if err = db.Create(&exchanges).Error; err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, planning: &planningServices{source: market.New()}}
	s.planningOnce.Do(func() {})
	f := &managedFixture{
		server:       s,
		db:           db,
		okx:          &fakeManagedTrader{account: execution.ManagedAccount{AccountKey: "venue-key", Venue: "okx"}},
		binance:      &fakeManagedTrader{account: execution.ManagedAccount{AccountKey: "venue-key", Venue: "binance"}},
		environments: map[string]execution.Environment{},
	}
	s.managedTraderFactory = func(_ context.Context, ex *store.Exchange, env execution.Environment) (execution.ManagedTrader, error) {
		f.environments[ex.ID] = env
		switch ex.ID {
		case "alice-binance-demo":
			return f.binance, nil
		case "alice-okx-other":
			return fakeIdentityTrader{f.okx, "okx-uid-2", nil}, nil
		case "alice-okx-broken":
			return fakeIdentityTrader{f.okx, "", errors.New("signed read failed")}, nil
		case "alice-okx-live", "alice-okx-live-2":
			return fakeIdentityTrader{f.okx, "okx-uid-live", nil}, nil
		}
		return fakeIdentityTrader{f.okx, "okx-uid-1", nil}, nil
	}
	f.router = gin.New()
	f.router.Use(func(c *gin.Context) { c.Set("user_id", c.GetHeader("Test-User")) })
	f.router.POST("/bindings", s.handleManagedAccountBinding)
	return f
}

func (f *managedFixture) bind(user string, in managedBindingRequest) *httptest.ResponseRecorder {
	body, _ := json.Marshal(in)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/bindings", strings.NewReader(string(body)))
	r.Header.Set("Test-User", user)
	r.Header.Set("Content-Type", "application/json")
	f.router.ServeHTTP(w, r)
	return w
}

func decodeBinding(t *testing.T, w *httptest.ResponseRecorder) execution.Binding {
	t.Helper()
	var b execution.Binding
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestManagedBindingEnvironment(t *testing.T) {
	f := newManagedFixture(t)
	ctx := context.Background()

	for id, want := range map[string]execution.Environment{
		"alice-okx-demo":     execution.EnvironmentSandbox,
		"alice-okx-live":     execution.EnvironmentLive,
		"alice-binance-demo": execution.EnvironmentSandbox,
		"alice-hyperliquid":  "",
	} {
		ex, err := f.server.store.Exchange().GetByID("alice", id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := managedEnvironment(ex)
		if id == "alice-hyperliquid" {
			if err == nil {
				t.Fatal("unsupported venue accepted")
			}
			continue
		}
		if err != nil || got != want {
			t.Fatalf("%s environment = %q, %v", id, got, err)
		}
	}

	// OKX demo credentials bind through the managed factory as sandbox.
	bindResponse := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-demo", VenueAccountID: "okx-uid-1", Environment: "sandbox"})
	if bindResponse.Code != 200 {
		t.Fatalf("OKX demo bind = %d %s", bindResponse.Code, bindResponse.Body.String())
	}
	if f.environments["alice-okx-demo"] != execution.EnvironmentSandbox {
		t.Fatalf("OKX demo factory env = %q", f.environments["alice-okx-demo"])
	}
	demo := decodeBinding(t, bindResponse)
	if !demo.Enabled || demo.VerifiedAt.IsZero() {
		t.Fatalf("verified binding not enabled: %+v", demo)
	}
	ex, _ := f.server.store.Exchange().GetByID("alice", "alice-okx-demo")
	if demo.ID == accountKey(ex) {
		t.Fatal("binding reused the credential hash as the physical identity")
	}

	// Managed execution receives the binding ID and keeps routing/leverage.
	trader, bound, err := f.server.managedTrader(ctx, "alice", "alice-okx-demo")
	if err != nil {
		t.Fatal(err)
	}
	if bound.ID != demo.ID {
		t.Fatalf("managed binding = %+v", bound)
	}
	snap, err := trader.ManagedSnapshot(ctx)
	if err != nil || snap.AccountKey != demo.ID {
		t.Fatalf("snapshot key = %q, %v", snap.AccountKey, err)
	}
	broker := live.NOFXBroker{Trader: trader}
	if id, err := broker.RouteClientID("abc"); err != nil || id != "okx-abc" {
		t.Fatalf("route = %q, %v", id, err)
	}
	if err := broker.PrepareLeverage(ctx, "BTC-USDT-SWAP", 3); err != nil {
		t.Fatal(err)
	}
	if f.okx.leverage != 3 || f.okx.prepares != 0 {
		t.Fatalf("leverage = %d, prepares = %d", f.okx.leverage, f.okx.prepares)
	}

	// Binance testnet also passes through the sandbox factory, but without an
	// immutable UID it stays disabled until dedicated-account confirmation.
	if w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-binance-demo", VenueAccountID: "binance-uid-1", Environment: "sandbox"}); w.Code == 200 {
		t.Fatal("unconfirmed identity binding accepted")
	}
	confirmed := f.bind("alice", managedBindingRequest{ExchangeID: "alice-binance-demo", VenueAccountID: "binance-uid-1", Environment: "sandbox", ConfirmDedicatedAccount: true})
	if confirmed.Code != 200 {
		t.Fatalf("confirmed bind = %d %s", confirmed.Code, confirmed.Body.String())
	}
	if f.environments["alice-binance-demo"] != execution.EnvironmentSandbox {
		t.Fatalf("Binance testnet factory env = %q", f.environments["alice-binance-demo"])
	}
	binance := decodeBinding(t, confirmed)
	if binance.Enabled || !binance.VerifiedAt.IsZero() {
		t.Fatalf("unverified binding enabled: %+v", binance)
	}
	if _, _, err := f.server.managedTrader(ctx, "alice", "alice-binance-demo"); err == nil {
		t.Fatal("unverified binding entered managed execution")
	}

	// Environment, ownership, identity and input are all checked.
	for name, in := range map[string]managedBindingRequest{
		"env mismatch":       {ExchangeID: "alice-okx-demo", VenueAccountID: "okx-uid-1", Environment: "live"},
		"empty identity":     {ExchangeID: "alice-okx-demo", VenueAccountID: "  ", Environment: "sandbox"},
		"foreign owner":      {ExchangeID: "bob-okx-demo", VenueAccountID: "okx-uid-1", Environment: "sandbox"},
		"identity mismatch":  {ExchangeID: "alice-okx-other", VenueAccountID: "okx-uid-1", Environment: "sandbox"},
		"identity read fail": {ExchangeID: "alice-okx-broken", VenueAccountID: "okx-uid-1", Environment: "sandbox"},
	} {
		if w := f.bind("alice", in); w.Code != 400 {
			t.Fatalf("%s = %d %s", name, w.Code, w.Body.String())
		}
	}
	if _, err := execution.ResolveBinding(ctx, f.db, "alice", "alice-okx-other"); err == nil {
		t.Fatal("rejected identity was persisted")
	}

	// Manual planning account selection is unchanged: testnet stays excluded.
	if _, err := f.server.planningAccount("alice", "alice-okx-other"); err == nil || !strings.Contains(err.Error(), "live mainnet") {
		t.Fatalf("manual planning selection changed: %v", err)
	}

	// If the saved endpoint changes under the binding, execution fails closed.
	if err := f.db.Model(&store.Exchange{}).Where("id = ?", "alice-okx-demo").Update("testnet", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.server.managedTrader(ctx, "alice", "alice-okx-demo"); err == nil {
		t.Fatal("environment mismatch entered managed execution")
	}
}

func TestManagedBindingRotation(t *testing.T) {
	f := newManagedFixture(t)
	ctx := context.Background()
	initial := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"})
	if initial.Code != 200 {
		t.Fatalf("bind = %d %s", initial.Code, initial.Body.String())
	}
	first := decodeBinding(t, initial)

	// Rotating the stored credentials of the same configuration keeps the ID.
	if err := f.db.Model(&store.Exchange{}).Where("id = ?", "alice-okx-live").Update("api_key", "rotated-key").Error; err != nil {
		t.Fatal(err)
	}
	rotation := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"})
	if rotation.Code != 200 {
		t.Fatalf("rotation = %d %s", rotation.Code, rotation.Body.String())
	}
	if rotated := decodeBinding(t, rotation); rotated.ID != first.ID {
		t.Fatal("rotation changed physical account binding")
	}
	if _, _, err := f.server.managedTrader(ctx, "alice", "alice-okx-live"); err != nil {
		t.Fatalf("rotated binding unusable: %v", err)
	}

	// A second credential configuration for the same physical account fails.
	if err := f.db.Create(&store.Exchange{ID: "alice-okx-live-2", UserID: "alice", ExchangeType: "okx", AccountName: "dup", Name: "dup", Type: "cex", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live-2", VenueAccountID: "okx-uid-live", Environment: "live"}); w.Code != 400 {
		t.Fatalf("second credential configuration accepted: %d %s", w.Code, w.Body.String())
	}

	// The existing configuration cannot be moved to another physical account.
	if w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-moved", Environment: "live"}); w.Code != 400 {
		t.Fatalf("configuration moved to another account: %d %s", w.Code, w.Body.String())
	}

	// Historical (unbound) accounts keep the credential-derived key, and a
	// verified binding replaces it without rewriting old rows.
	if _, key, err := f.server.planningBroker(ctx, "alice", "alice-okx-live"); err != nil || key != first.ID {
		t.Fatalf("planning key after binding = %q, %v", key, err)
	}
	if err := f.db.Create(&store.Exchange{ID: "alice-okx-legacy", UserID: "alice", ExchangeType: "okx", AccountName: "legacy", Name: "legacy", Type: "cex", Enabled: true, APIKey: "legacy-key", SecretKey: "legacy-secret", Passphrase: "legacy-pass"}).Error; err != nil {
		t.Fatal(err)
	}
	legacy, err := f.server.store.Exchange().GetByID("alice", "alice-okx-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, key, err := f.server.planningBroker(ctx, "alice", "alice-okx-legacy"); err != nil || key != accountKey(legacy) {
		t.Fatalf("legacy planning key = %q, %v", key, err)
	}
}

func (f *managedFixture) call(method, path, user, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Test-User", user)
	r.Header.Set("Content-Type", "application/json")
	f.router.ServeHTTP(w, r)
	return w
}

// withExchangeRoutes exposes the real exchange mutation handlers, the boundary
// that must void a binding when credentials or the endpoint change.
func (f *managedFixture) withExchangeRoutes(t *testing.T) *managedFixture {
	t.Setenv("JWT_SECRET", strings.Repeat("t", 32))
	config.Init()
	f.server.traderManager = manager.NewTraderManager()
	f.server.exchangeAccountStateCache = &ExchangeAccountStateCache{}
	f.router.PUT("/api/exchanges", f.server.handleUpdateExchangeConfigs)
	f.router.DELETE("/api/exchanges/:id", f.server.handleDeleteExchange)
	return f
}

func TestManagedBindingRejectsDisabledExchange(t *testing.T) {
	f := newManagedFixture(t)
	if err := f.db.Model(&store.Exchange{}).Where("id = ?", "alice-okx-demo").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-demo", VenueAccountID: "okx-uid-1", Environment: "sandbox"}); w.Code != 400 {
		t.Fatalf("disabled exchange bound: %d %s", w.Code, w.Body.String())
	}
	if _, err := execution.ResolveBinding(context.Background(), f.db, "alice", "alice-okx-demo"); err == nil {
		t.Fatal("disabled exchange was persisted")
	}
}

func TestManagedBindingKeepsCredentialsOut(t *testing.T) {
	f := newManagedFixture(t)
	w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"})
	if w.Code != 200 {
		t.Fatalf("bind = %d %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"id": true, "user_id": true, "exchange_id": true, "venue": true, "environment": true, "venue_account_id": true, "verified_at": true, "enabled": true}
	for key := range payload {
		if !allowed[key] {
			t.Fatalf("binding response leaked field %q", key)
		}
	}
	columns, err := f.db.Migrator().ColumnTypes(&execution.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		switch column.Name() {
		case "api_key", "secret_key", "passphrase":
			t.Fatalf("binding table stores credentials in %q", column.Name())
		}
	}
}

func TestManagedBindingInvalidatedByCredentialSwap(t *testing.T) {
	f := newManagedFixture(t).withExchangeRoutes(t)
	ctx := context.Background()
	w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"})
	if w.Code != 200 {
		t.Fatalf("bind = %d %s", w.Code, w.Body.String())
	}
	first := decodeBinding(t, w)

	if w := f.call("PUT", "/api/exchanges", "alice", `{"exchanges":{"alice-okx-live":{"api_key":"rotated-key"}}}`); w.Code != 200 {
		t.Fatalf("credential rotation = %d %s", w.Code, w.Body.String())
	}
	stale, err := execution.ResolveBinding(ctx, f.db, "alice", "alice-okx-live")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Enabled || !stale.VerifiedAt.IsZero() {
		t.Fatalf("rotated credentials inherited the proof: %+v", stale)
	}
	if _, _, err := f.server.managedTrader(ctx, "alice", "alice-okx-live"); err == nil {
		t.Fatal("rotated credentials entered managed execution")
	}

	// Re-verifying the same account restores execution under the same key.
	w = f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"})
	if w.Code != 200 {
		t.Fatalf("re-verify = %d %s", w.Code, w.Body.String())
	}
	if again := decodeBinding(t, w); again.ID != first.ID {
		t.Fatal("re-verifying the same account changed its key")
	}
	if _, _, err := f.server.managedTrader(ctx, "alice", "alice-okx-live"); err != nil {
		t.Fatal(err)
	}
}

func TestManagedBindingEnvironmentFlipRebinds(t *testing.T) {
	f := newManagedFixture(t).withExchangeRoutes(t)
	ctx := context.Background()
	w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"})
	if w.Code != 200 {
		t.Fatalf("bind = %d %s", w.Code, w.Body.String())
	}
	first := decodeBinding(t, w)

	if w := f.call("PUT", "/api/exchanges", "alice", `{"exchanges":{"alice-okx-live":{"testnet":true}}}`); w.Code != 200 {
		t.Fatalf("testnet flip = %d %s", w.Code, w.Body.String())
	}
	if stale, err := execution.ResolveBinding(ctx, f.db, "alice", "alice-okx-live"); err != nil || stale.Enabled || !stale.VerifiedAt.IsZero() {
		t.Fatalf("testnet flip kept the proof: %+v, %v", stale, err)
	}
	if w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"}); w.Code != 400 {
		t.Fatalf("stale environment bound: %d %s", w.Code, w.Body.String())
	}

	w = f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "sandbox"})
	if w.Code != 200 {
		t.Fatalf("sandbox rebind = %d %s", w.Code, w.Body.String())
	}
	if again := decodeBinding(t, w); again.ID != first.ID || again.Environment != execution.EnvironmentSandbox {
		t.Fatalf("environment flip = %+v, first %+v", again, first)
	}
	if f.environments["alice-okx-live"] != execution.EnvironmentSandbox {
		t.Fatalf("rebind factory env = %q", f.environments["alice-okx-live"])
	}
	trader, _, err := f.server.managedTrader(ctx, "alice", "alice-okx-live")
	if err != nil {
		t.Fatal(err)
	}
	if snap, err := trader.ManagedSnapshot(ctx); err != nil || snap.AccountKey != first.ID {
		t.Fatalf("snapshot key after flip = %q, %v", snap.AccountKey, err)
	}
}

func TestManagedBindingDeletedWithExchange(t *testing.T) {
	f := newManagedFixture(t).withExchangeRoutes(t)
	ctx := context.Background()
	w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"})
	if w.Code != 200 {
		t.Fatalf("bind = %d %s", w.Code, w.Body.String())
	}
	first := decodeBinding(t, w)

	if w := f.call("DELETE", "/api/exchanges/alice-okx-live", "alice", ""); w.Code != 200 {
		t.Fatalf("delete exchange = %d %s", w.Code, w.Body.String())
	}
	if _, err := execution.ResolveBinding(ctx, f.db, "alice", "alice-okx-live"); err == nil {
		t.Fatal("deleted exchange kept its binding")
	}

	// A re-created configuration can bind the same physical account again.
	if err := f.db.Create(&store.Exchange{ID: "alice-okx-live-2", UserID: "alice", ExchangeType: "okx", AccountName: "recreated", Name: "recreated", Type: "cex", Enabled: true, APIKey: "live-2-key", SecretKey: "live-2-secret", Passphrase: "live-2-pass"}).Error; err != nil {
		t.Fatal(err)
	}
	w = f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-live-2", VenueAccountID: "okx-uid-live", Environment: "live"})
	if w.Code != 200 {
		t.Fatalf("rebind after delete = %d %s", w.Code, w.Body.String())
	}
	if again := decodeBinding(t, w); again.ID == first.ID {
		t.Fatal("re-created configuration reused the deleted binding")
	}
	if _, _, err := f.server.managedTrader(ctx, "alice", "alice-okx-live-2"); err != nil {
		t.Fatal(err)
	}
}

func TestManagedBindingVerifiedTakeover(t *testing.T) {
	f := newManagedFixture(t)
	ctx := context.Background()
	stub, err := execution.BindManagedAccount(ctx, f.db, execution.Binding{UserID: "bob", ExchangeID: "bob-okx-demo", Venue: "okx", Environment: execution.EnvironmentSandbox, VenueAccountID: "okx-uid-1"})
	if err != nil || stub.Enabled {
		t.Fatalf("stub = %+v, %v", stub, err)
	}
	w := f.bind("alice", managedBindingRequest{ExchangeID: "alice-okx-demo", VenueAccountID: "okx-uid-1", Environment: "sandbox"})
	if w.Code != 200 {
		t.Fatalf("verified takeover = %d %s", w.Code, w.Body.String())
	}
	claimed := decodeBinding(t, w)
	if claimed.ID != stub.ID || !claimed.Enabled || claimed.VerifiedAt.IsZero() || claimed.ExchangeID != "alice-okx-demo" {
		t.Fatalf("takeover = %+v, stub %+v", claimed, stub)
	}
	if _, err := execution.ResolveBinding(ctx, f.db, "bob", "bob-okx-demo"); err == nil {
		t.Fatal("unverified stub kept its owner after takeover")
	}
}

// TestManagedOwnershipEnablesOnlyWhenIdle keeps the ordering of the enable
// path: the legacy autotrader has to be stopped before the binding is enabled.
func TestManagedOwnershipEnablesOnlyWhenIdle(t *testing.T) {
	f := newManagedFixture(t)
	legacy := store.Trader{ID: "legacy", UserID: "alice", Name: "legacy", ExchangeID: "alice-okx-live", IsRunning: true}
	if err := f.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	in := managedBindingRequest{ExchangeID: "alice-okx-live", VenueAccountID: "okx-uid-live", Environment: "live"}
	if w := f.bind("alice", in); w.Code != 400 {
		t.Fatalf("binding enabled while the legacy autotrader runs: %d %s", w.Code, w.Body.String())
	}
	if _, err := execution.ResolveBinding(context.Background(), f.db, "alice", "alice-okx-live"); err == nil {
		t.Fatal("refused enable persisted the binding")
	}
	if err := f.db.Model(&store.Trader{}).Where("id = ?", "legacy").Update("is_running", false).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.bind("alice", in); w.Code != 200 {
		t.Fatalf("idle account refused: %d %s", w.Code, w.Body.String())
	}
}

// TestManagedOwnershipBlocksLegacy proves the API start path cannot construct a
// trader for a managed account, so no venue settings or orders can be written.
func TestManagedOwnershipBlocksLegacy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "start.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	if err = db.AutoMigrate(&store.Exchange{}, &store.Trader{}, &store.AIModel{}, &execution.Binding{}, &live.Lease{}, &live.Run{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&store.Exchange{ID: "managed", UserID: "alice", ExchangeType: "okx", AccountName: "managed", Name: "managed", APIKey: "key", SecretKey: "secret", Passphrase: "pass", Enabled: true},
		&store.AIModel{ID: "model", UserID: "alice", Provider: "deepseek", Enabled: true},
		&store.Trader{ID: "trader-1", UserID: "alice", Name: "legacy", AIModelID: "model", ExchangeID: "managed"},
		&execution.Binding{ID: "binding-1", UserID: "alice", ExchangeID: "managed", Venue: "okx", Environment: execution.EnvironmentLive, VenueAccountID: "uid", Enabled: true},
	} {
		if err = db.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	s := &Server{store: st, traderManager: manager.NewTraderManager(), planning: &planningServices{source: market.New()}}
	s.planningOnce.Do(func() {})
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", c.GetHeader("Test-User")) })
	router.POST("/traders/:id/start", s.handleStartTrader)

	r := httptest.NewRequest("POST", "/traders/trader-1/start", nil)
	r.Header.Set("Test-User", "alice")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unattended execution") {
		t.Fatalf("API start on a managed account = %d %s", w.Code, w.Body.String())
	}
	if _, err = s.traderManager.GetTrader("trader-1"); err == nil {
		t.Fatal("API start constructed a legacy trader for a managed account")
	}
}

// TestManagedOwnershipAllowsEmergencyClose pins the exception: the guard freezes
// entries, never the authenticated reduce that lowers exposure.
func TestManagedOwnershipAllowsEmergencyClose(t *testing.T) {
	s, f, in := closeFixture(t)
	if err := s.store.GormDB().AutoMigrate(&execution.Binding{}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.GormDB().Create(&execution.Binding{ID: "binding-1", UserID: "alice", ExchangeID: "second", Venue: "binance", Environment: execution.EnvironmentLive, VenueAccountID: "uid", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	// A managed run keyed by the binding is live on the same physical account.
	if err := s.store.GormDB().Create(&live.Run{ID: "run-1", UserID: "alice", ExchangeID: "second", AccountKey: "binding-1", State: "position_open"}).Error; err != nil {
		t.Fatal(err)
	}
	order, err := s.closeFuturesAccount(context.Background(), "alice", in, func(*store.Exchange) (accountCloser, error) { return f, nil })
	if err != nil || order.Status != "FILLED" || f.calls != 1 {
		t.Fatalf("managed account could not close: %+v, %v, calls %d", order, err, f.calls)
	}
}
