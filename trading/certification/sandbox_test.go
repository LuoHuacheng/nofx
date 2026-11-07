package certification

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/crypto"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/planning/market"
	"nofx/store"
	"nofx/trader/binance"
	"nofx/trader/execution"
	"nofx/trader/okx"
	"nofx/trading/risk"
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
	// A name already taken by another package would leave these variables empty
	// and turn every run into the same silent skip as an operator who forgot the
	// flags, so the collision is fatal instead.
	for _, name := range []string{"sandbox-venue", "sandbox-binding", "sandbox-db"} {
		if flag.Lookup(name) != nil {
			panic("certification: flag -" + name + " is already registered; the sandbox entry point cannot read it")
		}
	}
	flag.StringVar(&sandboxVenue, "sandbox-venue", "", "sandbox venue to certify: binance or okx")
	flag.StringVar(&sandboxBinding, "sandbox-binding", "", "verified sandbox account binding ID")
	flag.StringVar(&sandboxDB, "sandbox-db", "", "path to a copied, isolated sandbox database")
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

// sandboxRepoRoot locates the repository from this source file, so the guard
// names the runtime database independently of the cwd go test chose (the
// package directory, where "data/data.db" means nothing).
func sandboxRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate the certification test source")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the certification test source")
		}
		dir = parent
	}
}

// isolatedSandboxDB rejects every path that resolves to the runtime database.
// The candidate is made absolute and, when it exists, symlink-resolved before
// the comparison, because a relative or symlinked path into <repo>/data would
// otherwise open the production file for writing. A copy outside data/ is still
// undetectable by content, so the operator must copy it into an isolated
// directory.
func isolatedSandboxDB(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("-sandbox-db is required: point it at a copied, isolated sandbox database")
	}
	clean := filepath.Clean(path)
	if clean == "data" || strings.HasPrefix(clean, "data"+string(filepath.Separator)) {
		return "", fmt.Errorf("-sandbox-db %q is the runtime data directory; copy it elsewhere first", path)
	}
	repo, err := sandboxRepoRoot()
	if err != nil {
		return "", err
	}
	runtimeDir := filepath.Join(repo, "data")
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("resolve -sandbox-db %q: %w", path, err)
	}
	resolved := abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = real
	}
	for _, candidate := range []string{abs, resolved} {
		if rel, err := filepath.Rel(runtimeDir, candidate); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("-sandbox-db %s is inside the runtime data directory %s; copy it to an isolated directory first", candidate, runtimeDir)
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("-sandbox-db %s is not an existing database file: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("-sandbox-db %s is not a regular database file", resolved)
	}
	probe, err := os.OpenFile(resolved, os.O_WRONLY, 0)
	if err != nil {
		return "", fmt.Errorf("-sandbox-db %s is not writable: %w", resolved, err)
	}
	probe.Close()
	if filepath.Base(resolved) == "astra_gateway.db" {
		return "", fmt.Errorf("-sandbox-db %s is the production gateway database; copy it elsewhere first", resolved)
	}
	return resolved, nil
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
			repo, err := sandboxRepoRoot()
			if err != nil {
				t.Fatalf("locate the repository: %v", err)
			}
			runtimeDB := filepath.Join(repo, "data", "data.db")
			run := func(name string, wantErr bool, wantReason string, mutate func(*testing.T, *exchangeFixture), path func(*exchangeFixture) string, trader func(*exchangeFixture) execution.ManagedTrader) {
				t.Helper()
				f := sandboxGateAccount(t, venue)
				if mutate != nil {
					mutate(t, f)
				}
				gate := sandboxGate{Venue: venue, BindingID: f.binding.ID, DBPath: f.dbPath, Trader: f.venue2}
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
					if wantReason != "" && !strings.Contains(err.Error(), wantReason) {
						t.Fatalf("%s rejected for the wrong reason, want %q: %v", name, wantReason, err)
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

			run("pinned sandbox account", false, "", nil, nil, func(f *exchangeFixture) execution.ManagedTrader {
				// Both venues run the real adapter: the local simulator now reports
				// the fields the gate reads, so no fixture may stand in for it.
				return f.venue2
			})
			run("live binding", true, "sandbox", func(t *testing.T, f *exchangeFixture) {
				if err := f.db.Model(&execution.Binding{}).Where("id = ?", f.binding.ID).Update("environment", execution.EnvironmentLive).Error; err != nil {
					t.Fatalf("move binding to live: %v", err)
				}
			}, nil, nil)
			run("live HTTP environment", true, "", func(t *testing.T, f *exchangeFixture) {
				if err := f.db.Model(&store.Exchange{}).Where("id = ?", f.binding.ExchangeID).Update("testnet", false).Error; err != nil {
					t.Fatalf("flip exchange config to live: %v", err)
				}
			}, nil, nil)
			run("runtime database", true, "runtime data directory", nil, func(*exchangeFixture) string {
				return runtimeDB
			}, nil)
			run("empty database path", true, "", nil, func(*exchangeFixture) string { return "" }, nil)
			run("owner mismatch", true, "", func(t *testing.T, f *exchangeFixture) {
				if err := f.db.Model(&store.Exchange{}).Where("id = ?", f.binding.ExchangeID).Update("user_id", "mallory").Error; err != nil {
					t.Fatalf("change exchange owner: %v", err)
				}
			}, nil, nil)
			run("hedge mode", true, "position mode", func(t *testing.T, f *exchangeFixture) {
				f.fake.setPositionMode("hedge")
			}, nil, nil)
			run("cross margin", true, "margin mode", func(t *testing.T, f *exchangeFixture) {
				// Both adapters report a margin mode only for an open position.
				f.fake.setMarginMode("cross")
				f.fake.setPosition(rat("0.01"), rat("100000"))
			}, nil, nil)
		})
	}
}

// roundTripFunc answers adapter construction calls without a network: the
// Binance adapter calibrates its clock through the injected transport.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSandboxRejectsRuntimeDatabase proves the guard rejects every spelling of
// the runtime database, not only the relative one. go test runs with the
// package directory as cwd, so the lexical check alone missed the real file.
func TestSandboxRejectsRuntimeDatabase(t *testing.T) {
	repo, err := sandboxRepoRoot()
	if err != nil {
		t.Fatalf("locate the repository: %v", err)
	}
	runtimeDB := filepath.Join(repo, "data", "data.db")
	cases := []struct{ name, path string }{
		{"absolute", runtimeDB},
		{"traversal", filepath.Join("..", "..", "data", "data.db")},
	}
	if _, err := os.Stat(runtimeDB); err == nil {
		link := filepath.Join(t.TempDir(), "runtime-copy.db")
		if err := os.Symlink(runtimeDB, link); err != nil {
			t.Fatalf("symlink the runtime database: %v", err)
		}
		cases = append(cases, struct{ name, path string }{"symlink", link})
	} else {
		t.Logf("runtime database %s is absent; the symlink case cannot be built", runtimeDB)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resolved, err := isolatedSandboxDB(c.path)
			if err == nil {
				t.Fatalf("-sandbox-db %s was accepted as %s", c.path, resolved)
			}
			if !strings.Contains(err.Error(), "runtime data directory") {
				t.Fatalf("-sandbox-db %s rejected for an unrelated reason: %v", c.path, err)
			}
		})
	}
}

// TestSandboxRequireEndpointsRejectsLive exercises the endpoint check directly:
// it is the only defense against an adapter that reports sandbox but kept a
// live host. Adapter construction stays offline because the Binance clock
// calibration is answered by the injected transport.
func TestSandboxRequireEndpointsRejectsLive(t *testing.T) {
	ctx := context.Background()
	offline := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"serverTime":1700000000000}`)),
		}, nil
	})}
	production, err := binance.NewManagedAccountTraderWithEndpoint(ctx, "key", "secret", "alice", execution.EnvironmentSandbox,
		&binance.ManagedEndpoint{BaseURL: futures.BaseApiMainUrl, StreamURL: "wss://stream.binancefuture.com/ws/", Client: offline})
	if err != nil {
		t.Fatalf("build the binance adapter: %v", err)
	}
	if err := requireSandboxEndpoints(venueBinance, production); err == nil || !strings.Contains(err.Error(), "HTTP endpoint") {
		t.Fatalf("binance live HTTP endpoint accepted: %v", err)
	}
	pinned, err := binance.NewManagedAccountTraderWithEndpoint(ctx, "key", "secret", "alice", execution.EnvironmentSandbox,
		&binance.ManagedEndpoint{BaseURL: futures.BaseApiTestnetUrl, StreamURL: "wss://stream.binancefuture.com/ws/", Client: offline})
	if err != nil {
		t.Fatalf("build the binance sandbox adapter: %v", err)
	}
	if err := requireSandboxEndpoints(venueBinance, pinned); err != nil {
		t.Fatalf("binance sandbox endpoint rejected: %v", err)
	}
	simulated, err := okx.NewManagedAccountTraderWithEndpoint(ctx, "key", "secret", "passphrase", execution.EnvironmentSandbox, nil)
	if err != nil {
		t.Fatalf("build the okx sandbox adapter: %v", err)
	}
	if err := requireSandboxEndpoints(venueOKX, simulated); err != nil {
		t.Fatalf("okx sandbox endpoint rejected: %v", err)
	}
	productionOKX, err := okx.NewManagedAccountTraderWithEndpoint(ctx, "key", "secret", "passphrase", execution.EnvironmentLive, nil)
	if err != nil {
		t.Fatalf("build the okx live adapter: %v", err)
	}
	if err := requireSandboxEndpoints(venueOKX, productionOKX); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("okx live environment accepted: %v", err)
	}
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
			for _, role := range []string{"sl", "tp"} {
				orders := f.fake.byRole(role)
				if len(orders) != 1 || !orders[0].live() {
					t.Fatalf("%s venue holds %d %s orders after the partial reduce, want the confirmed protection untouched", venue, len(orders), role)
				}
			}
			if state := f.ReservationState(); state != risk.StateReserved && state != risk.StateExposed {
				t.Fatalf("%s risk reservation is %q while the position is still open, want it held", venue, state)
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
// unreadable. With them it drives the production runtime — automation policy,
// compiled plan, approval, execution and supervision — against the authorized
// sandbox account, reads the venue back after every phase and flattens before
// failing, so a broken run never leaves the account exposed.
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
	user, venue := setup.binding.UserID, setup.binding.Venue
	symbol := binanceSymbol
	if venue == venueOKX {
		symbol = okxSymbol
	}
	broker := live.NOFXBroker{Trader: setup.trader}
	acct, err := broker.Account(ctx, symbol)
	if err != nil {
		t.Fatalf("read the sandbox account: %v", err)
	}
	book, err := market.New().Snapshot(ctx, venue, symbol)
	if err != nil {
		t.Fatalf("read the %s %s book: %v", venue, symbol, err)
	}
	equity, ok := new(big.Rat).SetString(string(acct.Equity))
	if !ok || equity.Sign() <= 0 {
		t.Fatalf("sandbox account equity %q is unusable", acct.Equity)
	}
	available := acct.Available
	if avail, ok := new(big.Rat).SetString(string(acct.Available)); ok && avail.Cmp(equity) > 0 {
		available = acct.Equity
	}
	fee := acct.FeeBPS
	if !core.Positive(fee) {
		fee = "5"
	}
	// 0.5% of equity is risk.DefaultPolicy's default per-trade budget.
	const leverage, stopBPS, targetBPS = 3, 100, 300
	entry := book.Ask
	constraints := core.Constraints{
		Leverage:          leverage,
		AvailableMargin:   available,
		Equity:            acct.Equity,
		MaxRisk:           core.Decimal(new(big.Rat).Mul(equity, big.NewRat(50, 10000)).FloatString(8)),
		MaxNotional:       core.Decimal(new(big.Rat).Mul(equity, big.NewRat(leverage, 1)).FloatString(8)),
		MinRewardRisk:     "1.5",
		FeeBPS:            fee,
		SlippageBPS:       "10",
		FundingReserveBPS: "10",
	}
	plan, err := core.Compile(book, constraints, core.Idea{
		Name:   "sandbox-certification-" + venue,
		Side:   "long",
		Entry:  entry,
		Stop:   core.Price(entry, 1-float64(stopBPS)/10000, book.Tick),
		Target: core.Price(entry, 1+float64(targetBPS)/10000, book.Tick),
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("compile the certification plan: %v", err)
	}

	risks, err := risk.NewStore(setup.db)
	if err != nil {
		t.Fatalf("open the risk store: %v", err)
	}
	policy := risk.DefaultPolicy(user, setup.binding.ID, "sandbox-certification", venue, execution.EnvironmentSandbox)
	policy.Symbols = []string{symbol}
	policy.Enabled = true
	if policy, err = risks.PutPolicy(ctx, policy); err != nil {
		t.Fatalf("store the sandbox certification policy: %v", err)
	}
	observed := setup.account
	observed.AccountKey = setup.binding.ID
	midnight := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Second)
	for _, at := range []time.Time{midnight, time.Now().UTC()} {
		if err := risks.Observe(ctx, observed, nil, at); err != nil {
			t.Fatalf("record the sandbox equity baseline: %v", err)
		}
	}
	drafts := planning.DraftStore{DB: setup.db, UserID: user, ExchangeID: setup.binding.ExchangeID, Venue: venue}
	if err := drafts.Add([]core.Plan{plan}); err != nil {
		t.Fatalf("store the draft plan: %v", err)
	}
	ctrl, err := live.New(setup.db, func(context.Context, string, string) (live.Broker, string, error) {
		return broker, setup.binding.ID, nil
	}, market.New())
	if err != nil {
		t.Fatalf("start the runtime: %v", err)
	}
	ctrl.Interval = -1 // the test drives every tick explicitly
	ctrl.Risk = risks
	defer ctrl.Shutdown()

	view, err := ctrl.Approve(ctx, user, plan.ID, plan.Hash, "APPROVE LIVE")
	if err != nil {
		t.Fatalf("approve the certification plan: %v", err)
	}
	run, err := ctrl.Execute(ctx, user, view.ID, view.ApprovalHash)
	if err != nil {
		t.Fatalf("execute the approved plan: %v", err)
	}
	t.Logf("%s run %s started: state=%s", venue, run.ID, run.State)

	current := func() live.View {
		t.Helper()
		v, err := ctrl.Get(user, run.ID)
		if err != nil {
			t.Fatalf("read run %s: %v", run.ID, err)
		}
		return v
	}
	terminal := func(v live.View) bool {
		switch v.State {
		case "needs_attention", "completed", "cancelled", "failed", "rejected":
			return true
		}
		return false
	}
	drive := func(until func(live.View) bool) live.View {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for {
			ctrl.StepContext(ctx, user, run.ID)
			v := current()
			if until(v) || time.Now().After(deadline) {
				return v
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	// venueState reads the account back from the venue, never from the runtime.
	venueState := func() (*big.Rat, int) {
		t.Helper()
		snapshot, err := setup.trader.ManagedSnapshot(ctx)
		if err != nil {
			t.Fatalf("read the sandbox venue: %v", err)
		}
		position := new(big.Rat)
		for _, p := range snapshot.Positions {
			if p.Symbol != symbol {
				continue
			}
			q, ok := new(big.Rat).SetString(string(p.Quantity))
			if !ok {
				t.Fatalf("venue position quantity %q is unusable", p.Quantity)
			}
			if strings.EqualFold(p.Side, "short") {
				t.Fatalf("venue reports a short %s position during a long acceptance run", symbol)
			}
			position.Add(position, q)
		}
		return position, len(snapshot.Orders)
	}

	v := drive(func(v live.View) bool { return v.ProtectionState == "protected" || terminal(v) })
	if v.ProtectionState != "protected" {
		_, _ = ctrl.Flatten(ctx, user, run.ID, "sandbox acceptance aborted before protection")
		t.Fatalf("sandbox entry was not protected: state=%s protection=%s error=%s", v.State, v.ProtectionState, v.Error)
	}
	position, orders := venueState()
	if position.Sign() <= 0 || orders != 2 {
		_, _ = ctrl.Flatten(ctx, user, run.ID, "sandbox acceptance aborted on an unexpected venue state")
		t.Fatalf("venue shows position %s and %d orders after protection, want a long position and 2 protective orders", position.RatString(), orders)
	}
	t.Logf("%s entry filled and protected: position=%s orders=%d", venue, position.RatString(), orders)

	step, ok := new(big.Rat).SetString(string(book.Step))
	if !ok || step.Sign() <= 0 {
		t.Fatalf("venue quantity step %q is unusable", book.Step)
	}
	half := new(big.Rat).Quo(position, big.NewRat(2, 1))
	units := new(big.Rat).Quo(half, step)
	quantity := new(big.Rat).SetInt(new(big.Int).Quo(units.Num(), units.Denom()))
	quantity.Mul(quantity, step)
	clientID, err := execution.NewClientID()
	if err != nil {
		t.Fatalf("mint a reduce client order ID: %v", err)
	}
	route, err := broker.RouteClientID(clientID)
	if err != nil {
		t.Fatalf("route the reduce client order ID: %v", err)
	}
	if _, err := broker.Place(ctx, execution.Order{
		ClientID: clientID, VenueClientID: route, Symbol: symbol, Side: "sell", Type: "market",
		Quantity: execution.Decimal(quantity.FloatString(12)), Price: entry,
		ReduceOnly: true, Role: "exit", Status: "submitting",
	}); err != nil {
		t.Fatalf("reduce half the position at the venue: %v", err)
	}
	v = drive(func(v live.View) bool { return v.ProtectionState != "protected" || terminal(v) })
	if v.ProtectionState == "protected" {
		t.Fatalf("runtime still reports protected after a partial reduce: state=%s", v.State)
	}
	position, orders = venueState()
	t.Logf("%s after the partial reduce: position=%s orders=%d state=%s protection=%s", venue, position.RatString(), orders, v.State, v.ProtectionState)

	if _, err := ctrl.Flatten(ctx, user, run.ID, "sandbox certification"); err != nil {
		t.Fatalf("flatten the sandbox position: %v", err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		position, orders = venueState()
		if (position.Sign() == 0 && orders == 0) || time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if position.Sign() != 0 || orders != 0 {
		t.Fatalf("sandbox account is not settled: position=%s orders=%d", position.RatString(), orders)
	}
	record, err := risks.Reservation(ctx, run.ID)
	if err != nil {
		t.Fatalf("read the risk reservation of run %s: %v", run.ID, err)
	}
	if record.State != risk.StateReleased {
		t.Fatalf("risk reservation is %q on a flat account, want %q", record.State, risk.StateReleased)
	}
	t.Logf("sandbox acceptance complete for %s binding %s: entry, protection, partial reduce, flatten, no venue orders, reservation released",
		venue, setup.binding.ID)
}
