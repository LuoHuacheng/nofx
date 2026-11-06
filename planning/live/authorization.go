package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// policyDigest binds an automatic authorization to the policy version and the
// signal that produced it; the manual form stays byte-identical.
func policyDigest(p core.Plan, user, exchange, account, policyVersion, signalID string) string {
	raw, _ := json.Marshal([]any{p.Hash, user, exchange, account, fmt.Sprintf(liveExecutionText, p.Constraints.EffectiveLeverage()), policyVersion, signalID})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// approvalDigest selects the digest form from the persisted mode, never from
// caller input: a historical row (empty mode) keeps its manual digest.
func (r Run) approvalDigest(p core.Plan) string {
	if r.AuthorizationMode == "policy" {
		return policyDigest(p, r.UserID, r.ExchangeID, r.AccountKey, r.PolicyVersion, r.SignalID)
	}
	return digest(p, r.UserID, r.ExchangeID, r.AccountKey)
}

type approvalRequest struct {
	User, PlanID, Hash      string
	Phrase                  string // manual approvals must repeat APPROVE LIVE
	ReplaceID, ReplaceHash  string
	Mode                    string // "" is manual, "policy" is automatic
	SignalID, PolicyVersion string
}

// AuthorizePolicy lets the server approve a plan for one signal without a
// human phrase. The policy and binding are read here, so the caller supplies
// only the version it saw; an authorization reserves no budget, because the
// hold commits when Execute starts the run.
func (c *Controller) AuthorizePolicy(ctx context.Context, user, planID, planHash, policyVersion, signalID string) (View, error) {
	return c.approve(ctx, approvalRequest{User: user, PlanID: planID, Hash: planHash, Mode: "policy", SignalID: signalID, PolicyVersion: policyVersion})
}

func (c *Controller) approve(ctx context.Context, req approvalRequest) (View, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if req.Mode == "" && req.Phrase != "APPROVE LIVE" {
		return View{}, errors.New("Type APPROVE LIVE to approve live trading")
	}
	if req.Mode == "policy" && (req.SignalID == "" || len(req.SignalID) > 128) {
		return View{}, errors.New("automatic authorization requires a signal id of at most 128 bytes")
	}
	var d planning.Draft
	if e := c.DB.Where("user_id = ? AND id = ?", req.User, req.PlanID).First(&d).Error; e != nil {
		return View{}, errors.New("plan not found or not owned")
	}
	var p core.Plan
	if json.Unmarshal([]byte(d.Body), &p) != nil || p.Hash != req.Hash {
		return View{}, errors.New("full plan hash required")
	}
	if e := p.Verify(time.Now().UTC()); e != nil {
		return View{}, e
	}
	if p.Constraints.PlanningOnly {
		return View{}, errors.New(EquityPreviewReviewMessage)
	}
	broker, account, e := c.Factory(ctx, req.User, d.ExchangeID)
	if e != nil {
		return View{}, e
	}
	if broker.Venue() != p.Snapshot.Venue {
		return View{}, errors.New("Plan venue differs from the trading account. Generate a new plan.")
	}
	if account == "" {
		return View{}, errors.New("account identity unavailable")
	}
	// A managed account fixes its reservation identity now; execution never
	// re-reads the policy, so a re-authorisation cannot change a live run.
	var binding execution.Binding
	managed := false
	var policy risk.Policy
	if err := c.DB.WithContext(ctx).Where("user_id = ? AND exchange_id = ? AND enabled = ?", req.User, d.ExchangeID, true).First(&binding).Error; err == nil {
		managed = true
		if c.Risk == nil {
			return View{}, errors.New("managed account requires the risk store")
		}
		if policy, err = c.Risk.Policy(ctx, req.User, account); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return View{}, errors.New("managed account has no risk policy")
			}
			return View{}, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, err
	}
	// Automatic authorization is only valid for the exact policy and account
	// the server can see now; a caller cannot carry its own policy object.
	if req.Mode == "policy" {
		if !managed {
			return View{}, errors.New("automatic authorization requires a managed account")
		}
		if !policy.Enabled {
			return View{}, errors.New("automation policy is disabled")
		}
		if policy.Version != req.PolicyVersion {
			return View{}, errors.New("policy version changed; re-authorize")
		}
		if policy.Environment != binding.Environment {
			return View{}, errors.New("policy environment differs from the account binding")
		}
		if policy.Venue != broker.Venue() {
			return View{}, errors.New("policy venue differs from the trading account")
		}
		if err := VerifyManagedBudget(p, policy); err != nil {
			return View{}, err
		}
		if p.Constraints.EffectiveLeverage() > policy.MaxLeverage {
			return View{}, errors.New("plan leverage exceeds the policy maximum")
		}
	}
	// Resolve a previously closed trade using exchange reads before acquiring a
	// new lease. The transaction below still arbitrates concurrent approvals.
	var previousLease Lease
	if err := c.DB.Where("account_key = ?", account).First(&previousLease).Error; err == nil {
		if previousLease.UserID == req.User {
			if _, err = c.CheckStatus(ctx, req.User, previousLease.RunID); err != nil {
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
	r := Run{ID: uuid.NewString(), UserID: req.User, ExchangeID: d.ExchangeID, AccountKey: account, PlanID: p.ID, PlanHash: p.Hash, State: "approved", PlanJSON: d.Body, OrdersJSON: "[]", ApprovedAt: now, UpdatedAt: now, Managed: managed, AuthorizationMode: req.Mode, SignalID: req.SignalID}
	if managed {
		r.PolicyID, r.PolicyVersion, r.PortfolioID = policy.ID, policy.Version, policy.PortfolioID
	}
	r.ApprovalHash = r.approvalDigest(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	// The same signal may be replayed by a retrying planner: it must resolve to
	// the run it already authorized instead of consuming another approval.
	if req.Mode == "policy" {
		if v, ok, er := c.policyRun(account, req.SignalID, p.Hash); er != nil {
			return View{}, er
		} else if ok {
			return v, nil
		}
	}
	e = c.DB.Transaction(func(tx *gorm.DB) error {
		var lease Lease
		if err := tx.Where("account_key = ?", account).First(&lease).Error; err == nil {
			var previous Run
			if lease.RunID != previousLease.RunID || lease.UserID != req.User || tx.Where("id = ? AND user_id = ?", lease.RunID, req.User).First(&previous).Error != nil {
				return errors.New("Account approval changed. Refresh and review the order again.")
			}
			v, er := decode(previous)
			if er != nil {
				return er
			}
			if v.PlanID == p.ID || v.State != "approved" || v.ID != req.ReplaceID || v.ApprovalHash != req.ReplaceHash {
				return errors.New("An earlier trade is reserving this account. Review the current trade before confirming a new order.")
			}
			if er = retireUnusedApproval(tx, v, "cancelled", "Unused approval replaced by a newly confirmed order; no exchange order was submitted.", c.Risk); er != nil {
				return er
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		res := tx.Model(&planning.Draft{}).Where("user_id = ? AND id = ? AND status = ?", req.User, p.ID, "draft").Update("status", "approved")
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("only an unchanged draft can be approved")
		}
		if er := tx.Create(&Lease{AccountKey: account, RunID: r.ID, UserID: req.User, ExchangeID: d.ExchangeID}).Error; er != nil {
			return errors.New("An earlier trade is reserving this account. Open Positions & orders to check its status or cancel its unused approval.")
		}
		return tx.Create(&r).Error
	})
	if e != nil {
		// A concurrent authorization of the same signal loses the unique index
		// race and must still resolve to the winner's run.
		if req.Mode == "policy" {
			if v, ok, er := c.policyRun(account, req.SignalID, p.Hash); er == nil && ok {
				return v, nil
			}
		}
		return View{}, e
	}
	return decode(r)
}

// policyRun reports the run one signal already authorized. The bool reports
// whether a row exists; a different plan under the same signal is a conflict.
func (c *Controller) policyRun(account, signal, hash string) (View, bool, error) {
	var existing Run
	err := c.DB.Where("account_key = ? AND signal_id = ? AND authorization_mode = ?", account, signal, "policy").First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, false, nil
	}
	if err != nil {
		return View{}, false, err
	}
	if existing.PlanHash != hash {
		return View{}, false, errors.New("signal already authorized a different plan")
	}
	v, err := decode(existing)
	if err != nil {
		return View{}, false, err
	}
	return v, true, nil
}
