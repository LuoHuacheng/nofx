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
