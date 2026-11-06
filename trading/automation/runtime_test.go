package automation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// managedTraderFixture is a venue that only reports what the test tells it to.
// Every call is counted so the tests can assert what the runtime did *not* do.
type managedTraderFixture struct {
	mu        sync.Mutex
	venue     string
	key       string
	env       execution.Environment
	snapshot  execution.ManagedAccount
	orders    map[string]execution.Order
	placed    []execution.Order
	cancelled []string
	snapErrs  int
	snapCalls int
	prepared  []string
	settings  map[string]int
	amendErr  error
}

func (f *managedTraderFixture) Venue() string { return f.venue }
func (f *managedTraderFixture) AccountSnapshot(context.Context, string) (execution.Account, error) {
	s, err := f.ManagedSnapshot(context.Background())
	if err != nil {
		return execution.Account{}, err
	}
	return execution.Account{Equity: s.Equity, Available: s.Available, FeeBPS: "5", Position: "0", OpenOrders: len(s.Orders)}, nil
}
func (f *managedTraderFixture) PrepareExecution(context.Context, string) error { return nil }
func (f *managedTraderFixture) SubmitOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placed = append(f.placed, o)
	o.OrderID = fmt.Sprint(len(f.placed))
	o.Status = "NEW"
	o.Filled = "0"
	f.orders[o.ClientID] = o
	return o, nil
}
func (f *managedTraderFixture) LookupOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if found, ok := f.orders[o.ClientID]; ok {
		return found, nil
	}
	return o, nil
}
func (f *managedTraderFixture) CancelExecutionOrder(_ context.Context, o execution.Order) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, o.ClientID)
	return nil
}
func (f *managedTraderFixture) RouteClientID(id string) (string, error) {
	return execution.CanonicalClientID(id)
}
func (f *managedTraderFixture) PrepareExecutionLeverage(_ context.Context, s string, l int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = append(f.prepared, fmt.Sprintf("%s/%d", s, l))
	return nil
}
func (f *managedTraderFixture) ManagedSnapshot(context.Context) (execution.ManagedAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapCalls++
	if f.snapErrs > 0 {
		f.snapErrs--
		return execution.ManagedAccount{}, errors.New("venue unavailable")
	}
	return f.snapshot, nil
}
func (f *managedTraderFixture) VerifyManagedSettings(_ context.Context, s string, l int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if want, ok := f.settings[s]; ok && want != l {
		return fmt.Errorf("venue reports leverage %d instead of %d", want, l)
	}
	return nil
}
func (f *managedTraderFixture) ManagedCapabilities() execution.Capabilities {
	return execution.Capabilities{}
}
func (f *managedTraderFixture) AmendStop(context.Context, execution.Order, execution.Decimal) (execution.Order, error) {
	return execution.Order{}, execution.ErrUnsupported
}
func (f *managedTraderFixture) Events(context.Context, func(execution.AccountEvent) error) error {
	return execution.ErrUnsupported
}

func (f *managedTraderFixture) placeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.placed)
}

func (f *managedTraderFixture) snapshotCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapCalls
}

// automationMarket serves the same perpetual book the controller tests use.
type automationMarket struct{ book core.Snapshot }

func (m *automationMarket) Snapshot(_ context.Context, _, _ string) (core.Snapshot, error) {
	return m.book, nil
}

// automationSource hands out one pre-built signal, or ErrNoSignal.
type automationSource struct {
	mu      sync.Mutex
	signals []Signal
}

func (s *automationSource) Next(context.Context, risk.Policy) (Signal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.signals) == 0 {
		return Signal{}, ErrNoSignal
	}
	next := s.signals[0]
	s.signals = s.signals[1:]
	return next, nil
}

type automationRig struct {
	db      *gorm.DB
	ctrl    *live.Controller
	risks   *risk.Store
	venue   *managedTraderFixture
	source  *automationSource
	book    core.Snapshot
	now     time.Time
	clock   *time.Time
	binding execution.Binding
	policy  risk.Policy
}

// rig builds one managed account end to end: a verified binding, an enabled
// policy, and a controller whose broker is that same venue.
func rig(t *testing.T, source IdeaSource) *automationRig {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "automation.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { sql.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	clock := now
	market := &automationMarket{book: core.Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual", Bid: "99999", Ask: "100000", BidSize: "100", AskSize: "100", Tick: "1", Step: "0.001", MinQty: "0.001", MinNotional: "10", Observed: now}}
	venue := &managedTraderFixture{venue: "binance", key: "account", env: execution.EnvironmentLive, orders: map[string]execution.Order{}, settings: map[string]int{}}
	venue.snapshot = execution.ManagedAccount{AccountKey: "account", Venue: "binance", Environment: execution.EnvironmentLive, PositionMode: "one-way", MarginMode: "isolated", Equity: "10000", Available: "10000", MarginUsed: "0", ObservedAt: now}

	ctrl, err := live.New(db, func(context.Context, string, string) (live.Broker, string, error) {
		return live.NOFXBroker{Trader: venue}, "account", nil
	}, market)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Now = func() time.Time { return clock }
	t.Cleanup(ctrl.Shutdown)

	risks, err := risk.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Risk = risks

	binding := execution.Binding{ID: "account", UserID: "alice", ExchangeID: "exchange", Venue: "binance", Environment: execution.EnvironmentLive, VenueAccountID: "uid-1", VerifiedAt: now, Enabled: true}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	policy := risk.DefaultPolicy("alice", "account", "portfolio", "binance", execution.EnvironmentLive)
	policy.Symbols = []string{"BTCUSDT"}
	policy.Enabled = true
	version, err := risk.PolicyVersion(policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = version
	stored, err := risks.PutPolicy(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	policy = stored
	// A live day start only completes inside DayStartGrace of 00:00 UTC, so the
	// fixture proves the baseline explicitly and then records a fresh one.
	dayStart := now.Truncate(24 * time.Hour).Add(time.Second)
	for _, at := range []time.Time{dayStart, now} {
		if err := risks.Observe(context.Background(), venue.snapshot, nil, at); err != nil {
			t.Fatal(err)
		}
	}
	return &automationRig{db: db, ctrl: ctrl, book: market.book, risks: risks, venue: venue, source: &automationSource{}, now: now, clock: &clock, binding: binding, policy: policy}
}

func (r *automationRig) runtime(t *testing.T, source IdeaSource) *Runtime {
	t.Helper()
	rt, err := NewRuntime(r.db, r.ctrl, r.risks, func(context.Context, string, string) (execution.ManagedTrader, execution.Binding, error) {
		return r.venue, r.binding, nil
	}, source, func() time.Time { return *r.clock })
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func (r *automationRig) advance(d time.Duration) { *r.clock = r.clock.Add(d) }

// waitPlaced waits for the controller's own run worker, which submits the entry
// from its goroutine rather than inside Execute.
func (r *automationRig) waitPlaced(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.venue.placeCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the run submitted %d orders, wanted %d", r.venue.placeCount(), want)
}

func (r *automationRig) idea() core.Idea {
	return core.Idea{Name: "signal", Side: "long", Entry: "100000", Stop: "99000", Target: "104000"}
}

func runStates(t *testing.T, db *gorm.DB) []live.Run {
	t.Helper()
	var runs []live.Run
	if err := db.Order("approved_at").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	return runs
}

// TestRuntimeStartsWithoutHTTP proves the supervisor needs no API handler: it
// ticks the accounts it discovers from verified bindings.
func TestRuntimeStartsWithoutHTTP(t *testing.T) {
	rig := rig(t, nil)
	rt := rig.runtime(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rig.venue.snapshotCalls() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rig.venue.snapshotCalls() == 0 {
		t.Fatal("the supervisor never read the managed account")
	}
	stop, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStop()
	if err := rt.Shutdown(stop); err != nil {
		t.Fatal(err)
	}
	// Shutting down the runtime must not stop the controller's own supervision.
	if _, err := rig.ctrl.Get("alice", "missing"); err == nil {
		t.Fatal("the shared controller stopped with the runtime")
	}
}

// TestRuntimePolicyPipeline drives one signal all the way to a submitted entry
// and proves no order exists before the authorization binding.
func TestRuntimePolicyPipeline(t *testing.T) {
	rig := rig(t, nil)
	signal := Signal{ID: "signal-1", Idea: rig.idea(), CreatedAt: rig.now, ExpiresAt: rig.now.Add(SignalExpiry)}
	rig.source.signals = []Signal{signal}
	rt := rig.runtime(t, rig.source)
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	runs := runStates(t, rig.db)
	if len(runs) != 1 {
		t.Fatalf("expected one authorized run, got %d", len(runs))
	}
	if runs[0].AuthorizationMode != "policy" || runs[0].SignalID != "signal-1" {
		t.Fatalf("run is not bound to the signal: %+v", runs[0])
	}
	if runs[0].PolicyVersion != rig.policy.Version {
		t.Fatalf("run is not bound to the policy version: %q", runs[0].PolicyVersion)
	}
	rig.waitPlaced(t, 1)
	if len(rig.venue.prepared) == 0 {
		t.Fatal("leverage was never prepared before the entry")
	}

	// The same signal must not open a second run on the next tick.
	rig.advance(SignalInterval)
	rig.source.signals = []Signal{signal}
	if err := rt.Tick(context.Background(), "alice", "account"); err == nil {
		t.Fatal("a duplicate signal was accepted")
	}
	if len(runStates(t, rig.db)) != 1 {
		t.Fatal("a duplicate signal created a second run")
	}
}

// TestRuntimePauseSurvivesRestart proves a breaker latch is not erased by a
// fresh runtime: the freeze comes from the database, not from memory.
func TestRuntimePauseSurvivesRestart(t *testing.T) {
	rig := rig(t, nil)
	// Equity halved at the venue: a drawdown the breaker must catch.
	rig.venue.snapshot.Equity = "5000"
	rig.venue.snapshot.Available = "5000"
	rt := rig.runtime(t, nil)
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if _, frozen := rt.frozenReason("account"); !frozen {
		t.Fatal("the breaker did not freeze new entries")
	}

	restarted := rig.runtime(t, nil)
	rig.source.signals = []Signal{{ID: "signal-after-restart", Idea: rig.idea(), CreatedAt: rig.now, ExpiresAt: rig.now.Add(SignalExpiry)}}
	restarted.source = rig.source
	if err := restarted.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if len(runStates(t, rig.db)) != 0 {
		t.Fatal("a restarted runtime ignored the persisted breaker")
	}
	if rig.venue.placeCount() != 0 {
		t.Fatal("a restarted runtime submitted an order while paused")
	}
}

// TestRecoveryNoEntryReplay proves recovery reconciles a live run and never
// resubmits its entry.
func TestRecoveryNoEntryReplay(t *testing.T) {
	rig := rig(t, nil)
	// A run that already holds a position, as if the process had crashed.
	limits, err := live.ManagedAccountLimits(rig.policy, rig.venue.snapshot, rig.now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := core.Compile(rig.book, limits, rig.idea(), rig.now)
	if err != nil {
		t.Fatal(err)
	}
	store := &planning.DraftStore{DB: rig.db, UserID: "alice", ExchangeID: "exchange", Venue: "binance"}
	if err := store.Add([]core.Plan{plan}); err != nil {
		t.Fatal(err)
	}
	view, err := rig.ctrl.AuthorizePolicy(context.Background(), "alice", plan.ID, plan.Hash, rig.policy.Version, "recovered-signal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.ctrl.Execute(context.Background(), "alice", view.ID, view.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	rig.waitPlaced(t, 1)
	placed := rig.venue.placeCount()

	rig.venue.snapshot.Positions = []execution.ManagedPosition{{Symbol: "BTCUSDT", Side: "long", Quantity: "0.1", Entry: "100000", Mark: "100000", StopPrice: "99000"}}
	rt := rig.runtime(t, nil)
	if err := rt.Recover(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if rig.venue.placeCount() != placed {
		t.Fatalf("recovery submitted %d new orders", rig.venue.placeCount()-placed)
	}
}

// TestRecoveryExternalPosition proves an unknown position freezes the account
// instead of being adopted as this runtime's own.
func TestRecoveryExternalPosition(t *testing.T) {
	rig := rig(t, nil)
	rig.venue.snapshot.Positions = []execution.ManagedPosition{{Symbol: "ETHUSDT", Side: "long", Quantity: "1.5", Entry: "4000", Mark: "4000"}}
	rt := rig.runtime(t, nil)
	if err := rt.Recover(context.Background(), "alice", "account"); err == nil {
		t.Fatal("recovery adopted a position no run owns")
	} else if want := "external position not owned by this runtime"; !contains(err.Error(), want) {
		t.Fatalf("unexpected reason: %v", err)
	}
	if _, frozen := rt.frozenReason("account"); !frozen {
		t.Fatal("an external position did not freeze new entries")
	}
	if rig.venue.placeCount() != 0 {
		t.Fatal("recovery placed an order")
	}
}

func contains(s, want string) bool { return strings.Contains(s, want) }
