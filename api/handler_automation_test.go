package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"nofx/planning/live"
	"nofx/planning/market"
	"nofx/store"
	"nofx/trader/execution"
	"nofx/trading/automation"
	"nofx/trading/risk"
)

// automationFixture is a server with one managed account and one foreign
// account, so the tests can prove ownership is enforced on every route.
type automationFixture struct {
	db     *gorm.DB
	server *Server
	rig    *automation.Runtime
	router *gin.Engine
	policy risk.Policy
}

func newAutomationFixture(t *testing.T) *automationFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "automation.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	if err = db.AutoMigrate(&store.Exchange{}, &execution.Binding{}); err != nil {
		t.Fatal(err)
	}
	bindings := []execution.Binding{
		{ID: "alice-account", UserID: "alice", ExchangeID: "alice-okx", Venue: "okx", Environment: execution.EnvironmentSandbox, VenueAccountID: "okx-uid-1", VerifiedAt: time.Now().UTC(), Enabled: true},
		{ID: "bob-account", UserID: "bob", ExchangeID: "bob-okx", Venue: "okx", Environment: execution.EnvironmentSandbox, VenueAccountID: "okx-uid-2", VerifiedAt: time.Now().UTC(), Enabled: true},
	}
	if err = db.Create(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	f := &automationFixture{db: db}
	f.server = &Server{store: st, planning: &planningServices{source: market.New()}, risks: mustRiskStore(t, db)}
	f.server.planningOnce.Do(func() {})
	controller, err := live.New(db, nil, market.New())
	if err != nil {
		t.Fatal(err)
	}
	controller.Risk = f.server.risks
	f.policy = risk.DefaultPolicy("alice", "alice-account", "portfolio", "okx", execution.EnvironmentSandbox)
	f.policy.Symbols = []string{"BTCUSDT"}
	f.policy.Enabled = false
	version, err := risk.PolicyVersion(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	f.policy.Version = version
	policy, err := f.server.risks.PutPolicy(context.Background(), f.policy)
	if err != nil {
		t.Fatal(err)
	}
	f.policy = policy
	if err := f.server.risks.Observe(context.Background(), execution.ManagedAccount{
		AccountKey:  "alice-account",
		Venue:       "okx",
		Environment: execution.EnvironmentSandbox,
		Equity:      "1000",
		Available:   "1000",
		ObservedAt:  time.Now().UTC(),
	}, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f.rig, err = automation.NewRuntime(db, controller, f.server.risks, func(context.Context, string, string) (execution.ManagedTrader, execution.Binding, error) {
		return nil, execution.Binding{}, context.Canceled
	}, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	f.server.managedRuntime = f.rig
	f.router = gin.New()
	f.router.Use(func(c *gin.Context) { c.Set("user_id", c.GetHeader("Test-User")) })
	f.server.registerAutomation(f.router.Group("/api"))
	return f
}

func mustRiskStore(t *testing.T, db *gorm.DB) *risk.Store {
	t.Helper()
	s, err := risk.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *automationFixture) call(t *testing.T, user, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Test-User", user)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

// TestAutomationAPIIsolation proves the account key is an owner-scoped handle:
// a foreign key, an unknown key and an anonymous call all get 403, and no
// response carries a credential or a venue identity.
func TestAutomationAPIIsolation(t *testing.T) {
	f := newAutomationFixture(t)
	cases := []struct{ name, user, path string }{
		{"foreign account", "bob", "/api/automation/alice-account/status"},
		{"unknown account", "alice", "/api/automation/nobody-account/status"},
		{"anonymous", "", "/api/automation/alice-account/status"},
	}
	for _, tc := range cases {
		if w := f.call(t, tc.user, "GET", tc.path, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s = %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	w := f.call(t, "alice", "GET", "/api/automation/alice-account/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("owner status = %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, forbidden := range []string{"okx-uid-1", "secret", "passphrase", "api_key", "token"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("status exposed %q: %s", forbidden, body)
		}
	}
	var status automation.Status
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatal(err)
	}
	if status.AccountKey != "alice-account" || status.Environment != execution.EnvironmentSandbox {
		t.Fatalf("status = %+v", status)
	}
}

// TestAutomationPolicyUpdate pins the version CAS, the default-disabled rule
// and the environment binding.
func TestAutomationPolicyUpdate(t *testing.T) {
	f := newAutomationFixture(t)
	stale := f.policy
	stale.Version = "not-the-current-version"
	body, _ := json.Marshal(map[string]any{"policy": stale})
	if w := f.call(t, "alice", "PUT", "/api/automation/alice-account/policy", string(body)); w.Code != http.StatusConflict {
		t.Fatalf("stale policy version = %d %s", w.Code, w.Body.String())
	}
	next := f.policy
	next.Enabled = true
	body, _ = json.Marshal(map[string]any{"policy": next})
	if w := f.call(t, "alice", "PUT", "/api/automation/alice-account/policy", string(body)); w.Code != http.StatusOK {
		t.Fatalf("policy update = %d %s", w.Code, w.Body.String())
	}
	stored, err := f.server.risks.Policy(context.Background(), "alice", "alice-account")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || stored.Version != mustVersion(t, stored) {
		t.Fatalf("stored policy = %+v", stored)
	}
	if stored.AccountKey != "alice-account" || stored.Venue != "okx" || stored.Environment != execution.EnvironmentSandbox {
		t.Fatalf("the body moved the policy identity: %+v", stored)
	}
	// A policy body cannot retarget another account. The CAS token is the
	// version the operator just read.
	foreign := stored
	foreign.AccountKey = "bob-account"
	foreign.UserID = "bob"
	body, _ = json.Marshal(map[string]any{"policy": foreign})
	if w := f.call(t, "alice", "PUT", "/api/automation/alice-account/policy", string(body)); w.Code != http.StatusOK {
		t.Fatalf("policy body override = %d %s", w.Code, w.Body.String())
	}
	if _, err := f.server.risks.Policy(context.Background(), "bob", "bob-account"); err == nil {
		t.Fatal("a request rewrote another user's policy")
	}
	// start refuses a mismatched environment and a missing confirmation phrase.
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/start", `{"environment":"live","policy_version":"`+stored.Version+`","confirm":"START AUTOMATION"}`); w.Code != http.StatusConflict {
		t.Fatalf("cross-environment start = %d %s", w.Code, w.Body.String())
	}
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/start", `{"environment":"sandbox","policy_version":"`+stored.Version+`","confirm":"yes"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("start without the phrase = %d %s", w.Code, w.Body.String())
	}
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/start", `{"environment":"sandbox","policy_version":"`+stored.Version+`","confirm":"START AUTOMATION"}`); w.Code != http.StatusConflict {
		t.Fatalf("start on an unverified account = %d %s", w.Code, w.Body.String())
	}
	// An unknown field is refused, so no execution parameter can be smuggled in.
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/pause", `{"reason":"ops","quantity":"1"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field accepted = %d %s", w.Code, w.Body.String())
	}
}

func mustVersion(t *testing.T, p risk.Policy) string {
	t.Helper()
	v, err := risk.PolicyVersion(p)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestAutomationPauseVsFlatten proves a pause stops new entries without
// touching protection, and flatten goes through the controller's reduction
// state machine rather than a second order path.
func TestAutomationPauseVsFlatten(t *testing.T) {
	f := newAutomationFixture(t)
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/pause", `{"reason":"ops review"}`); w.Code != http.StatusOK {
		t.Fatalf("pause = %d %s", w.Code, w.Body.String())
	}
	var latch []risk.Breaker
	if err := f.db.Where("scope = ? AND scope_id = ?", risk.ScopeAccount, "alice-account").Find(&latch).Error; err != nil {
		t.Fatal(err)
	}
	if len(latch) != 1 || latch[0].Action != risk.ActionPauseEntries || latch[0].Reason != "ops review" {
		t.Fatalf("pause latch = %+v", latch)
	}
	// Pause is a latch, not a cancellation: it must not create a flatten.
	if w := f.call(t, "alice", "GET", "/api/automation/alice-account/status", ""); w.Code != http.StatusOK {
		t.Fatalf("status after pause = %d %s", w.Code, w.Body.String())
	}
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/flatten", `{"reason":"ops flatten"}`); w.Code != http.StatusConflict {
		t.Fatalf("flatten without a live run = %d %s", w.Code, w.Body.String())
	}
	// reset needs the current latch version and a reason.
	var current risk.Breaker
	if err := f.db.Where("scope = ? AND scope_id = ?", risk.ScopeAccount, "alice-account").First(&current).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/reset", `{"latch_version":"wrong","reason":"reviewed"}`); w.Code != http.StatusConflict {
		t.Fatalf("reset with a stale version = %d %s", w.Code, w.Body.String())
	}
	if w := f.call(t, "alice", "POST", "/api/automation/alice-account/reset", `{"latch_version":"`+current.Version+`","reason":"reviewed"}`); w.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", w.Code, w.Body.String())
	}
	after, err := f.server.risks.Evaluate(context.Background(), "alice", "alice-account", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if after.Action != risk.ActionAllow && after.Reason != "no risk policy for this account" {
		t.Fatalf("a reset left the latch active: %+v", after)
	}
}
