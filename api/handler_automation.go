package api

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nofx/trader/execution"
	"nofx/trading/automation"
	"nofx/trading/risk"
)

// registerAutomation mounts the unattended-execution surface. Every route is
// owner-scoped: the account key is a binding id, and a key that does not
// resolve to a binding owned by the caller is forbidden, never 404.
func (s *Server) registerAutomation(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/automation/:accountKey/status", "Unattended account status", s.handleAutomationStatus)
	s.routeWithSchema(protected, "PUT", "/automation/:accountKey/policy", "Replace the risk policy of an unattended account",
		`Body: {"policy":{...}}`, s.handleAutomationPolicy)
	s.routeWithSchema(protected, "POST", "/automation/:accountKey/start", "Start unattended entries",
		`Body: {"environment":"live|sandbox","policy_version":"<string>","confirm":"START AUTOMATION"}`, s.handleAutomationStart)
	s.routeWithSchema(protected, "POST", "/automation/:accountKey/pause", "Pause new entries, keep protection",
		`Body: {"reason":"<string, optional>"}`, s.handleAutomationPause)
	s.routeWithSchema(protected, "POST", "/automation/:accountKey/flatten", "Exit every position through the controller",
		`Body: {"reason":"<string, optional>"}`, s.handleAutomationFlatten)
	s.routeWithSchema(protected, "POST", "/automation/:accountKey/reset", "Reset a breaker latch after review",
		`Body: {"latch_version":"<string>","reason":"<string>"}`, s.handleAutomationReset)
}

// automationAccount resolves the account key to a binding owned by the caller.
// A missing key and a foreign key are answered identically, so the surface
// cannot be used to probe for other users' accounts.
func (s *Server) automationAccount(c *gin.Context) (execution.Binding, bool) {
	user := c.GetString("user_id")
	var b execution.Binding
	err := s.store.GormDB().WithContext(c.Request.Context()).Where("id = ?", c.Param("accountKey")).First(&b).Error
	if err != nil || b.UserID != user || user == "" {
		if !errors.Is(err, gorm.ErrRecordNotFound) && err != nil {
			c.JSON(500, gin.H{"error": "could not read the managed account"})
			return b, false
		}
		c.JSON(403, gin.H{"error": "no managed account with that key belongs to this user"})
		return b, false
	}
	return b, true
}

func (s *Server) automationRuntime(c *gin.Context) (*automation.Runtime, bool) {
	rt := s.managedRuntimeHandle()
	if rt == nil {
		c.JSON(503, gin.H{"error": "unattended execution is not running on this instance"})
		return nil, false
	}
	return rt, true
}

func (s *Server) handleAutomationStatus(c *gin.Context) {
	b, ok := s.automationAccount(c)
	if !ok {
		return
	}
	rt, ok := s.automationRuntime(c)
	if !ok {
		return
	}
	status, err := rt.AccountStatus(c.Request.Context(), b.UserID, b.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, status)
}

func (s *Server) handleAutomationPolicy(c *gin.Context) {
	b, ok := s.automationAccount(c)
	if !ok {
		return
	}
	var body struct {
		Policy json.RawMessage `json:"policy"`
	}
	if err := decodeAutomationBody(c, &body); err != nil {
		return
	}
	if len(body.Policy) == 0 {
		c.JSON(400, gin.H{"error": "a policy object is required"})
		return
	}
	var policy risk.Policy
	if err := json.Unmarshal(body.Policy, &policy); err != nil {
		c.JSON(400, gin.H{"error": "the policy is not valid JSON"})
		return
	}
	// The identity of a policy is its account, never the request body: a body
	// that names another account must not be able to move a policy there.
	store := s.riskStore()
	if store == nil {
		c.JSON(500, gin.H{"error": "the risk store is unavailable"})
		return
	}
	policy.UserID = b.UserID
	policy.AccountKey = b.ID
	policy.Venue = b.Venue
	policy.Environment = b.Environment
	if policy.Version != "" {
		if _, err := risk.PolicyVersion(policy); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
	}
	// Any stored policy must still exist under the expected version, otherwise
	// an operator would silently overwrite a policy another actor just changed.
	current, err := store.Policy(c.Request.Context(), b.UserID, b.ID)
	switch {
	case err == nil:
		if !policyVersionsAgree(policy, current) {
			c.JSON(409, gin.H{"error": "the policy version is stale; read the current policy and retry"})
			return
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
	default:
		c.JSON(500, gin.H{"error": "could not read the current policy"})
		return
	}
	// The request's version is the CAS token; the stored version is always
	// derived from the content so a pinned token cannot become a stale label.
	policy.Version = ""
	stored, err := store.PutPolicy(c.Request.Context(), policy)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"policy": stored})
}

// policyVersionsAgree is the compare-and-swap token: a replacement must name
// the version it replaces, so two operators cannot silently overwrite each
// other. Content is never used as a substitute for the token.
func policyVersionsAgree(next, current risk.Policy) bool {
	return next.Version == current.Version
}

func (s *Server) handleAutomationStart(c *gin.Context) {
	b, ok := s.automationAccount(c)
	if !ok {
		return
	}
	rt, ok := s.automationRuntime(c)
	if !ok {
		return
	}
	var body struct {
		Environment   string `json:"environment"`
		PolicyVersion string `json:"policy_version"`
		Confirm       string `json:"confirm"`
	}
	if err := decodeAutomationBody(c, &body); err != nil {
		return
	}
	if body.Confirm != "START AUTOMATION" {
		c.JSON(400, gin.H{"error": `confirmation phrase "START AUTOMATION" is required`})
		return
	}
	env := execution.Environment(body.Environment)
	if env != execution.EnvironmentLive && env != execution.EnvironmentSandbox {
		c.JSON(400, gin.H{"error": "environment must be live or sandbox"})
		return
	}
	if env != b.Environment {
		c.JSON(409, gin.H{"error": "the account is bound to a different environment"})
		return
	}
	if err := rt.StartAccount(c.Request.Context(), b.UserID, b.ID, body.PolicyVersion, env); err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"account_key": b.ID, "environment": env, "state": "running"})
}

func (s *Server) handleAutomationPause(c *gin.Context) {
	b, ok := s.automationAccount(c)
	if !ok {
		return
	}
	rt, ok := s.automationRuntime(c)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeAutomationBody(c, &body); err != nil {
		return
	}
	if err := rt.PauseAccount(c.Request.Context(), b.UserID, b.ID, body.Reason); err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"account_key": b.ID, "state": "paused"})
}

func (s *Server) handleAutomationFlatten(c *gin.Context) {
	b, ok := s.automationAccount(c)
	if !ok {
		return
	}
	rt, ok := s.automationRuntime(c)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeAutomationBody(c, &body); err != nil {
		return
	}
	if err := rt.FlattenAccount(c.Request.Context(), b.UserID, b.ID, body.Reason); err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"account_key": b.ID, "state": "flat"})
}

func (s *Server) handleAutomationReset(c *gin.Context) {
	b, ok := s.automationAccount(c)
	if !ok {
		return
	}
	var body struct {
		LatchVersion string `json:"latch_version"`
		Reason       string `json:"reason"`
	}
	if err := decodeAutomationBody(c, &body); err != nil {
		return
	}
	if body.LatchVersion == "" || body.Reason == "" {
		c.JSON(400, gin.H{"error": "latch_version and reason are required"})
		return
	}
	store := s.riskStore()
	if store == nil {
		c.JSON(500, gin.H{"error": "the risk store is unavailable"})
		return
	}
	if err := store.Reset(c.Request.Context(), b.UserID, b.ID, body.LatchVersion, time.Now().UTC()); err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"account_key": b.ID, "state": "reset"})
}

func (s *Server) riskStore() *risk.Store {
	s.planningMu.Lock()
	defer s.planningMu.Unlock()
	if s.risks == nil {
		store, err := risk.NewStore(s.store.GormDB())
		if err != nil {
			return nil
		}
		s.risks = store
	}
	return s.risks
}

// decodeAutomationBody rejects unknown fields, so a request cannot smuggle an
// execution parameter (quantity, leverage, client id) past the handler.
func decodeAutomationBody(c *gin.Context, out any) error {
	dec := json.NewDecoder(io.LimitReader(c.Request.Body, 64*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(400, gin.H{"error": "the request body is not valid: " + err.Error()})
		return err
	}
	return nil
}
