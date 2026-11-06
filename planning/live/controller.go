package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sync"
	"time"

	"gorm.io/gorm"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

type Market interface {
	Snapshot(context.Context, string, string) (core.Snapshot, error)
}
type Controller struct {
	DB       *gorm.DB
	Factory  Factory
	Market   Market
	Risk     *risk.Store      // nil keeps the manual, unreserved behaviour
	Now      func() time.Time // nil means UTC wall clock; tests inject it
	mu       sync.Mutex
	workers  map[string]context.CancelFunc
	stopped  bool
	runLocks sync.Map
	workerWG sync.WaitGroup
}

func New(db *gorm.DB, f Factory, m Market) (*Controller, error) {
	if e := db.AutoMigrate(&planning.Draft{}, &Run{}, &Lease{}, &execution.Binding{}); e != nil {
		return nil, e
	}
	c := &Controller{DB: db, Factory: f, Market: m, workers: map[string]context.CancelFunc{}}
	// Restart never silently releases overdue TWAP slices or repeats submissions.
	if e := db.Model(&Run{}).Where("state IN ?", []string{"starting", "running", "stopping", "position_open"}).Updates(map[string]any{"state": "needs_attention", "error": "Service restarted: reconcile submitted orders only; no automatic replay or new entries", "updated_at": time.Now().UTC()}).Error; e != nil {
		return nil, e
	}
	return c, nil
}

// liveExecutionText pins the execution semantics a manual approval covers.
const liveExecutionText = "LIVE/GTC/%dx/fixed-limit/partial-fill-reduce-only-protection/no-strategy"

func digest(p core.Plan, user, exchange, account string) string {
	raw, _ := json.Marshal([]any{p.Hash, user, exchange, account, fmt.Sprintf(liveExecutionText, p.Constraints.EffectiveLeverage())})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func decode(r Run) (View, error) {
	var p core.Plan
	var orders []Order
	if json.Unmarshal([]byte(r.PlanJSON), &p) != nil || json.Unmarshal([]byte(r.OrdersJSON), &orders) != nil {
		return View{}, errors.New("invalid persisted live plan")
	}
	if p.Hash != r.PlanHash || p.Digest() != p.Hash || r.approvalDigest(p) != r.ApprovalHash {
		return View{}, errors.New("persisted approval digest mismatch")
	}
	view := View{Run: r, Plan: p, Orders: orders}
	if e := validateIntent(view); e != nil {
		return View{}, e
	}
	return view, nil
}
func (c *Controller) Get(user, id string) (View, error) {
	var r Run
	e := c.DB.Where("user_id = ? AND id = ?", user, id).First(&r).Error
	if e != nil {
		return View{}, errors.New("execution not found or not owned")
	}
	return decode(r)
}
func (c *Controller) List(user string) ([]View, error) {
	return c.ListContext(context.Background(), user)
}

func (c *Controller) ListContext(ctx context.Context, user string) ([]View, error) {
	var rows []Run
	if e := c.DB.Where("user_id = ?", user).Order("approved_at desc").Limit(100).Find(&rows).Error; e != nil {
		return nil, e
	}
	out := []View{}
	for _, r := range rows {
		v, e := decode(r)
		if e != nil {
			return nil, e
		}
		if v.State == "approved" && !time.Now().Before(v.Plan.Expires) {
			v, e = c.CheckStatus(ctx, user, v.ID)
			if e != nil {
				return nil, e
			}
		}
		out = append(out, v)
	}
	return out, nil
}
func (c *Controller) Approve(ctx context.Context, user, planID, hash, phrase string) (View, error) {
	return c.approve(ctx, approvalRequest{User: user, PlanID: planID, Hash: hash, Phrase: phrase})
}

// Replacement is bound to the exact unused approval shown during the user's
// new order review. Concurrent or started trades cannot be replaced.
func (c *Controller) ApproveReplacing(ctx context.Context, user, planID, hash, phrase, replaceID, replaceHash string) (View, error) {
	return c.approve(ctx, approvalRequest{User: user, PlanID: planID, Hash: hash, Phrase: phrase, ReplaceID: replaceID, ReplaceHash: replaceHash})
}
func decimal(d core.Decimal) (*big.Rat, error) {
	if !nativeDecimal.MatchString(string(d)) {
		return nil, errors.New("invalid native decimal")
	}
	n, ok := new(big.Rat).SetString(string(d))
	if !ok || n.Sign() < 0 {
		return nil, errors.New("invalid native decimal")
	}
	return n, nil
}

func cmp(a, b core.Decimal) int {
	x, e := decimal(a)
	if e != nil {
		return -999
	}
	y, e := decimal(b)
	if e != nil {
		return -999
	}
	return x.Cmp(y)
}
func add(a, b core.Decimal) core.Decimal {
	x, _ := decimal(a)
	y, _ := decimal(b)
	return core.Decimal(new(big.Rat).Add(x, y).FloatString(12))
}
func sub(a, b core.Decimal) core.Decimal {
	x, _ := decimal(a)
	y, _ := decimal(b)
	return core.Decimal(new(big.Rat).Sub(x, y).FloatString(12))
}
func cid(run string, index int, role, quantity string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s/%s", run, index, role, quantity)))
	return hex.EncodeToString(h[:16])
}

// saveStarted persists the run start and its risk reservation in one
// transaction: no order may be placed before the hold commits.
func (c *Controller) saveStarted(v *View, now time.Time) error {
	raw, err := json.Marshal(v.Orders)
	if err != nil {
		return err
	}
	v.OrdersJSON, v.UpdatedAt = string(raw), now
	return c.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Run{}).Where("id = ? AND user_id = ?", v.ID, v.UserID).Updates(map[string]any{"state": v.State, "orders_json": v.OrdersJSON, "started_at": v.StartedAt, "deadline": v.Deadline, "protection_state": v.ProtectionState, "protection_observed_at": v.ProtectionObservedAt, "updated_at": v.UpdatedAt, "error": v.Error}).Error; err != nil {
			return err
		}
		return c.reserveRisk(tx, v, now)
	})
}

func (c *Controller) save(v *View) error {
	raw, e := json.Marshal(v.Orders)
	if e != nil {
		return e
	}
	v.OrdersJSON = string(raw)
	v.UpdatedAt = time.Now().UTC()
	return c.DB.Model(&Run{}).Where("id = ? AND user_id = ?", v.ID, v.UserID).Updates(map[string]any{"state": v.State, "orders_json": v.OrdersJSON, "started_at": v.StartedAt, "deadline": v.Deadline, "protection_state": v.ProtectionState, "protection_observed_at": v.ProtectionObservedAt, "updated_at": v.UpdatedAt, "error": v.Error}).Error
}

// failStarted records a refusal that happened after the hold committed but
// before any order was submitted. The hold is released in the same
// transaction: nothing was sent, so nothing can still consume budget, and a
// needs_attention run has no path back to running that could release it later.
func (c *Controller) failStarted(v *View) error {
	v.UpdatedAt = time.Now().UTC()
	return c.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Run{}).Where("id = ? AND user_id = ?", v.ID, v.UserID).Updates(map[string]any{"state": v.State, "updated_at": v.UpdatedAt, "error": v.Error}).Error; err != nil {
			return err
		}
		return c.releaseRisk(tx, v)
	})
}

type runMutex struct{ token chan struct{} }

func newRunMutex() *runMutex {
	m := &runMutex{token: make(chan struct{}, 1)}
	m.token <- struct{}{}
	return m
}

func (m *runMutex) Lock()   { <-m.token }
func (m *runMutex) Unlock() { m.token <- struct{}{} }
func (m *runMutex) LockContext(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.New("Trade status is still updating. Try again shortly.")
	}
	select {
	case <-m.token:
		if ctx.Err() != nil {
			m.Unlock()
			return errors.New("Trade status is still updating. Try again shortly.")
		}
		return nil
	case <-ctx.Done():
		return errors.New("Trade status is still updating. Try again shortly.")
	}
}

func (c *Controller) runLock(id string) *runMutex {
	m, _ := c.runLocks.LoadOrStore(id, newRunMutex())
	return m.(*runMutex)
}

func (c *Controller) isStopped() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.stopped }

func (c *Controller) Execute(ctx context.Context, user, id, hash string) (out View, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock := c.runLock(id)
	if err := lock.LockContext(ctx); err != nil {
		return View{}, err
	}
	defer lock.Unlock()
	v, e := c.Get(user, id)
	if e != nil {
		return v, e
	}
	if hash != v.ApprovalHash {
		return v, errors.New("locked approval hash required")
	}
	if v.State != "approved" {
		if v.State == "cancelled" || v.State == "expired" {
			return v, errors.New("This approval is no longer valid. Review the plan again before placing an order.")
		}
		return v, nil
	}
	defer func() {
		if err == nil || v.State != "approved" || v.StartedAt != nil || len(v.Orders) != 0 {
			return
		}
		reason := "Order not submitted: " + err.Error()
		state := "cancelled"
		if !time.Now().Before(v.Plan.Expires) {
			state = "expired"
		}
		if cleanupErr := c.retireUnusedApproval(&v, state, reason); cleanupErr != nil {
			err = fmt.Errorf("%w; could not release the unused approval: %v", err, cleanupErr)
			return
		}
		out, err = v, errors.New(reason+". The unused approval was released; review the plan again.")
	}()
	if c.isStopped() {
		return v, errors.New("server is shutting down")
	}
	if e = v.Plan.Verify(time.Now().UTC()); e != nil {
		return v, e
	}
	b, key, e := c.Factory(ctx, user, v.ExchangeID)
	if e != nil {
		return v, e
	}
	if key != v.AccountKey || b.Venue() != v.Plan.Snapshot.Venue {
		return v, errors.New("Trading account identity changed; generate and approve a new plan")
	}
	account, e := b.Account(ctx, v.Plan.Snapshot.Symbol)
	if e != nil {
		return v, e
	}
	for _, value := range []core.Decimal{account.Equity, account.Available, account.FeeBPS, account.Position} {
		if _, e = decimal(value); e != nil {
			return v, errors.New("invalid real account financial fields")
		}
	}
	if cmp(account.Position, "0") != 0 || account.OpenOrders != 0 {
		return v, errors.New("This symbol already has positions or open orders and cannot be mixed with this independent plan")
	}
	if e = CheckAccountCapacity(v.Plan, account); e != nil {
		return v, e
	}
	if cmp(account.FeeBPS, v.Plan.Constraints.FeeBPS) > 0 {
		return v, errors.New("Actual fees including Builder fees exceed the plan assumption; generate a new plan")
	}
	book, e := c.Market.Snapshot(ctx, v.Plan.Snapshot.Venue, v.Plan.Snapshot.Symbol)
	if e != nil {
		return v, e
	}
	if e = core.CheckBook(v.Plan, book, time.Now().UTC()); e != nil {
		return v, e
	}
	if preparer, ok := b.(interface {
		PrepareLeverage(context.Context, string, int) error
	}); ok {
		e = preparer.PrepareLeverage(ctx, v.Plan.Snapshot.Symbol, v.Plan.Constraints.EffectiveLeverage())
	} else if v.Plan.Constraints.EffectiveLeverage() == 1 {
		e = b.Prepare(ctx, v.Plan.Snapshot.Symbol)
	} else {
		e = errors.New("broker cannot set the locked leverage")
	}
	if e != nil {
		return v, e
	}
	now := time.Now().UTC()
	deadline := now.Add(15 * time.Minute)
	if v.Plan.Idea.Execution != nil && v.Plan.Idea.Execution.Kind == "twap" {
		last := v.Plan.Orders[len(v.Plan.Orders)-1].OffsetSeconds
		deadline = now.Add(time.Duration(last+120) * time.Second)
	}
	v.StartedAt = &now
	v.Deadline = &deadline
	v.State = "running"
	v.Orders = []Order{}
	specs := v.Plan.Orders
	if len(specs) == 0 {
		specs = []core.OrderSpec{{Index: 1, Price: v.Plan.Idea.Entry, Quantity: v.Plan.Quantity}}
	}
	for _, o := range specs {
		intent, err := routedOrder(b, Order{Type: entryOrderType(v.Plan), ClientID: cid(v.ID, o.Index, "entry", ""), Role: "entry", Symbol: v.Plan.Snapshot.Symbol, Side: v.Plan.Idea.Side, Price: o.Price, Quantity: o.Quantity, Status: "scheduled", Filled: "0"})
		if err != nil {
			return v, err
		}
		v.Orders = append(v.Orders, intent)
	}
	started := v
	if e = c.saveStarted(&started, now); e != nil {
		// Nothing was placed: hand the approval back to the deferred retire path.
		v.State, v.StartedAt, v.Deadline, v.Orders = "approved", nil, nil, nil
		return v, e
	}
	v = started
	// An automatic authorization is void once its policy is disabled or
	// revised; the hold is released because nothing was submitted.
	if e = c.checkPolicy(ctx, &v); e != nil {
		if fail := c.failStarted(&v); fail != nil {
			return v, fmt.Errorf("%w; could not record the refusal: %v", e, fail)
		}
		return v, e
	}
	if e = c.checkRisk(ctx, &v); e != nil {
		// The hold is committed but nothing was submitted yet: persist the
		// refusal and release it in one transaction, then report the reason.
		if fail := c.failStarted(&v); fail != nil {
			return v, fmt.Errorf("%w; could not record the refusal: %v", e, fail)
		}
		return v, e
	}
	c.launch(v.ID, user)
	return v, nil
}
func (c *Controller) launch(id, user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if _, ok := c.workers[id]; ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.workers[id] = cancel
	c.workerWG.Add(1)
	go func() {
		defer c.workerWG.Done()
		defer func() { c.mu.Lock(); delete(c.workers, id); c.mu.Unlock() }()
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			done := c.step(ctx, user, id)
			if done {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}
func (c *Controller) Reconcile(user, id string) (View, error) {
	return c.ReconcileContext(context.Background(), user, id)
}

func (c *Controller) ReconcileContext(ctx context.Context, user, id string) (View, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock := c.runLock(id)
	if err := lock.LockContext(ctx); err != nil {
		return View{}, err
	}
	defer lock.Unlock()
	v, e := c.Get(user, id)
	if e != nil {
		return v, e
	}
	if v.State == "needs_attention" || v.State == "position_open" {
		c.launch(id, user)
	}
	return v, nil
}
func (c *Controller) Stop(user, id, hash string) (View, error) {
	return c.StopContext(context.Background(), user, id, hash)
}

func (c *Controller) StopContext(ctx context.Context, user, id, hash string) (View, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock := c.runLock(id)
	if err := lock.LockContext(ctx); err != nil {
		return View{}, err
	}
	defer lock.Unlock()
	v, e := c.Get(user, id)
	if e != nil {
		return v, e
	}
	if hash != v.ApprovalHash {
		return v, errors.New("locked approval hash required")
	}
	if v.State == "approved" || v.State == "expired" {
		v.State = "cancelled"
		e = c.DB.Transaction(func(tx *gorm.DB) error {
			if e := tx.Model(&Run{}).Where("id = ? AND user_id = ?", v.ID, user).Updates(map[string]any{"state": "cancelled", "updated_at": time.Now().UTC()}).Error; e != nil {
				return e
			}
			if e := tx.Where("run_id = ?", v.ID).Delete(&Lease{}).Error; e != nil {
				return e
			}
			// A cancelled approval never submits, so its hold is terminal here.
			if e := c.releaseRisk(tx, &v); e != nil {
				return e
			}
			return tx.Model(&planning.Draft{}).Where("user_id = ? AND id = ?", user, v.PlanID).Update("status", "cancelled").Error
		})
		return v, e
	}
	if v.State == "completed" || v.State == "cancelled" {
		return v, nil
	}
	v.State = "stopping"
	if e = c.save(&v); e != nil {
		return v, e
	}
	c.launch(id, user)
	return v, nil
}
func (c *Controller) Shutdown() {
	c.mu.Lock()
	c.stopped = true
	for _, cancel := range c.workers {
		cancel()
	}
	c.mu.Unlock()
	c.workerWG.Wait()
}

// step serializes durable intent before every external side effect. Unknown
// acknowledgements are reconciled by client ID, never blindly resubmitted.
func (c *Controller) step(ctx context.Context, user, id string) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock := c.runLock(id)
	if err := lock.LockContext(ctx); err != nil {
		return false
	}
	defer lock.Unlock()
	v, e := c.Get(user, id)
	if e != nil {
		return true
	}
	// halted_unknown keeps its lease and stays frozen: only the original exit
	// client ID may resolve it, and no other submission is allowed meanwhile.
	if v.State == "completed" || v.State == "cancelled" || v.State == "approved" || v.State == runHaltedUnknown {
		return true
	}
	if v.StartedAt == nil {
		v.State = "needs_attention"
		v.Error = "Execution has no valid start record; no orders will be submitted"
		_ = c.save(&v)
		return true
	}
	broker, key, e := c.Factory(ctx, user, v.ExchangeID)
	if e != nil || key != v.AccountKey || broker.Venue() != v.Plan.Snapshot.Venue {
		v.State = "needs_attention"
		v.Error = "Account unavailable or identity changed. Further entries stopped; check NOFX account settings."
		_ = c.save(&v)
		return true
	}
	observationsValid, receiptsValid := true, true
	supervised := c.supervised(ctx, v)
	attention := func(message string) {
		observationsValid = false
		v.State = "needs_attention"
		if v.ProtectionState != protectionExitRequired {
			v.Error = message // a frozen exit verdict keeps the reason it was raised
		}
	}
	// The hard breaker is read on every tick before any entry is released: a
	// latched account may still reduce, never add. Flatten runs through the
	// lock-free variant because this tick already holds the run lock.
	decision, er := c.riskDecision(ctx, &v)
	switch {
	case er != nil:
		attention("Risk breaker could not be evaluated; further entries stopped: " + er.Error())
	case decision.Action == risk.ActionFlatten && cmp(filledExposure(v.Orders), "0") > 0:
		_, _ = c.flatten(ctx, v.UserID, v.ID, decision.Reason) // it persists its own state and error
		return true
	case decision.Action != risk.ActionAllow:
		// No exposure to flatten (or only a pause): stop entries and let the
		// rest of this tick cancel them and prove the run flat.
		v.State, v.Error = "needs_attention", decision.Reason
	}
	if v.Deadline != nil && time.Now().After(*v.Deadline) && v.State == "running" {
		v.State = "stopping"
	}
	for i := range v.Orders {
		o := &v.Orders[i]
		if o.Role != "entry" && (cmp(o.Filled, "0") > 0 || o.Status == "TRIGGERED") {
			v.State = "stopping"
		}
		if o.Status == "scheduled" || o.Status == "FILLED" || o.Status == "CANCELED" || o.Status == "REJECTED" || o.Status == "EXPIRED" {
			continue
		}
		result, er := broker.Lookup(ctx, *o)
		if er != nil {
			receiptsValid = false
			// A supervised run judges its protectors through ensureProtection, so
			// an unreadable protection is a deadline instead of an immediate lock.
			if !supervised || o.Role == "entry" {
				attention("Exchange order state is unconfirmed; no resubmission:" + er.Error())
			}
			continue
		}
		if !validReceipt(*o, result) || (o.OrderID != "" && o.OrderID != result.OrderID) || cmp(result.Filled, o.Filled) < 0 {
			receiptsValid = false
			if !supervised || o.Role == "entry" {
				attention("Inconsistent exchange receipt; further entries stopped")
			}
			continue
		}
		*o = result
		if o.Role != "entry" && (cmp(o.Filled, "0") > 0 || o.Status == "TRIGGERED") {
			v.State = "stopping"
		}
	}
	// Persist observed fills before constructing protectors; a restart can safely
	// identify each protector from the exact cumulative-filled quantity.
	if e = c.save(&v); e != nil {
		return true
	}
	entries := len(v.Plan.Orders)
	if entries == 0 {
		entries = 1
	}
	if e = c.ensureProtection(ctx, &v, broker, c.now()); e != nil {
		return true
	}
	// A transient protection fault still blocks recovery into position_open.
	observationsValid = observationsValid && v.ProtectionState == protectionProtected
	if v.State == "stopping" || v.State == "needs_attention" {
		for i := 0; i < entries; i++ {
			o := &v.Orders[i]
			if o.Status == "scheduled" {
				o.Status = "CANCELED"
				continue
			}
			if o.Status == "NEW" || o.Status == "PARTIALLY_FILLED" {
				if er := broker.Cancel(ctx, *o); er != nil {
					attention("Pending order cancellation unconfirmed; reconcile:" + er.Error())
				}
			}
		}
	}
	// A supervised account must confirm its stop from the venue before another
	// entry is released; manual runs keep the original reserve-then-submit path.
	if v.State == "running" && (!supervised || v.ProtectionState == protectionProtected) {
		for i := 0; i < entries; i++ {
			o := &v.Orders[i]
			if o.Status != "scheduled" {
				continue
			}
			offset := 0
			if len(v.Plan.Orders) > 0 {
				offset = v.Plan.Orders[i].OffsetSeconds
			}
			release := v.StartedAt.Add(time.Duration(offset) * time.Second)
			if time.Now().Before(release) {
				continue
			}
			if offset > 0 && time.Since(release) > 10*time.Second {
				o.Status = "EXPIRED"
				continue
			}
			book, er := c.Market.Snapshot(ctx, v.Plan.Snapshot.Venue, v.Plan.Snapshot.Symbol)
			if er != nil {
				attention("Fresh market data unavailable; further entries stopped")
				break
			}
			if er = core.CheckBook(v.Plan, book, time.Now().UTC()); er != nil {
				attention("Market no longer satisfies the locked plan:" + er.Error())
				break
			}
			// Revocation is checked against the venue-side policy right before
			// the order leaves the process, not only when the run started.
			if er = c.checkPolicy(ctx, &v); er != nil {
				attention("Automation policy revoked; further entries stopped: " + er.Error())
				break
			}
			o.Status = "submitting"
			if e = c.save(&v); e != nil {
				return true
			}
			receipt, er := broker.Place(ctx, *o)
			if er == nil && !validReceipt(*o, receipt) {
				er = errors.New("invalid entry acknowledgement")
			}
			if er != nil {
				o.Status = "UNKNOWN"
				attention("Entry receipt unconfirmed; no resubmission:" + er.Error())
				break
			}
			*o = receipt
			if e = c.save(&v); e != nil {
				return true
			}
			if cmp(receipt.Filled, "0") > 0 {
				// Record the exposure before the position leaves the reservation.
				if er = c.exposeRisk(ctx, &v); er != nil {
					attention("Risk exposure could not be recorded; further entries stopped")
				}
				break
			} // protect a new fill before releasing another entry
		}
	}
	account, er := broker.Account(ctx, v.Plan.Snapshot.Symbol)
	if er != nil {
		attention("Live position read failed; further entries stopped")
	}
	if _, err := decimal(account.Position); err != nil {
		er = err
		attention("Invalid venue position field")
	}
	pending := false
	for i := 0; i < entries; i++ {
		s := v.Orders[i].Status
		if s == "scheduled" || s == "NEW" || s == "PARTIALLY_FILLED" || s == "UNKNOWN" || s == "submitting" {
			pending = true
		}
	}
	// A filled entry can precede its account-position update. Recompute after
	// placement and require confirmed exits before releasing the reservation.
	// External closes use the same terminal-order proof as CheckStatus.
	expected := filledExposure(v.Orders)
	if !pending && er == nil && receiptsValid && confirmedClosedTrade(v.Orders, account) {
		return c.complete(&v) == nil
	}
	if er == nil && cmp(account.Position, expected) != 0 {
		attention("Entry fills and the exchange position do not yet match. Further entries stopped; waiting for position confirmation. Check the exchange if this persists.")
	}
	if !pending && er == nil && cmp(account.Position, "0") == 0 && cmp(expected, "0") == 0 {
		for i := entries; i < len(v.Orders); i++ {
			o := v.Orders[i]
			if o.Status == "UNKNOWN" || o.Status == "submitting" || o.Status == "TRIGGERED" {
				er = errors.New("unresolved protection order")
				attention("Protection is not fully reconciled. Account lock retained; verify at the exchange.")
				break
			}
			if o.Status == "NEW" || o.Status == "PARTIALLY_FILLED" {
				if er = broker.Cancel(ctx, o); er != nil {
					attention("Remaining protective order cancellation is unconfirmed")
					break
				}
			}
		}
		if er == nil {
			v.State = "completed"
			v.Error = ""
			e = c.DB.Transaction(func(tx *gorm.DB) error {
				// Keep all durable lifecycle writes on the same transaction.
				raw, err := json.Marshal(v.Orders)
				if err != nil {
					return err
				}
				if err = tx.Model(&Run{}).Where("id = ? AND user_id = ?", v.ID, user).Updates(map[string]any{"state": "completed", "orders_json": string(raw), "error": "", "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
				if err = tx.Where("run_id = ?", v.ID).Delete(&Lease{}).Error; err != nil {
					return err
				}
				// This branch completes the run too, so it owes the same release.
				if err = c.releaseRisk(tx, &v); err != nil {
					return err
				}
				return tx.Model(&planning.Draft{}).Where("user_id = ? AND id = ?", user, v.PlanID).Update("status", "completed").Error
			})
			return true
		}
	}
	if !pending && er == nil && observationsValid && confirmedProtectedPosition(v.Orders, account) {
		v.State = "position_open"
		v.Error = ""
	}
	if e = c.save(&v); e != nil {
		return true
	}
	return false
}

func filledExposure(orders []Order) core.Decimal {
	entry, exit := core.Decimal("0"), core.Decimal("0")
	for _, order := range orders {
		if order.Role == "entry" {
			entry = add(entry, order.Filled)
		} else {
			exit = add(exit, order.Filled)
		}
	}
	return sub(entry, exit)
}

var nativeDecimal = regexp.MustCompile(`^[0-9]{1,24}(\.[0-9]{1,12})?$`)

func validReceipt(request, got Order) bool {
	if got.Type != request.Type || got.PostOnly != request.PostOnly || got.ReduceOnly != request.ReduceOnly || got.VenueClientID != request.VenueClientID || got.ClientID != request.ClientID || got.Role != request.Role || got.ParentID != request.ParentID || got.Symbol != request.Symbol || got.Side != request.Side || cmp(got.Quantity, request.Quantity) != 0 || cmp(got.Price, request.Price) != 0 || got.OrderID == "" {
		return false
	}
	if _, e := decimal(got.Filled); e != nil || cmp(got.Filled, got.Quantity) > 0 {
		return false
	}
	switch got.Status {
	case "NEW":
		return cmp(got.Filled, "0") == 0
	case "PARTIALLY_FILLED":
		return cmp(got.Filled, "0") > 0 && cmp(got.Filled, got.Quantity) < 0
	case "FILLED":
		return cmp(got.Filled, got.Quantity) == 0
	case "CANCELED", "REJECTED", "EXPIRED", "TRIGGERED":
		return true
	}
	return false
}
func entryOrderType(p core.Plan) string {
	if p.Idea.Execution != nil && p.Idea.Execution.Kind == "market" {
		return "market"
	}
	return ""
}
func validateIntent(v View) error {
	if v.StartedAt == nil {
		if len(v.Orders) != 0 {
			return errors.New("orders exist without an explicit execution start")
		}
		return nil
	}
	specs := v.Plan.Orders
	if len(specs) == 0 {
		specs = []core.OrderSpec{{Index: 1, Price: v.Plan.Idea.Entry, Quantity: v.Plan.Quantity}}
	}
	if len(v.Orders) < len(specs) || len(v.Orders) > 1024 || v.Deadline == nil {
		return errors.New("invalid persisted order basket")
	}
	entries := map[string]Order{}
	seen := map[string]bool{}
	for i, s := range specs {
		o := v.Orders[i]
		if o.Type != entryOrderType(v.Plan) || o.PostOnly || o.ReduceOnly || o.ClientID != cid(v.ID, s.Index, "entry", "") || o.Role != "entry" || o.Symbol != v.Plan.Snapshot.Symbol || o.Side != v.Plan.Idea.Side || cmp(o.Price, s.Price) != 0 || cmp(o.Quantity, s.Quantity) != 0 || cmp(o.Filled, o.Quantity) > 0 {
			return errors.New("entry intent no longer matches approval")
		}
		if _, e := decimal(o.Filled); e != nil {
			return e
		}
		entries[o.ClientID] = o
		seen[o.ClientID] = true
	}
	totals := map[string]core.Decimal{}
	basket := core.Decimal("0")
	for _, s := range specs {
		basket = add(basket, s.Quantity)
	}
	generation := 0
	for _, o := range v.Orders[len(specs):] {
		if o.Role == "exit" {
			generation++
			if o.Type != "market" || !o.ReduceOnly || o.PostOnly || o.ParentID != "" || o.Symbol != v.Plan.Snapshot.Symbol || o.Side != v.Plan.Idea.Side || o.ClientID != cid(v.ID, generation, "exit", "") || !core.Positive(o.Quantity) || cmp(o.Quantity, basket) > 0 || seen[o.ClientID] {
				return errors.New("closing intent exceeds the locked position")
			}
			seen[o.ClientID] = true
			if _, e := decimal(o.Filled); e != nil || cmp(o.Filled, o.Quantity) > 0 {
				return errors.New("invalid closing fill")
			}
			continue
		}
		entry, ok := entries[o.ParentID]
		price := v.Plan.Idea.Stop
		if o.Role == "tp" {
			price = v.Plan.Idea.Target
		}
		// A stop may only tighten, so a stop moved closer to the mark still fits
		// the risk the plan was approved for; a target stays at its plan price.
		priceOK := cmp(o.Price, price) == 0
		if o.Role == "sl" {
			priceOK = (entry.Side == "long" && cmp(o.Price, price) >= 0) || (entry.Side != "long" && cmp(o.Price, price) <= 0)
		}
		if o.Type != "" || o.PostOnly || o.ReduceOnly || !ok || (o.Role != "sl" && o.Role != "tp") || !core.Positive(o.Quantity) || cmp(o.Quantity, entry.Quantity) > 0 || !priceOK || o.Side != entry.Side || o.Symbol != entry.Symbol || seen[o.ClientID] {
			return errors.New("protective intent exceeds the locked basket")
		}
		seen[o.ClientID] = true
		if _, e := decimal(o.Filled); e != nil || cmp(o.Filled, o.Quantity) > 0 {
			return errors.New("invalid protective fill")
		}
		key := o.ParentID + o.Role
		if totals[key] == "" {
			totals[key] = "0"
		}
		totals[key] = add(totals[key], o.Quantity)
		if cmp(totals[key], entry.Quantity) > 0 {
			return errors.New("protective quantity exceeds the approved entry")
		}
	}
	return nil
}
