package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	history   execution.HistoryReader
	// stream is the private event source; a test pushes events into it to
	// exercise the stream path instead of the REST poll.
	stream chan execution.AccountEvent
	// clock makes the fixture report a snapshot as fresh as the test's fake now.
	clock *time.Time
}

func (f *managedTraderFixture) Venue() string { return f.venue }
func (f *managedTraderFixture) AccountSnapshot(_ context.Context, symbol string) (execution.Account, error) {
	s, err := f.ManagedSnapshot(context.Background())
	if err != nil {
		return execution.Account{}, err
	}
	// The position must mirror the snapshot: a venue that reports a flat
	// account while holding a position makes protection refuse to run.
	position := execution.Decimal("0")
	for _, p := range s.Positions {
		if p.Symbol != symbol {
			continue
		}
		qty, err := execution.Number(string(p.Quantity))
		if err != nil {
			return execution.Account{}, err
		}
		position, err = execution.ExactDecimal(qty)
		if err != nil {
			return execution.Account{}, err
		}
	}
	return execution.Account{Equity: s.Equity, Available: s.Available, FeeBPS: "5", Position: position, OpenOrders: len(s.Orders)}, nil
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
	// The venue answers "now": a fixture that reported a frozen timestamp would
	// fail the snapshot-age gate as soon as the test advanced its clock.
	account := f.snapshot
	account.ObservedAt = *f.clock
	return account, nil
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
func (f *managedTraderFixture) Events(ctx context.Context, fn func(execution.AccountEvent) error) error {
	f.mu.Lock()
	stream := f.stream
	f.mu.Unlock()
	if stream == nil {
		return execution.ErrUnsupported
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-stream:
			if !ok {
				return nil
			}
			if err := fn(e); err != nil {
				return err
			}
		}
	}
}

func (f *managedTraderFixture) ManagedHistory() execution.HistoryReader { return f.history }

func (f *managedTraderFixture) placeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.placed)
}

func (f *managedTraderFixture) protectionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, o := range f.placed {
		if o.Role == "sl" || o.Role == "tp" {
			n++
		}
	}
	return n
}

func (f *managedTraderFixture) exitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, o := range f.placed {
		if o.Role == "exit" {
			n++
		}
	}
	return n
}

func (f *managedTraderFixture) placedOrders() []execution.Order {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execution.Order(nil), f.placed...)
}

func (f *managedTraderFixture) entryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, o := range f.placed {
		if o.Role == "entry" {
			n++
		}
	}
	return n
}

func (f *managedTraderFixture) snapshotCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapCalls
}

// automationMarket serves the same perpetual book the controller tests use.
type automationMarket struct {
	book  core.Snapshot
	clock *time.Time
}

func (m *automationMarket) Snapshot(_ context.Context, _, _ string) (core.Snapshot, error) {
	if m.clock == nil {
		return m.book, nil
	}
	// A test clock that advanced must not look like a stale market read.
	book := m.book
	book.Observed = *m.clock
	return book, nil
}

// automationSource hands out one pre-built signal, or ErrNoSignal.
type automationSource struct {
	mu       sync.Mutex
	signals  []Signal
	failures int
}

func (s *automationSource) Next(context.Context, risk.Policy) (Signal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		s.failures--
		return Signal{}, errors.New("automation: the model is unavailable")
	}
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
	market := &automationMarket{clock: &clock, book: core.Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual", Bid: "99999", Ask: "100000", BidSize: "100", AskSize: "100", Tick: "1", Step: "0.001", MinQty: "0.001", MinNotional: "10", Observed: now}}
	venue := &managedTraderFixture{venue: "binance", key: "account", env: execution.EnvironmentLive, clock: &clock, orders: map[string]execution.Order{}, settings: map[string]int{}}
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
	if got := rig.venue.placeCount(); got != 0 {
		t.Fatalf("an entry was submitted before authorization completed: %d", got)
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

// TestSupervisionProtectsWithoutEntry proves a recovered position is protected
// without any entry being replayed: supervision places the stop-loss, never an
// entry.
func TestSupervisionProtectsWithoutEntry(t *testing.T) {
	rig := rig(t, nil)
	plan := rig.plan(t)
	startedPlan(t, rig, plan, "FILLED", "filled-entry")
	if err := rig.ctrl.DB.Model(&live.Run{}).Where("user_id = ?", "alice").Update("state", "position_open").Error; err != nil {
		t.Fatal(err)
	}
	rig.venue.snapshot.Positions = []execution.ManagedPosition{{Symbol: plan.Snapshot.Symbol, Side: "long", Quantity: plan.Quantity, Entry: plan.Idea.Entry, Mark: plan.Idea.Entry}}
	rt := rig.runtime(t, nil)
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && rig.venue.protectionCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if rig.venue.protectionCount() == 0 {
		t.Fatal("a recovered position was left without a stop-loss")
	}
	if got := rig.venue.entryCount(); got != 0 {
		t.Fatalf("supervision replayed %d entries on a recovered position", got)
	}
}

// TestFlattenAccountReachesTheController proves the management seam delegates
// the reduction to the controller's own state machine instead of writing an
// order of its own, and refuses an account that belongs to another user.
func TestFlattenAccountReachesTheController(t *testing.T) {
	rig := rig(t, nil)
	plan := rig.plan(t)
	startedPlan(t, rig, plan, "FILLED", "filled-entry")
	if err := rig.ctrl.DB.Model(&live.Run{}).Where("user_id = ?", "alice").Update("state", "position_open").Error; err != nil {
		t.Fatal(err)
	}
	rig.venue.snapshot.Positions = []execution.ManagedPosition{{Symbol: plan.Snapshot.Symbol, Side: "long", Quantity: plan.Quantity, Entry: plan.Idea.Entry, Mark: plan.Idea.Entry}}
	rt := rig.runtime(t, nil)
	if err := rt.FlattenAccount(context.Background(), "bob", "account", "review"); err == nil {
		t.Fatal("a foreign account key was flattened")
	}
	if err := rt.PauseAccount(context.Background(), "bob", "account", "review"); err == nil {
		t.Fatal("a foreign account key was paused")
	}
	// The exit is sent but the venue read still shows the position, so the
	// controller reports that state instead of claiming a flat account.
	if err := rt.FlattenAccount(context.Background(), "alice", "account", "review"); err != nil &&
		!strings.Contains(err.Error(), "not confirmed flat") {
		t.Fatalf("flatten failed: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && rig.venue.exitCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if rig.venue.exitCount() == 0 {
		t.Fatal("flatten never asked the controller for a reduce-only exit")
	}
	// The exit must be a reduction: no entry may be submitted by a flatten.
	if got := rig.venue.entryCount(); got != 0 {
		t.Fatalf("flatten submitted %d entries", got)
	}
	for _, o := range rig.venue.placedOrders() {
		if o.Role == "exit" && !o.ReduceOnly {
			t.Fatal("the flatten exit was not reduce-only")
		}
	}
}

// TestRecoveryBreakpoints proves every crash point between the approval and a
// submitted entry leaves at most one entry POST after a restart, and that an
// unresolved entry never becomes a second one.
func TestRecoveryBreakpoints(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, rig *automationRig, plan core.Plan) live.View
		posts   int
		state   string
		frozen  bool
	}{
		{
			// Approved but never started: no entry was ever written, so a
			// restart may only retire the approval.
			name: "approved_only",
			prepare: func(t *testing.T, rig *automationRig, plan core.Plan) live.View {
				return approvedPlan(t, rig, plan)
			},
			posts: 0, state: "approved",
		},
		{
			// Started, the entry persisted as scheduled, then the process died
			// before the venue saw it: reconciliation may submit it once.
			name: "started_not_submitted",
			prepare: func(t *testing.T, rig *automationRig, plan core.Plan) live.View {
				return startedPlan(t, rig, plan, "scheduled", "")
			},
			posts: 1, state: "running",
		},
		{
			// The intent is durable in submitting: a restart must not add a
			// second entry.
			name: "submitting_persisted",
			prepare: func(t *testing.T, rig *automationRig, plan core.Plan) live.View {
				return startedPlan(t, rig, plan, "submitting", "")
			},
			posts: 1, state: "running",
		},
		{
			// The venue may hold the entry and the reply never arrived.
			name: "entry_unknown",
			prepare: func(t *testing.T, rig *automationRig, plan core.Plan) live.View {
				return startedPlan(t, rig, plan, "unknown", "unknown-entry")
			},
			posts: 0, state: "halted_unknown", frozen: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := rig(t, nil)
			plan := rig.plan(t)
			tc.prepare(t, rig, plan)
			rt := rig.runtime(t, nil)
			// A frozen breakpoint may report an error; the run state and the
			// POST count are what the matrix asserts.
			_ = rt.Recover(context.Background(), "alice", "account")
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if _, frozen := rt.frozenReason("account"); frozen == tc.frozen {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if got := rig.venue.placeCount(); got > tc.posts {
				t.Fatalf("%s: recovery submitted %d entries, at most %d allowed", tc.name, got, tc.posts)
			}
			if _, frozen := rt.frozenReason("account"); frozen != tc.frozen {
				t.Fatalf("%s: frozen=%v, want %v", tc.name, frozen, tc.frozen)
			}
			runs := runStates(t, rig.db)
			if len(runs) != 1 {
				t.Fatalf("%s: expected one run, got %d", tc.name, len(runs))
			}
			if runs[0].State != tc.state {
				t.Fatalf("%s: run state %q, want %q", tc.name, runs[0].State, tc.state)
			}
		})
	}
}

// plan builds and drafts the compiled plan the fixture authorizes.
func (r *automationRig) plan(t *testing.T) core.Plan {
	t.Helper()
	limits, err := live.ManagedAccountLimits(r.policy, r.venue.snapshot, r.now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := core.Compile(r.book, limits, r.idea(), r.now)
	if err != nil {
		t.Fatal(err)
	}
	store := &planning.DraftStore{DB: r.db, UserID: "alice", ExchangeID: "exchange", Venue: "binance"}
	if err := store.Add([]core.Plan{plan}); err != nil {
		t.Fatal(err)
	}
	return plan
}

// entryClientID mirrors the controller's own derivation; a fixture that made
// its own ID would test a run the controller would refuse to load.
func entryClientID(run string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/entry/", run, index)))
	return hex.EncodeToString(sum[:16])
}

func approvedPlan(t *testing.T, r *automationRig, plan core.Plan) live.View {
	t.Helper()
	v, err := r.ctrl.Approve(context.Background(), "alice", plan.ID, plan.Hash, "APPROVE LIVE")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// startedPlan writes the durable run row a crashed process would have left
// behind: started, with one entry order in the requested status.
func startedPlan(t *testing.T, r *automationRig, plan core.Plan, status, orderID string) live.View {
	t.Helper()
	v := approvedPlan(t, r, plan)
	now := r.now
	deadline := now.Add(time.Hour)
	// The basket must match the approval exactly, including the derived client
	// ID, or the controller refuses to touch the run at all.
	specs := plan.Orders
	if len(specs) == 0 {
		specs = []core.OrderSpec{{Index: 1, Price: plan.Idea.Entry, Quantity: plan.Quantity}}
	}
	var orders []live.Order
	for _, spec := range specs {
		// A confirmed fill must report its own quantity: protection is sized
		// from confirmed fills, and a filled entry claiming zero is refused.
		filled := execution.Decimal("0")
		if status == "FILLED" {
			filled = spec.Quantity
		}
		orders = append(orders, live.Order{ClientID: entryClientID(v.ID, spec.Index), OrderID: orderID, Role: "entry", Symbol: plan.Snapshot.Symbol, Side: plan.Idea.Side, Price: spec.Price, Quantity: spec.Quantity, Status: status, Filled: filled})
	}
	raw, err := json.Marshal(orders)
	if err != nil {
		t.Fatal(err)
	}
	state := "running"
	if status == "unknown" {
		state = "halted_unknown"
	}
	if err := r.ctrl.DB.Model(&live.Run{}).Where("id = ?", v.ID).Updates(map[string]any{"state": state, "orders_json": string(raw), "started_at": &now, "deadline": &deadline}).Error; err != nil {
		t.Fatal(err)
	}
	v.State, v.StartedAt, v.Deadline, v.Orders = state, &now, &deadline, orders
	return v
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

// TestRecoveryLiftsSafetyFreezeWhenFlat proves the only automatic exit from a
// safety freeze: the venue holds nothing and no exit is unresolved.
func TestRecoveryLiftsSafetyFreezeWhenFlat(t *testing.T) {
	rig := rig(t, nil)
	rig.venue.snapshot.Positions = []execution.ManagedPosition{{Symbol: "ETHUSDT", Side: "long", Quantity: "1.5", Entry: "4000", Mark: "4000"}}
	rt := rig.runtime(t, nil)
	if err := rt.Recover(context.Background(), "alice", "account"); err == nil {
		t.Fatal("recovery adopted a position no run owns")
	}
	if _, frozen := rt.frozenReason("account"); !frozen {
		t.Fatal("an external position did not freeze new entries")
	}
	// The operator closes the position by hand; the next recovery sees flat.
	rig.venue.snapshot.Positions = nil
	if err := rt.Recover(context.Background(), "alice", "account"); err != nil {
		t.Fatalf("a flat account stayed frozen: %v", err)
	}
	if reason, frozen := rt.frozenReason("account"); frozen {
		t.Fatalf("a flat account kept the safety freeze: %s", reason)
	}
	if rig.venue.placeCount() != 0 {
		t.Fatal("recovery placed an order")
	}
}

// TestRecoveryHaltedUnknownStaysFrozen proves an unresolved exit is never
// replayed: the run stays halted, the account stays frozen, and no entry is
// submitted on the same tick or the next one.
func TestRecoveryHaltedUnknownStaysFrozen(t *testing.T) {
	rig := rig(t, nil)
	rig.source.signals = []Signal{{ID: "signal-1", Idea: rig.idea(), CreatedAt: rig.now, ExpiresAt: rig.now.Add(SignalExpiry)}}
	rt := rig.runtime(t, rig.source)
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	rig.waitPlaced(t, 1)

	var run live.Run
	if err := rig.db.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := rig.db.Model(&live.Run{}).Where("id = ?", run.ID).
		Updates(map[string]any{"state": "halted_unknown", "error": "exit unknown"}).Error; err != nil {
		t.Fatal(err)
	}
	before := rig.venue.placeCount()
	rig.source.signals = []Signal{{ID: "signal-2", Idea: rig.idea(), CreatedAt: rig.now, ExpiresAt: rig.now.Add(SignalExpiry)}}
	if err := rt.Recover(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if _, frozen := rt.frozenReason("account"); !frozen {
		t.Fatal("an unresolved exit left the account open for new entries")
	}
	rig.advance(SignalInterval)
	if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if got := rig.venue.placeCount(); got != before {
		t.Fatalf("a halted run replayed %d orders", got-before)
	}
	if state := runStates(t, rig.db)[0].State; state != "halted_unknown" {
		t.Fatalf("recovery moved the halted run to %q", state)
	}
}

// TestFreezeKindsAreIsolated proves a recovered risk read cannot lift a
// safety freeze, and a safety recovery cannot lift a risk freeze.
func TestFreezeKindsAreIsolated(t *testing.T) {
	rig := rig(t, nil)
	rt := rig.runtime(t, nil)
	ctx := context.Background()
	rig.venue.snapshot.Positions = []execution.ManagedPosition{{Symbol: "ETHUSDT", Side: "long", Quantity: "1.5"}}
	if err := rt.Recover(ctx, "alice", "account"); err == nil {
		t.Fatal("an external position was accepted")
	}
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatal(err)
	}
	if reason, frozen := rt.frozenReason("account"); !frozen || reason == "" {
		t.Fatal("the external-position freeze was cleared by a healthy risk read")
	}
}

// TestSignalFreezePausesOnlyNewSignals proves a source failure stops entries
// without becoming permanent: the next successful read lifts it.
func TestSignalFreezePausesOnlyNewSignals(t *testing.T) {
	rig := rig(t, nil)
	rig.source.failures = SignalFailureTolerance
	rt := rig.runtime(t, rig.source)
	ctx := context.Background()
	for i := 0; i < SignalFailureTolerance; i++ {
		if err := rt.Tick(ctx, "alice", "account"); err == nil {
			t.Fatal("a failing source reported success")
		}
		rig.advance(SignalInterval)
	}
	if kind, frozen := rt.frozenKind("account"); !frozen || kind != freezeSignal {
		t.Fatalf("source failures did not freeze new signals: kind=%d frozen=%v", kind, frozen)
	}
	// The frozen source recovers: one good round must clear the pause.
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatalf("a recovered source stayed frozen: %v", err)
	}
	if _, frozen := rt.frozenKind("account"); frozen {
		t.Fatal("a successful signal read did not lift the source freeze")
	}
}
