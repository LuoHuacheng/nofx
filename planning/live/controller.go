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

	"github.com/google/uuid"
	"gorm.io/gorm"
	"nofx/planning"
	"nofx/planning/core"
)

type Market interface {
	Snapshot(context.Context, string, string) (core.Snapshot, error)
}
type Controller struct {
	DB       *gorm.DB
	Factory  Factory
	Market   Market
	mu       sync.Mutex
	workers  map[string]context.CancelFunc
	stopped  bool
	runLocks sync.Map
	workerWG sync.WaitGroup
}

func New(db *gorm.DB, f Factory, m Market) (*Controller, error) {
	if e := db.AutoMigrate(&planning.Draft{}, &Run{}, &Lease{}); e != nil {
		return nil, e
	}
	c := &Controller{DB: db, Factory: f, Market: m, workers: map[string]context.CancelFunc{}}
	// Restart never silently releases overdue TWAP slices or repeats submissions.
	if e := db.Model(&Run{}).Where("state IN ?", []string{"starting", "running", "stopping", "position_open"}).Updates(map[string]any{"state": "needs_attention", "error": "Service restarted: reconcile submitted orders only; no automatic replay or new entries", "updated_at": time.Now().UTC()}).Error; e != nil {
		return nil, e
	}
	return c, nil
}
func digest(p core.Plan, user, exchange, account string) string {
	raw, _ := json.Marshal([]any{p.Hash, user, exchange, account, fmt.Sprintf("LIVE/GTC/%dx/fixed-limit/partial-fill-reduce-only-protection/no-strategy", p.Constraints.EffectiveLeverage())})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func decode(r Run) (View, error) {
	var p core.Plan
	var orders []Order
	if json.Unmarshal([]byte(r.PlanJSON), &p) != nil || json.Unmarshal([]byte(r.OrdersJSON), &orders) != nil {
		return View{}, errors.New("invalid persisted live plan")
	}
	if p.Hash != r.PlanHash || p.Digest() != p.Hash || digest(p, r.UserID, r.ExchangeID, r.AccountKey) != r.ApprovalHash {
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
	return c.ApproveReplacing(ctx, user, planID, hash, phrase, "", "")
}

// Replacement is bound to the exact unused approval shown during the user's
// new order review. Concurrent or started trades cannot be replaced.
func (c *Controller) ApproveReplacing(ctx context.Context, user, planID, hash, phrase, replaceID, replaceHash string) (View, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if phrase != "APPROVE LIVE" {
		return View{}, errors.New("Type APPROVE LIVE to approve live trading")
	}
	var d planning.Draft
	if e := c.DB.Where("user_id = ? AND id = ?", user, planID).First(&d).Error; e != nil {
		return View{}, errors.New("plan not found or not owned")
	}
	var p core.Plan
	if json.Unmarshal([]byte(d.Body), &p) != nil || p.Hash != hash {
		return View{}, errors.New("full plan hash required")
	}
	if e := p.Verify(time.Now().UTC()); e != nil {
		return View{}, e
	}
	if p.Constraints.PlanningOnly {
		return View{}, errors.New(EquityPreviewReviewMessage)
	}
	broker, account, e := c.Factory(ctx, user, d.ExchangeID)
	if e != nil {
		return View{}, e
	}
	if broker.Venue() != p.Snapshot.Venue {
		return View{}, errors.New("Plan venue differs from the trading account. Generate a new plan.")
	}
	if account == "" {
		return View{}, errors.New("account identity unavailable")
	}
	// Resolve a previously closed trade using exchange reads before acquiring a
	// new lease. The transaction below still arbitrates concurrent approvals.
	var previousLease Lease
	if err := c.DB.Where("account_key = ?", account).First(&previousLease).Error; err == nil {
		if previousLease.UserID == user {
			if _, err = c.CheckStatus(ctx, user, previousLease.RunID); err != nil {
				return View{}, err
			}
			lock := c.runLock(previousLease.RunID)
			if err = lock.LockContext(ctx); err != nil {
				return View{}, err
			}
			defer lock.Unlock()
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, err
	}
	now := time.Now().UTC()
	r := Run{ID: uuid.NewString(), UserID: user, ExchangeID: d.ExchangeID, AccountKey: account, PlanID: p.ID, PlanHash: p.Hash, ApprovalHash: digest(p, user, d.ExchangeID, account), State: "approved", PlanJSON: d.Body, OrdersJSON: "[]", ApprovedAt: now, UpdatedAt: now}
	c.mu.Lock()
	defer c.mu.Unlock()
	e = c.DB.Transaction(func(tx *gorm.DB) error {
		var lease Lease
		if err := tx.Where("account_key = ?", account).First(&lease).Error; err == nil {
			var previous Run
			if lease.RunID != previousLease.RunID || lease.UserID != user || tx.Where("id = ? AND user_id = ?", lease.RunID, user).First(&previous).Error != nil {
				return errors.New("Account approval changed. Refresh and review the order again.")
			}
			v, er := decode(previous)
			if er != nil {
				return er
			}
			if v.PlanID == p.ID || v.State != "approved" || v.ID != replaceID || v.ApprovalHash != replaceHash {
				return errors.New("An earlier trade is reserving this account. Review the current trade before confirming a new order.")
			}
			if er = retireUnusedApproval(tx, v, "cancelled", "Unused approval replaced by a newly confirmed order; no exchange order was submitted."); er != nil {
				return er
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		res := tx.Model(&planning.Draft{}).Where("user_id = ? AND id = ? AND status = ?", user, p.ID, "draft").Update("status", "approved")
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("only an unchanged draft can be approved")
		}
		if er := tx.Create(&Lease{AccountKey: account, RunID: r.ID, UserID: user, ExchangeID: d.ExchangeID}).Error; er != nil {
			return errors.New("An earlier trade is reserving this account. Open Positions & orders to check its status or cancel its unused approval.")
		}
		return tx.Create(&r).Error
	})
	if e != nil {
		return View{}, e
	}
	return decode(r)
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
func (c *Controller) save(v *View) error {
	raw, e := json.Marshal(v.Orders)
	if e != nil {
		return e
	}
	v.OrdersJSON = string(raw)
	v.UpdatedAt = time.Now().UTC()
	return c.DB.Model(&Run{}).Where("id = ? AND user_id = ?", v.ID, v.UserID).Updates(map[string]any{"state": v.State, "orders_json": v.OrdersJSON, "started_at": v.StartedAt, "deadline": v.Deadline, "updated_at": v.UpdatedAt, "error": v.Error}).Error
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
	if e = c.save(&v); e != nil {
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
	if v.State == "completed" || v.State == "cancelled" || v.State == "approved" {
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
	attention := func(message string) {
		observationsValid = false
		v.State = "needs_attention"
		v.Error = message
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
			attention("Exchange order state is unconfirmed; no resubmission:" + er.Error())
			continue
		}
		if !validReceipt(*o, result) || (o.OrderID != "" && o.OrderID != result.OrderID) || cmp(result.Filled, o.Filled) < 0 {
			receiptsValid = false
			attention("Inconsistent exchange receipt; further entries stopped")
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
	account, accountErr := broker.Account(ctx, v.Plan.Snapshot.Symbol)
	positionValid := accountErr == nil
	if _, err := decimal(account.Position); err != nil {
		positionValid = false
	}
	if !positionValid {
		attention("Live position read failed. New entries and protection submission stopped; reconcile positions immediately.")
	}
	entries := len(v.Plan.Orders)
	if entries == 0 {
		entries = 1
	}
	expected := filledExposure(v.Orders)
	if cmp(expected, "0") < 0 {
		positionValid = false
		attention("Exit fills exceed confirmed entry fills; reconcile live positions.")
	}
	positionMatches := positionValid && cmp(account.Position, expected) == 0
	if positionValid && !positionMatches {
		attention("Live position differs from this plan fills. Further entries stopped; check external trades and protective orders.")
	}
	for i := 0; i < entries; i++ {
		entry := v.Orders[i]
		if !positionMatches || cmp(account.Position, "0") == 0 || cmp(entry.Filled, "0") <= 0 {
			continue
		}
		for _, role := range []string{"sl", "tp"} {
			protected := core.Decimal("0")
			uncertain := false
			for _, o := range v.Orders {
				if o.Role != role || o.ParentID != entry.ClientID {
					continue
				}
				if o.OrderID != "" && (o.Status == "NEW" || o.Status == "PARTIALLY_FILLED" || o.Status == "FILLED" || o.Status == "TRIGGERED") {
					protected = add(protected, o.Quantity)
				} else {
					uncertain = true
				}
			}
			if uncertain {
				attention("Protective order state is abnormal. Further entries stopped; reconcile live positions manually.")
				continue
			}
			if cmp(protected, entry.Filled) >= 0 {
				continue
			}
			if len(v.Orders) >= 1024 {
				attention("Partial-fill count exceeds the handling limit; manage protection manually")
				break
			}
			delta := sub(entry.Filled, protected)
			client := cid(v.ID, i+1, role, string(entry.Filled))
			exists := false
			for _, o := range v.Orders {
				if o.ClientID == client {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			price := v.Plan.Idea.Stop
			if role == "tp" {
				price = v.Plan.Idea.Target
			}
			o := Order{ClientID: client, Role: role, Symbol: entry.Symbol, Side: entry.Side, Price: price, Quantity: delta, Status: "submitting", Filled: "0", ParentID: entry.ClientID}
			o, er := routedOrder(broker, o)
			if er != nil {
				attention("Protective order route is unconfirmed; further entries stopped")
				break
			}
			v.Orders = append(v.Orders, o)
			if e = c.save(&v); e != nil {
				return true
			}
			receipt, er := broker.Place(ctx, o)
			if er == nil && !validReceipt(o, receipt) {
				er = errors.New("invalid protection acknowledgement")
			}
			if er != nil {
				v.Orders[len(v.Orders)-1].Status = "UNKNOWN"
				attention("Protection is unconfirmed. New entries stopped; reconcile positions and protective orders in NOFX immediately. " + er.Error())
				_ = c.save(&v)
				break
			}
			receipt.ParentID = entry.ClientID
			v.Orders[len(v.Orders)-1] = receipt
			if receipt.Status == "TRIGGERED" || cmp(receipt.Filled, "0") > 0 {
				v.State = "stopping"
			}
			if e = c.save(&v); e != nil {
				return true
			}
		}
	}
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
	if v.State == "running" {
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
	expected = filledExposure(v.Orders)
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
	for _, o := range v.Orders[len(specs):] {
		entry, ok := entries[o.ParentID]
		price := v.Plan.Idea.Stop
		if o.Role == "tp" {
			price = v.Plan.Idea.Target
		}
		if o.Type != "" || o.PostOnly || o.ReduceOnly || !ok || (o.Role != "sl" && o.Role != "tp") || !core.Positive(o.Quantity) || cmp(o.Quantity, entry.Quantity) > 0 || cmp(o.Price, price) != 0 || o.Side != entry.Side || o.Symbol != entry.Symbol || seen[o.ClientID] {
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
