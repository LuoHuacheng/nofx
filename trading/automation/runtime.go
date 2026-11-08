package automation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	ReconcileInterval = 2 * time.Second
	SignalInterval    = time.Duration(SignalBarSeconds) * time.Second
	EntryFillWindow   = live.PolicyEntryWindow
	MaxHold           = live.PolicyMaxHold
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
	signalIndex    int
	signalFailures int
	signalPending  bool
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
	streams   map[string]bool
	pending   map[string][]execution.AccountEvent
	known     []managedAccount
	alertURL  string
	lastEvent map[string]time.Time
	// alertClient is the delivery seam; production uses a guarded dialer.
	alertClient *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	started     bool
	wg          sync.WaitGroup
}

func NewRuntime(db *gorm.DB, controller *live.Controller, risks *risk.Store, factory Factory, source IdeaSource, now func() time.Time) (*Runtime, error) {
	if db == nil || controller == nil || risks == nil || factory == nil {
		return nil, errors.New("automation: runtime requires a database, controller, risk store and account factory")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if err := db.AutoMigrate(&execution.Binding{}, &EventCursor{}, &Alert{}, &alertTarget{}, &signalScan{}); err != nil {
		return nil, err
	}
	return &Runtime{db: db, controller: controller, risks: risks, factory: factory, source: source, now: now, accounts: map[string]*accountState{}, locks: map[string]*sync.Mutex{}, frozen: map[string]freezeState{}, reconcile: map[string]string{}, streams: map[string]bool{}, pending: map[string][]execution.AccountEvent{}, lastEvent: map[string]time.Time{}}, nil
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
	r.wg.Add(1)
	r.mu.Unlock()
	recovered := map[string]bool{}
	for _, account := range r.managedAccounts(run) {
		if err := r.Recover(run, account.user, account.key); err != nil {
			logger.Warnf("automation: %s startup recovery failed: %v", account.key, err)
			continue
		}
		recovered[account.key] = true
	}
	go func() {
		defer r.wg.Done()
		r.supervise(run, recovered)
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

func (r *Runtime) supervise(ctx context.Context, recovered map[string]bool) {
	for {
		for _, account := range r.managedAccounts(ctx) {
			if ctx.Err() != nil {
				return
			}
			if !recovered[account.key] {
				if err := r.Recover(ctx, account.user, account.key); err != nil {
					logger.Warnf("automation: %s startup recovery failed: %v", account.key, err)
					continue
				}
				recovered[account.key] = true
			}
			r.startStream(ctx, account)
			if err := r.tick(ctx, account.user, account.key, true); err != nil {
				logger.Warnf("automation: %s supervision failed: %v", account.key, err)
			}
		}
		// Delivery is a separate concern from supervision: a failing webhook must
		// never delay protection, so it runs after the tick and only logs.
		if err := r.DeliverAlerts(ctx); err != nil {
			logger.Warnf("automation: alert delivery failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ReconcileInterval):
		}
	}
}

// startStream opens the account's private event stream once per process. The
// stream only shortens the time a fill waits for protection; the REST window
// stays the authority, so a broken stream is logged and re-read rather than
// trusted or treated as a quiet account.
func (r *Runtime) startStream(ctx context.Context, account managedAccount) {
	r.mu.Lock()
	if r.streams[account.key] {
		r.mu.Unlock()
		return
	}
	r.streams[account.key] = true
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.streams, account.key)
			r.mu.Unlock()
		}()
		r.streamAccount(ctx, account.user, account.key)
	}()
}

func (r *Runtime) streamAccount(ctx context.Context, user, accountKey string) {
	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		logger.Warnf("automation: %s private stream unavailable: %v", accountKey, err)
		return
	}
	trader, err := r.trader(ctx, user, binding.ExchangeID)
	if err != nil {
		logger.Warnf("automation: %s private stream unavailable: %v", accountKey, err)
		return
	}
	err = trader.Events(ctx, func(e execution.AccountEvent) error {
		// Every stream event is ingested on its own so a partial fill reaches
		// protection immediately instead of waiting for a batch boundary.
		return r.Ingest(ctx, user, accountKey, []execution.AccountEvent{e})
	})
	if ctx.Err() != nil {
		return
	}
	// The stream ended on its own: the venue record is now the only source, so
	// the next tick must re-read it before anything trusts this process's view.
	if err != nil {
		logger.Warnf("automation: %s private stream ended: %v", accountKey, err)
	}
	r.markReconcile(accountKey, "the private stream is not running; the venue record is the only source")
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
// A weaker freeze never replaces a stronger one: otherwise a transient risk
// fault could overwrite a safety stop that only a human or a restart may lift.
func (r *Runtime) freeze(accountKey, reason string, kind int) {
	r.mu.Lock()
	previous, had := r.frozen[accountKey]
	if had && previous.kind <= kind {
		r.mu.Unlock()
		return
	}
	r.frozen[accountKey] = freezeState{reason: reason, kind: kind}
	r.mu.Unlock()
	logger.Warnf("automation: %s frozen for new entries: %s", accountKey, reason)
	// A freeze is only auditable if it outlives the process: the outbox row is
	// written best-effort, and a failure here must not weaken the freeze itself.
	if err := r.QueueAlert(context.Background(), Alert{
		AccountKey: accountKey,
		Code:       alertCodeFreeze(kind),
		Severity:   AlertSeverityCritical,
		Message:    reason,
	}); err != nil {
		logger.Warnf("automation: %s freeze was not queued for delivery: %v", accountKey, err)
	}
}

// alertCodeFreeze names the freeze in operator terms so a pager message says
// what stopped the account without reading the runtime's internals.
func alertCodeFreeze(kind int) string {
	switch kind {
	case freezeSafety:
		return "execution_safety_freeze"
	case freezeRisk:
		return "risk_freeze"
	}
	return "signal_freeze"
}

// thaw clears only freezes of the given kind.
func (r *Runtime) thaw(accountKey string, kind int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.frozen[accountKey]; ok && current.kind == kind {
		delete(r.frozen, accountKey)
	}
}

// frozenKind reports the kind of the active freeze, if any.
func (r *Runtime) frozenKind(accountKey string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.frozen[accountKey]
	return current.kind, ok
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

// noteReconcile records the slot under the lock: Ingest reads the same clock
// from another goroutine and must never see a stale value.
func (r *Runtime) noteReconcile(accountKey string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.accounts[accountKey]; ok {
		s.lastReconcile = now
	}
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
	return r.tick(ctx, user, accountKey, false)
}

func (r *Runtime) tick(ctx context.Context, user, accountKey string, asynchronous bool) error {
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
	}
	if r.source == nil {
		return nil
	}
	// A stream that went quiet or broke must be reconciled before another
	// signal: the venue may already hold a fill this process never saw.
	if r.reconciling(accountKey) {
		return nil
	}
	// A source freeze only pauses new signals, so the attempt below must still
	// run: a successful read is what lifts it. Safety and risk freezes stop it.
	if kind, frozen := r.frozenKind(accountKey); frozen && kind != freezeSignal {
		return nil
	}
	if !state.signalPending {
		symbol, err := r.nextSignalSymbol(ctx, user, accountKey, state, now)
		if err != nil {
			return err
		}
		if symbol == "" {
			return nil
		}
		signalCtx := context.WithValue(ctx, signalBarCloseKey{}, state.lastSignal)
		if asynchronous {
			state.signalPending = true
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				policy, signal, err := r.collectSignal(signalCtx, user, accountKey, symbol)
				lock.Lock()
				defer lock.Unlock()
				state.signalPending = false
				if ctx.Err() != nil {
					return
				}
				// Re-check freezes after model latency, before any authorization.
				if kind, frozen := r.frozenKind(accountKey); frozen && kind != freezeSignal {
					return
				}
				if err == nil {
					err = r.applySignal(ctx, user, accountKey, policy, signal)
				}
				if err := r.signalOutcome(accountKey, state, err); err != nil {
					logger.Warnf("automation: %s supervision failed: %v", accountKey, err)
				}
			}()
			return nil
		}
		return r.signalOutcome(accountKey, state, r.signal(signalCtx, user, accountKey, symbol))
	}
	return nil
}

func (r *Runtime) signalOutcome(accountKey string, state *accountState, err error) error {
	if err != nil && !errors.Is(err, ErrNoSignal) {
		state.signalFailures++
		if state.signalFailures >= SignalFailureTolerance {
			r.freeze(accountKey, err.Error(), freezeSignal)
		}
		return err
	}
	// An empty candidate set is a healthy source response.
	state.signalFailures = 0
	r.thaw(accountKey, freezeSignal)
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
	events := r.ledgerEvents(r.drainEvents(accountKey))
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
	// The slot is claimed here rather than by each caller: Tick and a stream
	// event both read the venue, and only one of them may pay for it per window.
	r.noteReconcile(accountKey, now)
	return nil
}

// ledgerEvents keeps only money movements. A stream event that merely proves
// exposure changed, or that the venue record must be re-read, is supervision
// input: the ledger rejects it, and a reconcile marker has already done its work
// by landing here at all.
func (r *Runtime) ledgerEvents(events []execution.AccountEvent) []execution.AccountEvent {
	out := make([]execution.AccountEvent, 0, len(events))
	for _, e := range events {
		if streamOnly(e.Kind) {
			continue
		}
		out = append(out, e)
	}
	return out
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
func (r *Runtime) collectSignal(ctx context.Context, user, accountKey, symbol string) (risk.Policy, Signal, error) {
	policy, err := r.risks.Policy(ctx, user, accountKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return policy, Signal{}, errors.New("automation: managed account has no risk policy")
		}
		return policy, Signal{}, err
	}
	if !policy.Enabled {
		return policy, Signal{}, nil
	}
	allowed := false
	for _, candidate := range policy.Symbols {
		if candidate == symbol {
			allowed = true
			break
		}
	}
	if !allowed {
		return policy, Signal{}, errors.New("automation: scheduled symbol is no longer allowed")
	}
	// Scope only generation; authorization retains the full versioned policy.
	sourcePolicy := policy
	sourcePolicy.Symbols = []string{symbol}
	signal, err := r.source.Next(ctx, sourcePolicy)
	if err == nil && signal.Symbol != symbol {
		return policy, Signal{}, errors.New("automation: source returned another symbol")
	}
	return policy, signal, err
}

func (r *Runtime) signal(ctx context.Context, user, accountKey, symbol string) error {
	policy, signal, err := r.collectSignal(ctx, user, accountKey, symbol)
	if err != nil {
		return err
	}
	return r.applySignal(ctx, user, accountKey, policy, signal)
}

func (r *Runtime) applySignal(ctx context.Context, user, accountKey string, policy risk.Policy, signal Signal) error {
	if !policy.Enabled {
		return nil
	}
	if err := signal.Validate(r.clock()); err != nil {
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
	plan, err := r.compileIdea(ctx, policy, limits, signal)
	if err != nil {
		return err
	}
	store := &planning.DraftStore{DB: r.db, UserID: user, ExchangeID: binding.ExchangeID, Venue: policy.Venue}
	if err := store.Add([]core.Plan{plan}); err != nil {
		return err
	}
	if err := signal.Validate(r.clock()); err != nil {
		return err
	}
	// Model latency can outlive the equity baseline; reconcile history and balances before reserving risk.
	if err := r.reconcileAccount(ctx, user, accountKey); err != nil {
		return err
	}
	authorized, err := r.controller.AuthorizePolicy(ctx, user, plan.ID, plan.Hash, policy.Version, signal.ID, signal.ExpiresAt)
	if err != nil {
		return err
	}
	if _, err := r.controller.Execute(ctx, user, authorized.ID, authorized.ApprovalHash); err != nil {
		return err
	}
	return nil
}

// compileIdea preserves the server-selected instrument instead of guessing from prices.
func (r *Runtime) compileIdea(ctx context.Context, policy risk.Policy, limits core.Constraints, signal Signal) (core.Plan, error) {
	allowed := false
	for _, symbol := range policy.Symbols {
		if symbol == signal.Symbol && symbol != "" {
			allowed = true
			break
		}
	}
	if !allowed {
		return core.Plan{}, errors.New("automation: signal instrument is missing or not whitelisted")
	}
	if r.controller.Market == nil {
		return core.Plan{}, errors.New("automation: the controller has no market source for sizing")
	}
	book, err := r.controller.Market.Snapshot(ctx, policy.Venue, signal.Symbol)
	if err != nil {
		return core.Plan{}, fmt.Errorf("automation: %s market snapshot unavailable: %w", signal.Symbol, err)
	}
	if book.Symbol != signal.Symbol || book.Venue != policy.Venue {
		return core.Plan{}, errors.New("automation: market snapshot does not match the signal instrument")
	}
	plan, err := core.Compile(book, limits, signal.Idea, r.clock())
	if err != nil {
		return core.Plan{}, fmt.Errorf("automation: %s signal rejected: %w", signal.Symbol, err)
	}
	if err := live.VerifyManagedBudget(plan, policy); err != nil {
		return core.Plan{}, err
	}
	return plan, nil
}

// StartAccount enables unattended entries for one account, but only for the
// policy version the operator saw and only in the environment the binding was
// verified against. A breaker latch refuses the start: a reset is its own
// explicit action, never a side effect of starting.
func (r *Runtime) StartAccount(ctx context.Context, user, accountKey, version string, env execution.Environment) error {
	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		return err
	}
	if binding.UserID != user {
		return errors.New("automation: the account does not belong to this user")
	}
	if binding.Environment != env {
		return errors.New("automation: the account is bound to a different environment")
	}
	policy, err := r.risks.Policy(ctx, user, accountKey)
	if err != nil {
		return fmt.Errorf("automation: could not read the risk policy: %w", err)
	}
	if version == "" || policy.Version != version {
		return fmt.Errorf("automation: policy version %q is not the current %q", version, policy.Version)
	}
	if !policy.Enabled {
		return errors.New("automation: the risk policy is not enabled")
	}
	// Activation must read fresh equity; the REST polling window exceeds its maximum age.
	if err := r.Recover(ctx, user, accountKey); err != nil {
		return err
	}
	decision, err := r.risks.Evaluate(ctx, user, accountKey, r.clock())
	if err != nil {
		return fmt.Errorf("automation: could not evaluate the risk breaker: %w", err)
	}
	if decision.Action != risk.ActionAllow {
		return fmt.Errorf("automation: the risk breaker holds this account: %s", decision.Reason)
	}
	return nil
}

// PauseAccount latches new entries. Unfilled entries are refused by the same
// per-entry risk gate, and live protection is never touched: a pause must not
// leave a position unguarded. The latch is durable, so a restart keeps the
// pause and only an explicit reset lifts it.
func (r *Runtime) PauseAccount(ctx context.Context, user, accountKey, reason string) error {
	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		return err
	}
	if binding.UserID != user {
		return errors.New("automation: no managed account binding owns this account")
	}
	if reason == "" {
		reason = "operator pause"
	}
	return r.risks.Pause(ctx, user, accountKey, reason)
}

// FlattenAccount exits through the controller's own reduction state machine, so
// a flatten never becomes a second, unreviewed order path.
func (r *Runtime) FlattenAccount(ctx context.Context, user, accountKey, reason string) error {
	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		return err
	}
	if binding.UserID != user {
		return errors.New("automation: no managed account binding owns this account")
	}
	views, err := r.controller.ListContext(ctx, user)
	if err != nil {
		return fmt.Errorf("automation: could not list runs: %w", err)
	}
	if reason == "" {
		reason = "operator flatten"
	}
	var last error
	flattened := false
	for _, v := range views {
		if v.AccountKey != accountKey {
			continue
		}
		if v.State == "completed" || v.State == "cancelled" || v.State == "expired" || v.State == "approved" {
			continue
		}
		flattened = true
		if _, err := r.controller.Flatten(ctx, user, v.ID, reason); err != nil {
			last = fmt.Errorf("automation: could not flatten run %s: %w", v.ID, err)
		}
	}
	if !flattened {
		return errors.New("automation: no live run holds a position on this account")
	}
	return last
}

// Status is the operator view of one account. Healthy HTTP does not mean
// entries are allowed: State and Reason carry that answer.
type Status struct {
	AccountKey      string                `json:"account_key"`
	State           string                `json:"state"`
	ProtectionState string                `json:"protection_state"`
	PolicyVersion   string                `json:"policy_version"`
	Reason          string                `json:"reason"`
	Environment     execution.Environment `json:"environment"`
	LastREST        time.Time             `json:"last_rest"`
	LastWS          time.Time             `json:"last_ws"`
	LedgerComplete  bool                  `json:"ledger_complete"`
	ReservedRisk    execution.Decimal     `json:"reserved_risk"`
}

func (r *Runtime) AccountStatus(ctx context.Context, user, accountKey string) (Status, error) {
	binding, err := r.binding(ctx, accountKey)
	if err != nil {
		return Status{}, err
	}
	if binding.UserID != user {
		return Status{}, errors.New("automation: the account does not belong to this user")
	}
	out := Status{AccountKey: accountKey, State: "unknown", ProtectionState: "unknown", Environment: binding.Environment}
	if policy, err := r.risks.Policy(ctx, user, accountKey); err == nil {
		out.PolicyVersion = policy.Version
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Status{}, err
	}
	// A freeze outranks a breaker pause: the freeze is the harder stop, and
	// reporting the softer one would send an operator to the wrong recovery.
	if reason, frozen := r.frozenReason(accountKey); frozen {
		out.State = "frozen"
		out.Reason = reason
	} else if decision, err := r.risks.Evaluate(ctx, user, accountKey, r.clock()); err != nil {
		return Status{}, fmt.Errorf("automation: evaluate account status: %w", err)
	} else if decision.Action != risk.ActionAllow {
		out.State = "paused"
		out.Reason = decision.Reason
	} else {
		out.State = "running"
	}
	r.mu.Lock()
	if state, ok := r.accounts[accountKey]; ok {
		out.LastREST = state.lastReconcile
	}
	r.mu.Unlock()
	out.LastWS = r.lastWS(accountKey)
	if equity, err := r.risks.EquityState(ctx, accountKey, r.clock().Format("2006-01-02")); err == nil {
		out.LedgerComplete = equity.Complete
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Status{}, fmt.Errorf("automation: read status equity: %w", err)
	}
	out.ProtectionState = "unknown"
	views, err := r.controller.ListContext(ctx, user)
	if err != nil {
		return Status{}, fmt.Errorf("automation: read status runs: %w", err)
	}
	for _, v := range views {
		if v.AccountKey != accountKey {
			continue
		}
		if v.ProtectionState != "" {
			out.ProtectionState = v.ProtectionState
		}
		rec, err := r.risks.Reservation(ctx, v.ID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return Status{}, fmt.Errorf("automation: read status reservation: %w", err)
		}
		if err == nil && rec.State != risk.StateReleased {
			out.ReservedRisk = rec.Risk
		}
	}
	return out, nil
}

func (r *Runtime) lastWS(accountKey string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastEvent[accountKey]
}

func (r *Runtime) noteEvent(accountKey string, at time.Time) {
	r.mu.Lock()
	r.lastEvent[accountKey] = at
	r.mu.Unlock()
}
