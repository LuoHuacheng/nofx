package certification

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/adshao/go-binance/v2/futures"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/crypto"
	"nofx/store"
	"nofx/trader/binance"
	"nofx/trader/execution"
	"nofx/trader/okx"
)

// The sandbox acceptance entry point is a test, not a server route: it may
// place orders only when an operator supplies the isolated account explicitly.
// Flags are package-level so flag.Parse, which already ran before the tests,
// can see them. No live flag exists; live stays unreachable from here.
var (
	sandboxVenue   string
	sandboxBinding string
	sandboxDB      string
)

func init() {
	if flag.Lookup("sandbox-venue") == nil {
		flag.StringVar(&sandboxVenue, "sandbox-venue", "", "sandbox venue to certify: binance or okx")
	}
	if flag.Lookup("sandbox-binding") == nil {
		flag.StringVar(&sandboxBinding, "sandbox-binding", "", "verified sandbox account binding ID")
	}
	if flag.Lookup("sandbox-db") == nil {
		flag.StringVar(&sandboxDB, "sandbox-db", "", "path to a copied, isolated sandbox database")
	}
}

// sandboxGate is every check that must pass before the acceptance run may talk
// to a venue. It resolves the binding and the exchange config from the isolated
// database, builds the same production adapter the runtime builds, and refuses
// anything that could reach production.
type sandboxGate struct {
	Venue     string
	BindingID string
	DBPath    string

	// Trader is set only by the offline gate test: a local venue that counts
	// every POST, so a rejection can be proven to send nothing. The entry point
	// leaves it nil and builds the pinned production adapter.
	Trader execution.ManagedTrader
}

type sandboxSetup struct {
	db       *gorm.DB
	binding  execution.Binding
	exchange store.Exchange
	trader   execution.ManagedTrader
	account  execution.ManagedAccount
}

func (s *sandboxSetup) Close() {
	if s.db == nil {
		return
	}
	if sqlDB, err := s.db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// open resolves and validates one sandbox account. It never places an order:
// every rejection is returned before any write reaches a venue.
func (g sandboxGate) open(ctx context.Context) (*sandboxSetup, error) {
	venue := strings.ToLower(strings.TrimSpace(g.Venue))
	if venue != venueBinance && venue != venueOKX {
		return nil, fmt.Errorf("-sandbox-venue must be binance or okx, got %q", g.Venue)
	}
	path, err := isolatedSandboxDB(g.DBPath)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open isolated sandbox database %s: %w", path, err)
	}
	setup := &sandboxSetup{db: db}
	if err := db.WithContext(ctx).Where("id = ?", strings.TrimSpace(g.BindingID)).First(&setup.binding).Error; err != nil {
		setup.Close()
		return nil, fmt.Errorf("binding %q does not resolve in %s: %w", g.BindingID, path, err)
	}
	b := setup.binding
	if b.Environment != execution.EnvironmentSandbox {
		setup.Close()
		return nil, fmt.Errorf("binding %s is %q, not %q", b.ID, b.Environment, execution.EnvironmentSandbox)
	}
	if !b.Enabled || b.VerifiedAt.IsZero() {
		setup.Close()
		return nil, fmt.Errorf("binding %s is not enabled and verified; run the bind verification first", b.ID)
	}
	if b.Venue != venue {
		setup.Close()
		return nil, fmt.Errorf("binding %s is %s, not the requested venue %s", b.ID, b.Venue, venue)
	}
	// The runtime builds its adapter from the exchange configuration of the
	// binding's owner. A row of another owner, venue or environment is a
	// different physical account and must never be certified here.
	if err := db.WithContext(ctx).Where("id = ? AND user_id = ?", b.ExchangeID, b.UserID).First(&setup.exchange).Error; err != nil {
		setup.Close()
		return nil, fmt.Errorf("exchange config %s of user %s does not resolve: %w", b.ExchangeID, b.UserID, err)
	}
	ex := setup.exchange
	if !ex.Enabled {
		setup.Close()
		return nil, fmt.Errorf("exchange config %s is disabled", ex.ID)
	}
	if ex.ExchangeType != venue {
		setup.Close()
		return nil, fmt.Errorf("exchange config %s is %s, not %s", ex.ID, ex.ExchangeType, venue)
	}
	if !ex.Testnet {
		setup.Close()
		return nil, fmt.Errorf("exchange config %s is not testnet; sandbox acceptance refuses a live HTTP environment", ex.ID)
	}
	if ex.APIKey == "" || ex.SecretKey == "" || (venue == venueOKX && ex.Passphrase == "") {
		setup.Close()
		return nil, fmt.Errorf("exchange config %s has incomplete sandbox credentials", ex.ID)
	}

	trader := g.Trader
	if trader == nil {
		trader, err = sandboxTrader(ctx, venue, &ex)
		if err != nil {
			setup.Close()
			return nil, err
		}
		// The endpoint check is only meaningful for the adapter the runtime
		// builds; the offline seam deliberately points at a local venue.
		if err := requireSandboxEndpoints(venue, trader); err != nil {
			setup.Close()
			return nil, err
		}
	}
	if trader.Venue() != b.Venue || trader.Venue() != ex.ExchangeType {
		setup.Close()
		return nil, fmt.Errorf("adapter venue %q does not match binding %q and config %q", trader.Venue(), b.Venue, ex.ExchangeType)
	}
	account, err := trader.ManagedSnapshot(ctx)
	if err != nil {
		setup.Close()
		return nil, fmt.Errorf("read sandbox account %s: %w", b.ID, err)
	}
	if err := requireSandboxAccountMode(b.ID, account); err != nil {
		setup.Close()
		return nil, err
	}
	setup.trader, setup.account = trader, account
	return setup, nil
}

// isolatedSandboxDB rejects paths that address the runtime database. The check
// is lexical on purpose: a copy of the runtime database outside data/ cannot be
// detected, so the operator must copy it into an isolated directory.
func isolatedSandboxDB(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("-sandbox-db is required: point it at a copied, isolated sandbox database")
	}
	clean := filepath.Clean(path)
	if clean == "data" || strings.HasPrefix(clean, "data"+string(filepath.Separator)) {
		return "", fmt.Errorf("-sandbox-db %q is the production runtime database directory; copy it elsewhere first", path)
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("resolve -sandbox-db %q: %w", path, err)
	}
	if cwd, err := os.Getwd(); err == nil {
		prodDir := filepath.Join(cwd, "data")
		if rel, err := filepath.Rel(prodDir, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("-sandbox-db %s is under the runtime data directory %s", abs, prodDir)
		}
	}
	if filepath.Base(abs) == "astra_gateway.db" {
		return "", fmt.Errorf("-sandbox-db %s is the production gateway database; copy it elsewhere first", abs)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("-sandbox-db %s is not an existing database file: %w", abs, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("-sandbox-db %s is a directory, not a database file", abs)
	}
	return abs, nil
}

func sandboxTrader(ctx context.Context, venue string, ex *store.Exchange) (execution.ManagedTrader, error) {
	switch venue {
	case venueBinance:
		return binance.NewManagedAccountTrader(ctx, string(ex.APIKey), string(ex.SecretKey), ex.UserID, execution.EnvironmentSandbox)
	case venueOKX:
		return okx.NewManagedAccountTrader(ctx, string(ex.APIKey), string(ex.SecretKey), string(ex.Passphrase), execution.EnvironmentSandbox)
	}
	return nil, fmt.Errorf("unsupported sandbox venue %q", venue)
}

// requireSandboxEndpoints re-reads the endpoints the adapter actually pinned.
// The environment name alone is not evidence: a sandbox trader that kept a live
// host would report sandbox and still sign against production.
func requireSandboxEndpoints(venue string, trader execution.ManagedTrader) error {
	var (
		baseNames  []string
		envField   string
		wantBase   string
		wantStream string
	)
	switch venue {
	case venueBinance:
		baseNames = []string{"client", "BaseURL"}
		envField = "managedEnvironment"
		wantBase = futures.BaseApiTestnetUrl
		wantStream = "wss://stream.binancefuture.com/ws/"
	case venueOKX:
		baseNames = []string{"baseURL"}
		envField = "environment"
		// OKX demo trading shares the live HTTP host and is selected only by the
		// x-simulated-trading header, so the demo stream is the endpoint proof.
		wantBase = "https://www.okx.com"
		wantStream = "wss://wspap.okx.com:8443/ws/v5/private?brokerId=9999"
	default:
		return fmt.Errorf("unsupported sandbox venue %q", venue)
	}
	// These endpoints are unexported in both adapters; adding accessors would be
	// a production change, so the gate reads the pinned values directly.
	base, err := managedEndpointField(trader, baseNames...)
	if err != nil {
		return err
	}
	env, err := managedEndpointField(trader, envField)
	if err != nil {
		return err
	}
	stream, err := managedEndpointField(trader, "managedStreamURL")
	if err != nil {
		return err
	}
	if env != string(execution.EnvironmentSandbox) {
		return fmt.Errorf("managed %s adapter is pinned to environment %q, not sandbox", venue, env)
	}
	if base != wantBase {
		return fmt.Errorf("managed %s HTTP endpoint is %q, want the sandbox endpoint %q", venue, base, wantBase)
	}
	if stream != wantStream {
		return fmt.Errorf("managed %s stream endpoint is %q, want the sandbox endpoint %q", venue, stream, wantStream)
	}
	return nil
}

func managedEndpointField(value any, names ...string) (string, error) {
	rv := reflect.ValueOf(value)
	for _, name := range names {
		for rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return "", fmt.Errorf("managed endpoint %s is nil", name)
			}
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Struct {
			return "", fmt.Errorf("managed endpoint %s is not a struct field", name)
		}
		rv = rv.FieldByName(name)
		if !rv.IsValid() {
			return "", fmt.Errorf("managed adapter has no %s field", name)
		}
	}
	if rv.Kind() != reflect.String {
		return "", fmt.Errorf("managed endpoint %s is not a string", names[len(names)-1])
	}
	return rv.String(), nil
}

// requireSandboxAccountMode refuses any account that could open the wrong kind
// of position: hedge mode needs position sides the plan never carries, and a
// reported cross-margin mode invalidates the planned margin.
func requireSandboxAccountMode(bindingID string, account execution.ManagedAccount) error {
	if account.Environment != execution.EnvironmentSandbox {
		return fmt.Errorf("account %s reported environment %q, not sandbox", bindingID, account.Environment)
	}
	mode := strings.ReplaceAll(strings.ToLower(account.PositionMode), "-", "_")
	if mode != "one_way" && mode != "net_mode" {
		return fmt.Errorf("account %s is in position mode %q; unattended execution requires one-way (net) mode", bindingID, account.PositionMode)
	}
	if account.MarginMode != "" && !strings.EqualFold(account.MarginMode, "isolated") {
		return fmt.Errorf("account %s is in margin mode %q; unattended execution requires isolated margin", bindingID, account.MarginMode)
	}
	return nil
}

// TestSandboxGateRejectsLive proves every unsafe account is refused before a
// single order reaches the venue. The local venue counts POSTs, so a gate that
// validated too late would be caught here.
func TestSandboxGateRejectsLive(t *testing.T) {
	ctx := context.Background()
	for _, venue := range []string{venueBinance, venueOKX} {
		venue := venue
		t.Run(venue, func(t *testing.T) {
			run := func(name string, wantErr bool, mutate func(*testing.T, *exchangeFixture), path func(*exchangeFixture) string, trader func(*exchangeFixture) execution.ManagedTrader) {
				t.Helper()
				f := sandboxGateAccount(t, venue)
				if mutate != nil {
					mutate(t, f)
				}
				gate := sandboxGate{Venue: venue, BindingID: f.binding.ID, DBPath: f.dbPath}
				if path != nil {
					gate.DBPath = path(f)
				}
				if trader != nil {
					gate.Trader = trader(f)
				}
				setup, err := gate.open(ctx)
				if setup != nil {
					setup.Close()
				}
				entry, protect, exit, cancels := f.fake.countsNow()
				unsafePOSTs := entry + protect + exit + cancels
				if wantErr {
					if err == nil || unsafePOSTs != 0 {
						t.Fatal("sandbox gate accepted unsafe account")
					}
					t.Logf("%s rejected: %v", name, err)
					return
				}
				if err != nil {
					t.Fatalf("%s: sandbox gate rejected a sandbox account: %v", name, err)
				}
				if unsafePOSTs != 0 {
					t.Fatalf("%s: gate sent %d venue requests", name, unsafePOSTs)
				}
			}

			run("pinned sandbox account", false, nil, nil, func(f *exchangeFixture) execution.ManagedTrader {
				if venue == venueBinance {
					return f.venue2
				}
				// The OKX simulator omits the imr field the adapter requires from
				// its balance payload, so the control case supplies the account the
				// venue would return. Every rejection case runs on the adapter.
				return fixtureAccountTrader{ManagedTrader: f.venue2, account: execution.ManagedAccount{
					Venue: venue, Environment: execution.EnvironmentSandbox, PositionMode: "one_way", MarginMode: "isolated",
				}}
			})
			run("live binding", true, func(t *testing.T, f *exchangeFixture) {
				if err := f.db.Model(&execution.Binding{}).Where("id = ?", f.binding.ID).Update("environment", execution.EnvironmentLive).Error; err != nil {
					t.Fatalf("move binding to live: %v", err)
				}
			}, nil, nil)
			run("live HTTP environment", true, func(t *testing.T, f *exchangeFixture) {
				if err := f.db.Model(&store.Exchange{}).Where("id = ?", f.binding.ExchangeID).Update("testnet", false).Error; err != nil {
					t.Fatalf("flip exchange config to live: %v", err)
				}
			}, nil, nil)
			run("runtime database", true, nil, func(*exchangeFixture) string {
				return filepath.Join("data", "data.db")
			}, nil)
			run("empty database path", true, nil, func(*exchangeFixture) string { return "" }, nil)
			run("owner mismatch", true, func(t *testing.T, f *exchangeFixture) {
				if err := f.db.Model(&store.Exchange{}).Where("id = ?", f.binding.ExchangeID).Update("user_id", "mallory").Error; err != nil {
					t.Fatalf("change exchange owner: %v", err)
				}
			}, nil, nil)
			run("hedge mode", true, nil, nil, func(f *exchangeFixture) execution.ManagedTrader {
				return snapshotTrader{ManagedTrader: f.venue2, mode: "hedge", margin: "isolated"}
			})
			run("cross margin", true, nil, nil, func(f *exchangeFixture) execution.ManagedTrader {
				return snapshotTrader{ManagedTrader: f.venue2, mode: "one_way", margin: "cross"}
			})
		})
	}
}

// snapshotTrader overrides only the account read, so a gate rejection can be
// proven offline for settings the local venue fixture does not simulate.
type snapshotTrader struct {
	execution.ManagedTrader
	mode   string
	margin string
}

func (s snapshotTrader) ManagedSnapshot(ctx context.Context) (execution.ManagedAccount, error) {
	account, err := s.ManagedTrader.ManagedSnapshot(ctx)
	if err != nil {
		return account, err
	}
	if s.mode != "" {
		account.PositionMode = s.mode
	}
	if s.margin != "" {
		account.MarginMode = s.margin
	}
	return account, nil
}

// fixtureAccountTrader replaces the account read for the control case only.
type fixtureAccountTrader struct {
	execution.ManagedTrader
	account execution.ManagedAccount
}

func (f fixtureAccountTrader) ManagedSnapshot(context.Context) (execution.ManagedAccount, error) {
	return f.account, nil
}

func sandboxGateAccount(t *testing.T, venue string) *exchangeFixture {
	t.Helper()
	f := newExchangeFixture(t, venue)
	if err := f.db.AutoMigrate(&store.Exchange{}); err != nil {
		t.Fatalf("migrate exchanges: %v", err)
	}
	ex := store.Exchange{
		ID: f.binding.ExchangeID, UserID: f.binding.UserID,
		ExchangeType: venue, AccountName: "sandbox", Name: "sandbox", Type: "cex",
		Enabled: true, Testnet: true,
		APIKey: "sandbox-key", SecretKey: "sandbox-secret", Passphrase: "sandbox-pass",
	}
	if err := f.db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exchange config: %v", err)
	}
	return f
}

// TestSandboxLifecycleAssertions drives the full lifecycle offline: plan, fill,
// confirmed SL/TP, a partial reduce at the venue, then a flat account with no
// leftover orders and a released reservation. It is the contract the sandbox
// operator must observe on a real account.
func TestSandboxLifecycleAssertions(t *testing.T) {
	for _, venue := range []string{venueBinance, venueOKX} {
		venue := venue
		t.Run(venue, func(t *testing.T) {
			f := newExchangeFixture(t, venue)
			f.Run()
			if f.executeErr != nil {
				t.Fatalf("%s execute the approved plan: %v", venue, f.executeErr)
			}
			f.Steps(3)
			f.AssertOneEntry(t, "signal-1")

			filled := f.fake.positionNow()
			if filled.Sign() <= 0 {
				t.Fatalf("%s filled position is %s, want a long position", venue, dec(filled))
			}
			for _, role := range []string{"sl", "tp"} {
				orders := f.fake.byRole(role)
				if len(orders) != 1 || !orders[0].live() {
					t.Fatalf("%s venue holds %d %s orders, want 1 confirmed", venue, len(orders), role)
				}
			}
			if state := f.run().ProtectionState; state != "protected" {
				t.Fatalf("%s protection state is %q after confirmation, want protected", venue, state)
			}

			// The operator reduces the position by half at the venue. The
			// runtime must notice the plan no longer matches the account and
			// stop treating the position as protected.
			f.fake.mu.Lock()
			entry := new(big.Rat).Set(f.fake.entry)
			f.fake.mu.Unlock()
			f.fake.setPosition(new(big.Rat).Quo(filled, big.NewRat(2, 1)), entry)
			f.Steps(2)
			if state := f.run().ProtectionState; state == "protected" {
				t.Fatalf("%s still reports protected after a partial reduce", venue)
			}

			if err := f.Flatten(); err != nil {
				t.Fatalf("%s flatten the remaining position: %v", venue, err)
			}
			finalPosition := dec(f.fake.positionNow())
			remainingOrders := len(f.fake.liveNow())
			if !execution.Equal(finalPosition, "0") || remainingOrders != 0 {
				t.Fatal("sandbox lifecycle not settled")
			}
			if state := f.ReservationState(); state != "released" {
				t.Fatalf("%s risk reservation is %q after a flat account, want released", venue, state)
			}
		})
	}
}

// TestSandboxManagedLifecycle is the sandbox acceptance entry point. It never
// runs without the three flags and the same encryption keys the runtime uses:
// without them the encrypted credentials in the isolated database are
// unreadable. It proves the account is safe to certify; the order cycle itself
// is driven by the runtime and recorded in the acceptance template.
func TestSandboxManagedLifecycle(t *testing.T) {
	if sandboxVenue == "" || sandboxBinding == "" || sandboxDB == "" {
		t.Skip("sandbox acceptance needs -sandbox-venue, -sandbox-binding and -sandbox-db for an authorized isolated sandbox account; no venue was contacted")
	}
	ctx := context.Background()
	service, err := crypto.NewCryptoService()
	if err != nil {
		t.Fatalf("sandbox credentials are encrypted: export %s and %s from the runtime secret store: %v", crypto.EnvRSAPrivateKey, crypto.EnvDataEncryptionKey, err)
	}
	crypto.SetGlobalCryptoService(service)
	defer crypto.SetGlobalCryptoService(nil)

	gate := sandboxGate{Venue: sandboxVenue, BindingID: sandboxBinding, DBPath: sandboxDB}
	setup, err := gate.open(ctx)
	if err != nil {
		t.Fatalf("sandbox gate: %v", err)
	}
	defer setup.Close()
	account := setup.account
	if len(account.Positions) != 0 || len(account.Orders) != 0 {
		t.Fatalf("sandbox account %s is not empty: %d positions, %d orders; acceptance needs a flat, order-free account",
			setup.binding.ID, len(account.Positions), len(account.Orders))
	}
	t.Logf("sandbox account %s verified: venue=%s environment=%s binding=%s equity=%s available=%s position_mode=%s margin_mode=%s observed=%s",
		setup.binding.ID, setup.binding.Venue, setup.binding.Environment, setup.exchange.ID,
		account.Equity, account.Available, account.PositionMode, account.MarginMode, account.ObservedAt.Format("2006-01-02T15:04:05Z07:00"))
}
