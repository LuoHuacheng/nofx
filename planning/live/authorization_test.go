package live

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// policyPlan compiles a plan whose risk budget fits the fixture policy's
// per-trade limit (50 bps of 10000), so automatic authorization can verify it.
func policyPlan(t *testing.T, m *testMarket, target string) core.Plan {
	t.Helper()
	idea := core.Idea{Name: "policy plan", Side: "long", Entry: "100000", Stop: "99000", Target: core.Decimal(target)}
	p, e := core.Compile(m.book, core.Constraints{Equity: "10000", MaxRisk: "50", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}, idea, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func addDraft(t *testing.T, c *Controller, p core.Plan) {
	t.Helper()
	ds := planning.DraftStore{DB: c.DB, UserID: "alice", ExchangeID: "exchange", Venue: "binance"}
	if e := ds.Add([]core.Plan{p}); e != nil {
		t.Fatal(e)
	}
}

func policySetup(t *testing.T) (*Controller, *testBroker, *testMarket, core.Plan, *risk.Store) {
	t.Helper()
	c, b, m, _, store := riskSetup(t, "limit")
	p := policyPlan(t, m, "104000")
	addDraft(t, c, p)
	// The shared fixture stores a disabled policy; automation needs it enabled.
	pol := policyOf(t, store)
	pol.Enabled = true
	version, e := risk.PolicyVersion(pol)
	if e != nil {
		t.Fatal(e)
	}
	pol.Version = version
	if _, e = store.PutPolicy(context.Background(), pol); e != nil {
		t.Fatal(e)
	}
	return c, b, m, p, store
}

func policyOf(t *testing.T, store *risk.Store) risk.Policy {
	t.Helper()
	pol, e := store.Policy(context.Background(), "alice", "account")
	if e != nil {
		t.Fatal(e)
	}
	return pol
}

func authorize(t *testing.T, c *Controller, p core.Plan, version, signal string) View {
	t.Helper()
	v, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, version, signal)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func entryPOSTs(b *testBroker) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, o := range b.placed {
		if o.Role == "entry" {
			n++
		}
	}
	return n
}

func reservationOrEmpty(t *testing.T, store *risk.Store, runID string) string {
	t.Helper()
	r, e := store.Reservation(context.Background(), runID)
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return ""
	}
	if e != nil {
		t.Fatal(e)
	}
	return r.State
}

func TestPolicyAuthorizationBinding(t *testing.T) {
	t.Run("owner", func(t *testing.T) {
		c, _, _, p, store := policySetup(t)
		if _, e := c.AuthorizePolicy(context.Background(), "bob", p.ID, p.Hash, policyOf(t, store).Version, "signal-1"); e == nil {
			t.Fatal("another owner authorized the plan")
		}
	})

	t.Run("account", func(t *testing.T) {
		c, _, _, p, store := policySetup(t)
		// Production resolves the account key from the binding; an account the
		// binding does not own has no policy, so authorization must fail.
		original := c.Factory
		c.Factory = func(ctx context.Context, user, exchange string) (Broker, string, error) {
			b, _, e := original(ctx, user, exchange)
			return b, "other-account", e
		}
		if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, policyOf(t, store).Version, "signal-1"); e == nil {
			t.Fatal("a run was authorized for an account the binding does not own")
		}
	})

	t.Run("environment", func(t *testing.T) {
		c, _, _, p, store := policySetup(t)
		if e := c.DB.Model(&execution.Binding{}).Where("id = ?", "account").Update("environment", "sandbox").Error; e != nil {
			t.Fatal(e)
		}
		if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, policyOf(t, store).Version, "signal-1"); e == nil {
			t.Fatal("a live policy authorized a sandbox account")
		}
	})

	t.Run("plan-hash", func(t *testing.T) {
		c, _, _, p, _ := policySetup(t)
		if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, "wrong", "v", "signal-1"); e == nil {
			t.Fatal("a mismatched plan hash was authorized")
		}
	})

	t.Run("policy-version", func(t *testing.T) {
		c, _, _, p, _ := policySetup(t)
		if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, "stale-version", "signal-1"); e == nil {
			t.Fatal("a stale policy version was authorized")
		}
	})

	t.Run("signal", func(t *testing.T) {
		c, _, _, p, store := policySetup(t)
		if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, policyOf(t, store).Version, ""); e == nil {
			t.Fatal("an authorization without a signal was accepted")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		c, _, _, p, store := policySetup(t)
		pol := policyOf(t, store)
		pol.Enabled = false
		var e error
		if pol.Version, e = risk.PolicyVersion(pol); e != nil {
			t.Fatal(e)
		}
		if _, e = store.PutPolicy(context.Background(), pol); e != nil {
			t.Fatal(e)
		}
		if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, pol.Version, "signal-1"); e == nil {
			t.Fatal("a disabled policy authorized an automatic run")
		}
	})
}

func TestPolicyAuthorizationIdempotent(t *testing.T) {
	t.Run("same-signal", func(t *testing.T) {
		c, _, m, p, store := policySetup(t)
		version := policyOf(t, store).Version
		first := authorize(t, c, p, version, "signal-1")
		repeated := authorize(t, c, p, version, "signal-1")
		if first.ID != repeated.ID {
			t.Fatal("same signal created multiple runs")
		}
		other := policyPlan(t, m, "105000")
		addDraft(t, c, other)
		if _, e := c.AuthorizePolicy(context.Background(), "alice", other.ID, other.Hash, version, "signal-1"); e == nil {
			t.Fatal("a second plan was authorized for the same signal")
		}
		var count int64
		if e := c.DB.Model(&Run{}).Count(&count).Error; e != nil {
			t.Fatal(e)
		}
		if count != 1 {
			t.Fatalf("%d runs persisted, want 1", count)
		}
	})

	t.Run("index", func(t *testing.T) {
		c, _, _, _, _ := policySetup(t)
		row := func(id string) Run {
			return Run{ID: id, UserID: "alice", ExchangeID: "exchange", AccountKey: "account", PlanID: "plan", PlanHash: "hash", ApprovalHash: "digest", State: "approved", PlanJSON: "{}", OrdersJSON: "[]"}
		}
		first, second := row("manual-1"), row("manual-2")
		if e := c.DB.Create(&first).Error; e != nil {
			t.Fatal(e)
		}
		if e := c.DB.Create(&second).Error; e != nil {
			t.Fatalf("manual runs with an empty signal collided: %v", e)
		}
		auto := func(id string) Run {
			r := row(id)
			r.AuthorizationMode, r.SignalID = "policy", "signal-1"
			return r
		}
		first, second = auto("policy-1"), auto("policy-2")
		if e := c.DB.Create(&first).Error; e != nil {
			t.Fatal(e)
		}
		if e := c.DB.Create(&second).Error; e == nil {
			t.Fatal("the same signal authorized two policy runs")
		}
	})
}

func TestPolicyAuthorizationRevokedBeforeSubmit(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		c, b, _, p, store := policySetup(t)
		pol := policyOf(t, store)
		v := authorize(t, c, p, pol.Version, "signal-1")
		pol.Enabled = false
		var e error
		if pol.Version, e = risk.PolicyVersion(pol); e != nil {
			t.Fatal(e)
		}
		if _, e = store.PutPolicy(context.Background(), pol); e != nil {
			t.Fatal(e)
		}
		if _, e = c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); e == nil {
			t.Fatal("a disabled policy started the run")
		}
		revokedPOSTs := entryPOSTs(b)
		if revokedPOSTs != 0 {
			t.Fatal("revoked policy submitted entry")
		}
		if state := reservationOrEmpty(t, store, v.ID); state != risk.StateReleased && state != "" {
			t.Fatalf("a revoked authorization left the hold in %q", state)
		}
	})

	t.Run("version", func(t *testing.T) {
		c, b, _, p, store := policySetup(t)
		pol := policyOf(t, store)
		v := authorize(t, c, p, pol.Version, "signal-1")
		pol.PerTradeRiskBPS = 40
		var e error
		if pol.Version, e = risk.PolicyVersion(pol); e != nil {
			t.Fatal(e)
		}
		if _, e = store.PutPolicy(context.Background(), pol); e != nil {
			t.Fatal(e)
		}
		if _, e = c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); e == nil {
			t.Fatal("a re-authorised policy started the old run")
		} else if !strings.Contains(e.Error(), "policy version changed") {
			t.Fatalf("the run stopped for an unexpected reason: %v", e)
		}
		revokedPOSTs := entryPOSTs(b)
		if revokedPOSTs != 0 {
			t.Fatal("revoked policy submitted entry")
		}
		if state := reservationOrEmpty(t, store, v.ID); state != risk.StateReleased && state != "" {
			t.Fatalf("a revoked authorization left the hold in %q", state)
		}
	})
}

func TestPolicyAuthorizationManualCompatibility(t *testing.T) {
	c, _, _, p, _ := riskSetup(t, "limit")
	if _, e := c.Approve(context.Background(), "alice", p.ID, p.Hash, "approve live"); e == nil {
		t.Fatal("manual approval accepted a phrase other than APPROVE LIVE")
	}
	v := approved(t, c, p)
	if v.AuthorizationMode != "" || v.ApprovalHash != digest(p, "alice", "exchange", "account") {
		t.Fatal("manual approval no longer uses the historical digest")
	}
	if _, e := c.Get("alice", v.ID); e != nil {
		t.Fatalf("a historical manual approval no longer decodes: %v", e)
	}
	// The persisted mode, not the caller, selects the digest a run must match.
	if e := c.DB.Model(&Run{}).Where("id = ?", v.ID).Updates(map[string]any{"authorization_mode": "policy", "signal_id": "signal-1"}).Error; e != nil {
		t.Fatal(e)
	}
	if _, e := c.Get("alice", v.ID); e == nil {
		t.Fatal("the manual digest verified a policy authorization")
	}
	hash := policyDigest(v.Plan, "alice", "exchange", "account", v.PolicyVersion, "signal-1")
	if e := c.DB.Model(&Run{}).Where("id = ?", v.ID).Update("approval_hash", hash).Error; e != nil {
		t.Fatal(e)
	}
	if _, e := c.Get("alice", v.ID); e != nil {
		t.Fatalf("a policy authorization did not verify with its policy digest: %v", e)
	}
}

// putPolicy applies a change to the stored policy and re-derives its version,
// which is what a caller must present to authorize against the new content.
func putPolicy(t *testing.T, store *risk.Store, change func(*risk.Policy)) risk.Policy {
	t.Helper()
	pol := policyOf(t, store)
	change(&pol)
	var e error
	if pol.Version, e = risk.PolicyVersion(pol); e != nil {
		t.Fatal(e)
	}
	if _, e = store.PutPolicy(context.Background(), pol); e != nil {
		t.Fatal(e)
	}
	return pol
}

func enablePolicy(t *testing.T, store *risk.Store) risk.Policy {
	t.Helper()
	return putPolicy(t, store, func(p *risk.Policy) { p.Enabled = true })
}

// policyPlanWith compiles a policy-shaped plan for the given execution kind.
func policyPlanWith(t *testing.T, m *testMarket, exec *core.Execution, leverage int) core.Plan {
	t.Helper()
	idea := core.Idea{Name: "policy plan", Side: "long", Entry: "100000", Stop: "99000", Target: "104000", Execution: exec}
	p, e := core.Compile(m.book, core.Constraints{Equity: "10000", MaxRisk: "50", MaxNotional: "1000", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10", Leverage: leverage}, idea, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	return p
}

// TestPolicyAuthorizationGateRejections pins each rejection of the approval
// gate: without these, a gate that never rejects would pass every other test.
func TestPolicyAuthorizationGateRejections(t *testing.T) {
	cases := []struct {
		name   string
		venue  string
		plan   func(t *testing.T, m *testMarket) core.Plan
		change func(*risk.Policy)
		want   string
	}{
		{name: "venue", venue: "okx", plan: func(t *testing.T, m *testMarket) core.Plan { return policyPlan(t, m, "104000") }, change: func(*risk.Policy) {}, want: "policy venue differs from the trading account"},
		{name: "whitelist", venue: "binance", plan: func(t *testing.T, m *testMarket) core.Plan { return policyPlan(t, m, "104000") }, change: func(p *risk.Policy) { p.Symbols = []string{"ETHUSDT"} }, want: "outside the policy whitelist"},
		{name: "budget", venue: "binance", plan: func(t *testing.T, m *testMarket) core.Plan { return policyPlan(t, m, "104000") }, change: func(p *risk.Policy) { p.PerTradeRiskBPS = 1 }, want: "exceeds the policy per-trade risk"},
		{name: "leverage", venue: "binance", plan: func(t *testing.T, m *testMarket) core.Plan { return policyPlanWith(t, m, nil, 3) }, change: func(p *risk.Policy) { p.MaxLeverage = 1 }, want: "plan leverage exceeds the policy maximum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, b, m, _, store := riskSetupVenue(t, "limit", tc.venue)
			p := tc.plan(t, m)
			addDraft(t, c, p)
			pol := putPolicy(t, store, func(q *risk.Policy) {
				q.Enabled = true
				tc.change(q)
			})
			if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, pol.Version, "signal-1"); e == nil {
				t.Fatal("a plan the policy does not allow was authorized")
			} else if !strings.Contains(e.Error(), tc.want) {
				t.Fatalf("gate rejected for an unexpected reason: %v", e)
			}
			var runs int64
			if e := c.DB.Model(&Run{}).Count(&runs).Error; e != nil {
				t.Fatal(e)
			}
			if runs != 0 || entryPOSTs(b) != 0 {
				t.Fatal("a rejected authorization left a run or an order")
			}
		})
	}

	t.Run("signal-length", func(t *testing.T) {
		c, b, _, p, store := policySetup(t)
		pol := enablePolicy(t, store)
		long := strings.Repeat("s", 129)
		if _, e := c.AuthorizePolicy(context.Background(), "alice", p.ID, p.Hash, pol.Version, long); e == nil {
			t.Fatal("an oversized signal id was authorized")
		} else if !strings.Contains(e.Error(), "at most 128 bytes") {
			t.Fatalf("gate rejected for an unexpected reason: %v", e)
		}
		var runs int64
		if e := c.DB.Model(&Run{}).Count(&runs).Error; e != nil {
			t.Fatal(e)
		}
		if runs != 0 || entryPOSTs(b) != 0 {
			t.Fatal("an oversized signal left a run or an order")
		}
	})
}

// TestPolicyDigestsArePinned compares against values computed outside this
// package, so a drift in the digest inputs fails here instead of agreeing with
// itself. The manual value is the historical approval digest; the 1x variant
// proves leverage still occupies its own slot.
func TestPolicyDigestsArePinned(t *testing.T) {
	p := core.Plan{Hash: strings.Repeat("0", 64), Constraints: core.Constraints{Leverage: 3}}
	if got, want := digest(p, "alice", "exchange", "account"), "87484724a99b7c1ca21eab6b42665707ec9083be4080aedaf76e62ac913d7415"; got != want {
		t.Fatalf("manual approval digest changed: %s", got)
	}
	if got, want := policyDigest(p, "alice", "exchange", "account", "policy-version", "signal-1"), "a9aa22df0fc2de8d3f668e8f92a1c7a75edbdb6f79e74797412f4b29755c70b9"; got != want {
		t.Fatalf("policy authorization digest changed: %s", got)
	}
	p.Constraints.Leverage = 1
	if got, want := digest(p, "alice", "exchange", "account"), "1a4a82ecf48fa41ca985eb42f3afc58fb510269eaf899fac433eaa5acf5517c1"; got != want {
		t.Fatalf("digest no longer binds the effective leverage: %s", got)
	}
}

// TestPolicyAuthorizationStartsAndSubmits drives the accepted path end to end:
// the gate must let a permitted plan through and the tick must submit it.
func TestPolicyAuthorizationStartsAndSubmits(t *testing.T) {
	c, b, _, p, store := policySetup(t)
	pol := enablePolicy(t, store)
	v := authorize(t, c, p, pol.Version, "signal-1")
	if _, e := c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); e != nil {
		t.Fatalf("a permitted policy run was refused: %v", e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for entryPOSTs(b) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if entryPOSTs(b) != 1 {
		t.Fatalf("%d entries reached the venue, want 1", entryPOSTs(b))
	}
	got, e := c.Get("alice", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.State != "running" || got.AuthorizationMode != "policy" {
		t.Fatalf("the authorized run is %q/%q", got.State, got.AuthorizationMode)
	}
	if state := reservationOrEmpty(t, store, v.ID); state != risk.StateReserved && state != risk.StateExposed {
		t.Fatalf("the permitted run holds %q", state)
	}
}

// TestPolicyRevocationStopsLaterEntries proves the per-entry check in step, not
// only the one at start: a TWAP whose policy is revoked between slices must not
// release the second slice.
func TestPolicyRevocationStopsLaterEntries(t *testing.T) {
	c, b, m, _, store := riskSetup(t, "limit")
	twap := core.Execution{Kind: "twap", Slices: 2, IntervalSeconds: 30}
	p := policyPlanWith(t, m, &twap, 0)
	addDraft(t, c, p)
	pol := enablePolicy(t, store)
	v := authorize(t, c, p, pol.Version, "signal-1")
	if len(v.Orders) != 0 {
		t.Fatal("authorization already created orders")
	}
	// Start the run by hand: the first slice is due, the second is 30s out.
	now := time.Now().UTC()
	deadline := now.Add(time.Hour)
	v.StartedAt, v.Deadline, v.State = &now, &deadline, "running"
	for _, s := range p.Orders {
		v.Orders = append(v.Orders, Order{Type: entryOrderType(p), ClientID: cid(v.ID, s.Index, "entry", ""), Role: "entry", Symbol: p.Snapshot.Symbol, Side: p.Idea.Side, Price: s.Price, Quantity: s.Quantity, Status: "scheduled", Filled: "0"})
	}
	if e := c.save(&v); e != nil {
		t.Fatal(e)
	}
	c.step(context.Background(), "alice", v.ID)
	if entryPOSTs(b) != 1 {
		t.Fatalf("%d entries submitted, want the first slice only", entryPOSTs(b))
	}
	putPolicy(t, store, func(q *risk.Policy) { q.Enabled = false })
	// Bring the second slice inside its release window (expiry is 10s).
	past := time.Now().UTC().Add(-35 * time.Second)
	v.StartedAt = &past
	if e := c.save(&v); e != nil {
		t.Fatal(e)
	}
	c.step(context.Background(), "alice", v.ID)
	if entryPOSTs(b) != 1 {
		t.Fatalf("a revoked policy released entry %d", entryPOSTs(b))
	}
	got, e := c.Get("alice", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.State != "needs_attention" || !strings.Contains(got.Error, "Automation policy revoked") {
		t.Fatalf("the tick did not stop the run on the revoked policy: %q %q", got.State, got.Error)
	}
}

// TestPolicyReplayDoesNotBlockOnARunningStep pins the ordering the reviewer
// required: a replayed signal resolves to the run it already authorized before
// any exchange read or run lock. The first run's lock is held here to stand in
// for a tick that is still stepping.
func TestPolicyReplayDoesNotBlockOnARunningStep(t *testing.T) {
	c, _, _, p, store := policySetup(t)
	pol := enablePolicy(t, store)
	first := authorize(t, c, p, pol.Version, "signal-1")
	lock := c.runLock(first.ID)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	repeated, e := c.AuthorizePolicy(ctx, "alice", p.ID, p.Hash, pol.Version, "signal-1")
	if e != nil {
		t.Fatalf("a replay waited on the run lock: %v", e)
	}
	if repeated.ID != first.ID {
		t.Fatal("a replay created a second run")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("a replay took %s, so it waited on the run lock", elapsed)
	}
}
