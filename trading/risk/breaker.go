package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"nofx/trader/execution"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Breaker scopes. One latch exists per scope identity.
const (
	ScopeAccount   = "account"
	ScopePortfolio = "portfolio"
)

// Breaker is the persisted latch of one account or portfolio. The row is never
// deleted: a controlled reset stamps ResetAt/ResetBy/ResetVersion so the
// incident stays auditable while the latch stops blocking new entries.
type Breaker struct {
	Scope        string    `gorm:"column:scope;primaryKey" json:"scope"`
	ScopeID      string    `gorm:"column:scope_id;primaryKey" json:"scope_id"`
	Action       string    `gorm:"column:action;not null" json:"action"`
	Reason       string    `gorm:"column:reason;not null" json:"reason"`
	TriggeredAt  time.Time `gorm:"column:triggered_at;not null" json:"triggered_at"`
	Version      string    `gorm:"column:version;not null" json:"version"`
	ResetAt      time.Time `gorm:"column:reset_at" json:"reset_at"`
	ResetBy      string    `gorm:"column:reset_by" json:"reset_by"`
	ResetVersion string    `gorm:"column:reset_version" json:"reset_version"`
}

func (Breaker) TableName() string { return "managed_risk_breakers" }

// ResetRecord is the audit row of one controlled reset: who acknowledged which
// breaker version, and the equity baseline that was accepted at that moment.
type ResetRecord struct {
	ID             uint              `gorm:"primaryKey" json:"id"`
	Scope          string            `gorm:"column:scope;not null" json:"scope"`
	ScopeID        string            `gorm:"column:scope_id;not null" json:"scope_id"`
	UserID         string            `gorm:"column:user_id;not null" json:"user_id"`
	BreakerVersion string            `gorm:"column:breaker_version;not null" json:"breaker_version"`
	Reason         string            `gorm:"column:reason;not null" json:"reason"`
	BaselineDay    string            `gorm:"column:baseline_day;not null" json:"baseline_day"`
	BaselineEquity execution.Decimal `gorm:"column:baseline_equity;not null" json:"baseline_equity"`
	ResetAt        time.Time         `gorm:"column:reset_at;not null" json:"reset_at"`
}

func (ResetRecord) TableName() string { return "managed_risk_resets" }

// Evaluate returns the persisted hard-breaker verdict for one account. It reads
// only persisted state: no caller-supplied number or model confidence can
// change the outcome. A threshold breach writes the latch inside the same
// transaction, so a decision is never reported without its record.
func (s *Store) Evaluate(ctx context.Context, user, accountKey string, now time.Time) (Decision, error) {
	var decision Decision
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		policy, err := policyTx(tx, user, accountKey)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				decision = Decision{Action: ActionPauseEntries, Reason: "no risk policy for this account"}
				return nil
			}
			return fmt.Errorf("risk: load policy of %s: %w", accountKey, err)
		}

		latch, latched, err := activeLatchTx(tx, ScopeAccount, accountKey)
		if err != nil {
			return err
		}
		if policy.PortfolioID != "" {
			portfolio, ok, err := activeLatchTx(tx, ScopePortfolio, portfolioScope(policy))
			if err != nil {
				return err
			}
			if ok && (!latched || stronger(portfolio.Action, latch.Action)) {
				latch, latched = portfolio, true
			}
		}
		if latched {
			decision = Decision{Action: latch.Action, Reason: latch.Reason}
			return nil
		}

		day := now.UTC().Format(ledgerDayLayout)
		state, err := equityStateTx(tx, accountKey, day)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				decision = Decision{Action: ActionPauseEntries, Reason: fmt.Sprintf("no equity baseline of %s for %s", accountKey, day)}
				return nil
			}
			return fmt.Errorf("risk: load equity baseline of %s: %w", accountKey, err)
		}
		if !state.Complete {
			decision = Decision{Action: ActionPauseEntries, Reason: fmt.Sprintf("equity baseline of %s for %s is incomplete", accountKey, day)}
			return nil
		}
		if age := now.UTC().Sub(state.ObservedAt.UTC()); age > PortfolioSnapshotMaxAge {
			decision = Decision{Action: ActionPauseEntries, Reason: fmt.Sprintf("equity baseline of %s is stale: observed %s ago", accountKey, age)}
			return nil
		}

		adjusted, err := execution.Number(string(state.AdjustedEquity))
		if err != nil {
			return fmt.Errorf("risk: stored adjusted equity of %s is invalid: %w", accountKey, err)
		}
		dayStart, err := execution.Number(string(state.DayStart))
		if err != nil {
			return fmt.Errorf("risk: stored day start of %s is invalid: %w", accountKey, err)
		}
		peak, err := execution.Number(string(state.Peak))
		if err != nil {
			return fmt.Errorf("risk: stored peak of %s is invalid: %w", accountKey, err)
		}

		// A reset re-anchors the daily-loss baseline: without it every reset would
		// re-latch on the next tick, because the day start still precedes the loss
		// the operator just acknowledged. The re-anchor may only tighten: a reset
		// taken after the equity rose must not loosen the day's limit.
		if reset, found, err := latestResetTx(tx, ScopeAccount, accountKey, day); err != nil {
			return err
		} else if found {
			reanchored, err := execution.Number(string(reset.BaselineEquity))
			if err != nil {
				return fmt.Errorf("risk: stored reset baseline of %s is invalid: %w", accountKey, err)
			}
			if reanchored.Cmp(dayStart) < 0 {
				dayStart = reanchored
			}
		}

		if breached(adjusted, dayStart, policy.DailyLossBPS) {
			reason := fmt.Sprintf("daily loss limit reached: adjusted equity %s against day start %s exceeds %d bps",
				state.AdjustedEquity, state.DayStart, policy.DailyLossBPS)
			return latchBreach(tx, accountKey, portfolioScope(policy), reason, &decision, now)
		}
		if breached(adjusted, peak, policy.DrawdownBPS) {
			reason := fmt.Sprintf("drawdown limit reached: adjusted equity %s against peak %s exceeds %d bps",
				state.AdjustedEquity, state.Peak, policy.DrawdownBPS)
			return latchBreach(tx, accountKey, portfolioScope(policy), reason, &decision, now)
		}
		decision = Decision{Action: ActionAllow}
		return nil
	})
	return decision, err
}

// breached reports whether value fell to or past a basis-point limit of
// baseline. The comparison is exact decimal: 100 against 100 at 200 bps trips
// exactly at 98.
func breached(value, baseline *big.Rat, bps int) bool {
	limit := new(big.Rat).Mul(baseline, new(big.Rat).Sub(big.NewRat(1, 1), big.NewRat(int64(bps), 10000)))
	return value.Cmp(limit) <= 0
}

// stronger reports whether action outranks an already recorded latch action.
func stronger(action, than string) bool { return action == ActionFlatten && than != ActionFlatten }

// latchTx returns the latch row of one scope, reset or not.
func latchTx(tx *gorm.DB, scope, scopeID string) (Breaker, bool, error) {
	var latch Breaker
	err := tx.Where("scope = ? AND scope_id = ?", scope, scopeID).First(&latch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Breaker{}, false, nil
	}
	if err != nil {
		return Breaker{}, false, fmt.Errorf("risk: load breaker latch of %s: %w", scopeID, err)
	}
	return latch, true, nil
}

// activeLatchTx returns the latch of one scope unless a reset cleared it. The
// row itself stays: only a Reset stamps the markers.
func activeLatchTx(tx *gorm.DB, scope, scopeID string) (Breaker, bool, error) {
	latch, ok, err := latchTx(tx, scope, scopeID)
	if err != nil || !ok || !latch.ResetAt.IsZero() {
		return Breaker{}, false, err
	}
	return latch, true, nil
}

// latchBreach records the flatten latch on the account and, when the policy has
// one, on its portfolio: the portfolio latch stops every sibling account until
// each of them is reset.
func latchBreach(tx *gorm.DB, accountKey, portfolioID, reason string, decision *Decision, now time.Time) error {
	if err := writeLatchTx(tx, ScopeAccount, accountKey, ActionFlatten, reason, now); err != nil {
		return err
	}
	if portfolioID != "" {
		if err := writeLatchTx(tx, ScopePortfolio, portfolioID, ActionFlatten, reason, now); err != nil {
			return err
		}
	}
	*decision = Decision{Action: ActionFlatten, Reason: reason}
	return nil
}

// writeLatchTx records a latch. While the existing latch is active a stronger
// action replaces a weaker one and a weaker one is ignored; a reset row is inert,
// so any new latch may take it over. An unchanged active latch keeps its version
// so a reset's expected version stays valid.
func writeLatchTx(tx *gorm.DB, scope, scopeID, action, reason string, now time.Time) error {
	var existing Breaker
	err := tx.Where("scope = ? AND scope_id = ?", scope, scopeID).First(&existing).Error
	switch {
	case err == nil:
		if existing.ResetAt.IsZero() && existing.Action == ActionFlatten && action != ActionFlatten {
			return nil
		}
		if existing.ResetAt.IsZero() && existing.Action == action {
			return nil
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
	default:
		return fmt.Errorf("risk: load breaker latch of %s: %w", scopeID, err)
	}
	at := now.UTC()
	latch := Breaker{
		Scope:       scope,
		ScopeID:     scopeID,
		Action:      action,
		Reason:      reason,
		TriggeredAt: at,
		Version:     breakerVersion(scope, scopeID, action, at),
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "scope"}, {Name: "scope_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"action", "reason", "triggered_at", "version", "reset_at", "reset_by", "reset_version"}),
	}).Create(&latch).Error
}

// breakerVersion identifies one latch write. A reset must name this version, so
// a latch rewritten afterwards cannot be cleared by a stale reset.
func breakerVersion(scope, scopeID, action string, at time.Time) string {
	h := sha256.Sum256([]byte(scope + "/" + scopeID + "/" + action + "/" + at.Format(time.RFC3339Nano)))
	return hex.EncodeToString(h[:8])
}

// latestResetTx returns the newest reset acknowledged for one scope and UTC
// day: that baseline replaces the day start the incident was measured against.
func latestResetTx(tx *gorm.DB, scope, scopeID, day string) (ResetRecord, bool, error) {
	var reset ResetRecord
	err := tx.Where("scope = ? AND scope_id = ? AND baseline_day = ?", scope, scopeID, day).
		Order("id DESC").First(&reset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ResetRecord{}, false, nil
	}
	if err != nil {
		return ResetRecord{}, false, fmt.Errorf("risk: load reset of %s: %w", scopeID, err)
	}
	return reset, true, nil
}

// Pause latches the account for a reason the caller proved (for example an
// unconfirmed loss). It is owner-checked so a foreign caller cannot freeze an
// account it does not own.
func (s *Store) Pause(ctx context.Context, user, accountKey, reason string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := policyTx(tx, user, accountKey); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("risk: account %s has no policy for user %s", accountKey, user)
			}
			return err
		}
		return writeLatchTx(tx, ScopeAccount, accountKey, ActionPauseEntries, reason, time.Now().UTC())
	})
}

// Reset clears the account latch with an audit trail. The store cannot see
// planning_live_runs, so the brief's "no open position or order" is expressed
// through the reservation: every managed run reserves before any order and only
// releases on terminal proof, so a non-released row is exactly an unresolved
// exposure. Peak is never lowered and the latch row is never deleted, so a real
// drawdown is not papered over by a reset.
func (s *Store) Reset(ctx context.Context, user, accountKey, expectedVersion string, now time.Time) error {
	at := now.UTC()
	day := at.Format(ledgerDayLayout)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		policy, err := policyTx(tx, user, accountKey)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("risk: reset refused: account %s has no policy for user %s", accountKey, user)
			}
			return err
		}
		latch, ok, err := latchTx(tx, ScopeAccount, accountKey)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("risk: reset refused: no breaker latch is recorded for %s", accountKey)
		}
		if latch.Version != expectedVersion {
			return fmt.Errorf("risk: reset refused: latch version %s is not the expected %s", latch.Version, expectedVersion)
		}
		var unresolved int64
		if err := tx.Model(&ReservationRecord{}).
			Where("account_key = ? AND state <> ?", accountKey, StateReleased).
			Count(&unresolved).Error; err != nil {
			return fmt.Errorf("risk: load reservations of %s: %w", accountKey, err)
		}
		if unresolved > 0 {
			return fmt.Errorf("risk: reset refused: %d reservation(s) of %s are not released", unresolved, accountKey)
		}
		state, err := equityStateTx(tx, accountKey, day)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("risk: reset refused: %s has no equity baseline for %s", accountKey, day)
			}
			return fmt.Errorf("risk: load equity baseline of %s: %w", accountKey, err)
		}
		if !state.Complete {
			return fmt.Errorf("risk: reset refused: the equity baseline of %s for %s is incomplete", accountKey, day)
		}
		baseline, err := execution.Number(string(state.AdjustedEquity))
		if err != nil {
			return fmt.Errorf("risk: stored adjusted equity of %s is invalid: %w", accountKey, err)
		}
		baselineDec, err := execution.ExactDecimal(baseline)
		if err != nil {
			return fmt.Errorf("risk: reset baseline is not an exact decimal: %w", err)
		}
		// A breach latches the account and its portfolio in the same write, so one
		// acknowledgement clears both halves of that incident. A portfolio latch
		// triggered later belongs to another account's incident and stays.
		// Each scope is guarded by its own version: the account and portfolio
		// latches of one incident were written separately and hash differently.
		scopes := []struct{ scope, scopeID, version string }{{ScopeAccount, accountKey, latch.Version}}
		if policy.PortfolioID != "" {
			portfolio, found, err := latchTx(tx, ScopePortfolio, portfolioScope(policy))
			if err != nil {
				return err
			}
			if found && !portfolio.TriggeredAt.After(latch.TriggeredAt) {
				scopes = append(scopes, struct{ scope, scopeID, version string }{ScopePortfolio, portfolioScope(policy), portfolio.Version})
			}
		}
		for _, s := range scopes {
			record := ResetRecord{
				Scope:          s.scope,
				ScopeID:        s.scopeID,
				UserID:         user,
				BreakerVersion: s.version,
				Reason:         "operator reset",
				BaselineDay:    day,
				BaselineEquity: baselineDec,
				ResetAt:        at,
			}
			if err := tx.Create(&record).Error; err != nil {
				return fmt.Errorf("risk: record reset of %s: %w", s.scopeID, err)
			}
			updated := tx.Model(&Breaker{}).
				Where("scope = ? AND scope_id = ? AND version = ?", s.scope, s.scopeID, s.version).
				Updates(map[string]any{"reset_at": at, "reset_by": user, "reset_version": s.version})
			if updated.Error != nil {
				return fmt.Errorf("risk: mark breaker reset of %s: %w", s.scopeID, updated.Error)
			}
			if updated.RowsAffected != 1 {
				return errors.New("risk: reset refused: the breaker changed while resetting")
			}
		}
		return nil
	})
}
