package automation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
	"nofx/logger"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

const (
	ReconcileInterval = 10 * time.Second
	SignalInterval    = 60 * time.Second
	EntryFillWindow   = 30 * time.Second
	MaxHold           = 24 * time.Hour
	// SignalFailureTolerance is how many consecutive source failures only
	// pause new signals, per the plan's global constraints.
	SignalFailureTolerance = 3
)

// Factory builds the managed adapter for one saved exchange configuration. It
// must return the binding that owns the account, never a default.
type Factory func(context.Context, string, string) (execution.ManagedTrader, execution.Binding, error)

// accountState is the per-account serialisation point plus the two cadence
// clocks, so a slow account never starves another and restarts do not depend on
// timers surviving the process.
type accountState struct {
	user           string
	lastReconcile  time.Time
	lastSignal     time.Time
	signalFailures int
}

// Runtime supervises unattended execution: it reconciles what the venue shows,
// keeps protection alive, and only then asks the model for a new entry. Every
// failure freezes new entries while protection supervision continues.
type Runtime struct {
	db         *gorm.DB
	controller *live.Controller
	risks      *risk.Store
	factory    Factory
	source     IdeaSource
	now        func() time.Time

	mu        sync.Mutex
	accounts  map[string]*accountState
	locks     map[string]*sync.Mutex
	frozen    map[string]freezeState
	reconcile map[string]string
	pending   map[string][]execution.AccountEvent
	known     []managedAccount
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
	wg        sync.WaitGroup
}

func NewRuntime(db *gorm.DB, controller *live.Controller, risks *risk.Store, factory Factory, source IdeaSource, now func() time.Time) (*Runtime, error) {
	if db == nil || controller == nil || risks == nil || factory == nil {
		return nil, errors.New("automation: runtime requires a database, controller, risk store and account factory")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if err := db.AutoMigrate(&execution.Binding{}, &EventCursor{}); err != nil {
		return nil, err
	}
	return &Runtime{db: db, controller: controller, risks: risks, factory: factory, source: source, now: now, accounts: map[string]*accountState{}, locks: map[string]*sync.Mutex{}, frozen: map[string]freezeState{}, reconcile: map[string]string{}, pending: map[string][]execution.AccountEvent{}}, nil
}

func (r *Runtime) clock() time.Time { return r.now().UTC() }

// Start launches the supervisor. Accounts are discovered from verified bindings
// on every round, so a binding created while the runtime runs is picked up
// without a restart.
func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("automation: runtime already started")
	}
	r.started = true
	r.ctx, r.cancel = context.WithCancel(ctx)
	run := r.ctx
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.supervise(run)
	}()
	return nil
}

// Shutdown stops the supervisor and waits for it. It never cancels venue
// protection and never stops the shared controller, whose runs belong to the
// API as well.
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	cancel := r.cancel
	r.started = false
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// The waiter above is bounded by the supervisor itself, which exits as
		// soon as its context is cancelled; nothing is abandoned here.
		return errors.New("automation: timed out waiting for the supervisor to stop")
	}
}

func (r *Runtime) supervise(ctx context.Context) {
	for {
		for _, account := range r.managedAccounts(ctx) {
			if ctx.Err() != nil {
				return
			}
			if err := r.Tick(ctx, account.user, account.key); err != nil {
				logger.Warnf("automation: %s supervision failed: %v", account.key, err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ReconcileInterval):
		}
	}
}

type managedAccount struct{ user, key string }

func (r *Runtime) managedAccounts(ctx context.Context) []managedAccount {
	var rows []execution.Binding
	if err := r.db.WithContext(ctx).Where("enabled = ? AND verified_at IS NOT NULL", true).Find(&rows).Error; err != nil {
		// A failed list must not stop supervision of accounts already known:
		// that would abandon live protection on a transient database fault.
		logger.Warnf("automation: could not list managed accounts: %v", err)
		r.mu.Lock()
		defer r.mu.Unlock()
		out := make([]managedAccount, 0, len(r.known))
		for _, account := range r.known {
			out = append(out, account)
		}
		return out
	}
	out := make([]managedAccount, 0, len(rows))
	for _, b := range rows {
		out = append(out, managedAccount{user: b.UserID, key: b.ID})
	}
	r.mu.Lock()
	r.known = out
	r.mu.Unlock()
	return out
}

func (r *Runtime) lock(accountKey string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.locks[accountKey]
	if !ok {
		m = &sync.Mutex{}
		r.locks[accountKey] = m
	}
	return m
}

func (r *Runtime) state(accountKey, user string) *accountState {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.accounts[accountKey]
	if !ok {
		s = &accountState{user: user}
		r.accounts[accountKey] = s
	}
	return s
}

// Freeze kinds. Only the kind that set a freeze may clear it: a recovered
// risk read must never lift a safety stop (an unowned position, an unresolved
// exit) or a source failure, and vice versa.
const (
	freezeSafety = iota + 1
	freezeRisk
	freezeSignal
)

type freezeState struct {
	reason string
	kind   int
}

// freeze stops new entries for one account. Protection supervision keeps
// running, because abandoning a live stop-loss would be worse than the fault.
func (r *Runtime) freeze(accountKey, reason string, kind int) {
	r.mu.Lock()
	previous, had := r.frozen[accountKey]
	r.frozen[accountKey] = freezeState{reason: reason, kind: kind}
	r.mu.Unlock()
	if had && previous.reason == reason && previous.kind == kind {
		return
	}
	logger.Warnf("automation: %s frozen for new entries: %s", accountKey, reason)
}

// thaw clears only freezes of the given kind.
func (r *Runtime) thaw(accountKey string, kind int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.frozen[accountKey]; ok && current.kind == kind {
		delete(r.frozen, accountKey)
	}
}

func (r *Runtime) frozenReason(accountKey string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.frozen[accountKey]
	return current.reason, ok
}

func (r *Runtime) markReconcile(accountKey, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconcile[accountKey] = reason
}

func (r *Runtime) clearReconcile(accountKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.reconcile, accountKey)
}

func (r *Runtime) reconciling(accountKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.reconcile[accountKey]
	return ok
}

// Tick advances one account: protection and reconciliation first, then a
// snapshot for the risk ledger, and only then a possible new signal.
func (r *Runtime) Tick(ctx context.Context, user, accountKey string) error {
	if accountKey == "" || user == "" {
		return errors.New("automation: tick requires a user and an account")
	}
	lock := r.lock(accountKey)
	lock.Lock()
	defer lock.Unlock()
	state := r.state(accountKey, user)

	if err := r.superviseRuns(ctx, user, accountKey); err != nil {
		r.freeze(accountKey, err.Error(), freezeSafety)
		return err
	}
	now := r.clock()
	if state.lastReconcile.IsZero() || now.Sub(state.lastReconcile) >= ReconcileInterval {
		if err := r.reconcileAccount(ctx, user, accountKey); err != nil {
			r.freeze(accountKey, err.Error(), freezeRisk)
			return err
		}
		state.lastReconcile = now
	}
	if r.source == nil {
		return nil
	}
	// A stream that went quiet or broke must be reconciled before another
	// signal: the venue may already hold a fill this process never saw.
	if r.reconciling(accountKey) {
		return nil
	}
	if _, frozen := r.frozenReason(accountKey); frozen {
		return nil
	}
	if state.lastSignal.IsZero() || now.Sub(state.lastSignal) >= SignalInterval {
		state.lastSignal = now
		if err := r.signal(ctx, user, accountKey); err != nil {
			if errors.Is(err, ErrNoSignal) {
				state.signalFailures = 0
				return nil
			}
			// A failing source pauses new signals after SignalFailureTolerance
			// consecutive rounds; a single bad reply must not cost a whole day.
			state.signalFailures++
			if state.signalFailures >= SignalFailureTolerance {
				r.freeze(accountKey, err.Error(), freezeSignal)
			}
			return err
		}
		state.signalFailures = 0
		r.thaw(accountKey, freezeSignal)
	}
	return nil
}

// superviseRuns reconciles every non-terminal run this runtime owns. It never
// calls Execute: only a fresh signal may submit an entry.
func (r *Runtime) superviseRuns(ctx context.Context, user, accountKey string) error {
	views, err := r.controller.ListContext(ctx, user)
	if err != nil {
		return fmt.Errorf("automation: could not list runs: %w", err)
	}
	for _, v := range views {
		if v.AccountKey != accountKey {
			continue
		}
		switch v.State {
		case "running", "stopping", "position_open", "needs_attention":
			if _, err := r.controller.ReconcileContext(ctx, user, v.ID); err != nil {
				return fmt.Errorf("automation: could not reconcile run %s: %w", v.ID, err)
			}
		case "halted_unknown":
			// Frozen by design: only the original exit client ID may resolve it.
			r.freeze(accountKey, "a run halted with an unresolved exit; resolve it before new entries", freezeSafety)
		}
	}
	return nil
}

// reconcileAccount reads the venue and feeds the risk ledger. A failed read is
// a freeze, never a default: sizing on a stale equity would be worse than
// standing still.
func (r *Runtime) reconcileAccount(ctx context.Context, user, accountKey string) error {
	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		return err
	}
	trader, err := r.trader(ctx, user, binding.ExchangeID)
	if err != nil {
		return err
	}
	// History first: the events it returns are attributed to the snapshot below,
	// so funding and fees land in the same ledger day as the equity they explain.
	if reader, ok := trader.(historySource); ok {
		if err := r.syncHistory(ctx, user, accountKey, binding.Venue, reader.ManagedHistory()); err != nil {
			return err
		}
	}
	events := r.drainEvents(accountKey)
	account, err := trader.ManagedSnapshot(ctx)
	if err != nil {
		r.bufferEvents(accountKey, events)
		r.markReconcile(accountKey, "account read failed")
		return fmt.Errorf("automation: could not read the managed account: %w", err)
	}
	if account.AccountKey != accountKey {
		r.bufferEvents(accountKey, events)
		return fmt.Errorf("automation: the venue reported account %q instead of %q", account.AccountKey, accountKey)
	}
	now := r.clock()
	if err := r.risks.Observe(ctx, account, events, now); err != nil {
		return fmt.Errorf("automation: could not record the equity observation: %w", err)
	}
	decision, err := r.risks.Evaluate(ctx, user, accountKey, now)
	if err != nil {
		return fmt.Errorf("automation: could not evaluate the risk breaker: %w", err)
	}
	if decision.Action != risk.ActionAllow {
		r.freeze(accountKey, decision.Reason, freezeRisk)
	} else {
		r.thaw(accountKey, freezeRisk)
	}
	r.clearReconcile(accountKey)
	return nil
}

// historySource is an adapter that can replay the account's money movements.
type historySource interface {
	ManagedHistory() execution.HistoryReader
}

func (r *Runtime) binding(ctx context.Context, accountKey string) (execution.Binding, error) {
	var b execution.Binding
	err := r.db.WithContext(ctx).Where("id = ?", accountKey).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return b, errors.New("automation: no managed account binding owns this account")
	}
	if err != nil {
		return b, err
	}
	if !b.Enabled || b.VerifiedAt.IsZero() {
		return b, errors.New("automation: the physical account is not verified; unattended execution stays disabled")
	}
	return b, nil
}

func (r *Runtime) trader(ctx context.Context, user, exchangeID string) (execution.ManagedTrader, error) {
	t, b, err := r.factory(ctx, user, exchangeID)
	if err != nil {
		return nil, fmt.Errorf("automation: could not open the managed account: %w", err)
	}
	if !b.Enabled || b.VerifiedAt.IsZero() {
		return nil, errors.New("automation: the physical account is not verified")
	}
	return t, nil
}

// signal runs the model to authorization pipeline. Nothing reaches the venue
// before AuthorizePolicy has bound the run to this policy version and signal.
func (r *Runtime) signal(ctx context.Context, user, accountKey string) error {
	policy, err := r.risks.Policy(ctx, user, accountKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("automation: managed account has no risk policy")
		}
		return err
	}
	if !policy.Enabled {
		return nil
	}
	signal, err := r.source.Next(ctx, policy)
	if err != nil {
		return err
	}
	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		return err
	}
	trader, err := r.trader(ctx, user, binding.ExchangeID)
	if err != nil {
		return err
	}
	account, err := trader.ManagedSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("automation: could not read the managed account: %w", err)
	}
	now := r.clock()
	limits, err := live.ManagedAccountLimits(policy, account, now)
	if err != nil {
		return err
	}
	plan, err := r.compileIdea(ctx, policy, limits, signal.Idea, now)
	if err != nil {
		return err
	}
	store := &planning.DraftStore{DB: r.db, UserID: user, ExchangeID: binding.ExchangeID, Venue: policy.Venue}
	if err := store.Add([]core.Plan{plan}); err != nil {
		return err
	}
	authorized, err := r.controller.AuthorizePolicy(ctx, user, plan.ID, plan.Hash, policy.Version, signal.ID)
	if err != nil {
		return err
	}
	if _, err := r.controller.Execute(ctx, user, authorized.ID, authorized.ApprovalHash); err != nil {
		return err
	}
	return nil
}

// compileIdea binds the idea to the one whitelisted instrument whose book the
// idea's absolute prices actually fit. The signal carries no symbol, so trying
// the whitelist in order is what keeps a mismatched instrument from compiling.
func (r *Runtime) compileIdea(ctx context.Context, policy risk.Policy, limits core.Constraints, idea core.Idea, now time.Time) (core.Plan, error) {
	if r.controller.Market == nil {
		return core.Plan{}, errors.New("automation: the controller has no market source for sizing")
	}
	var lastErr error
	for _, symbol := range policy.Symbols {
		book, err := r.controller.Market.Snapshot(ctx, policy.Venue, symbol)
		if err != nil {
			lastErr = err
			continue
		}
		plan, err := core.Compile(book, limits, idea, now)
		if err != nil {
			lastErr = err
			continue
		}
		if err := live.VerifyManagedBudget(plan, policy); err != nil {
			return core.Plan{}, err
		}
		return plan, nil
	}
	if lastErr == nil {
		lastErr = errors.New("automation: the policy whitelist is empty")
	}
	return core.Plan{}, fmt.Errorf("automation: no whitelisted instrument accepts the signal: %w", lastErr)
}

// holdExpired reports whether a started run has outlived the unattended limits.
// The controller keeps its own entry deadline; this only refuses to extend it.
func holdExpired(startedAt *time.Time, now time.Time) bool {
	return startedAt != nil && now.Sub(*startedAt) >= MaxHold
}
