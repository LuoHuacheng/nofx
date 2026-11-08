package automation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type signalBarCloseKey struct{}

// signalScan is an at-most-once checkpoint, including failed attempts. Only the
// latest close is retained per account/symbol; restarting never replays a bar.
type signalScan struct {
	AccountKey string `gorm:"primaryKey"`
	Symbol     string `gorm:"primaryKey"`
	BarClose   time.Time
}

func (signalScan) TableName() string { return "managed_signal_scans" }

// nextSignalSymbol runs with the account lock held. Model jobs remain separate
// from the two-second account supervision and one job is active per account.
func (r *Runtime) nextSignalSymbol(ctx context.Context, user, accountKey string, state *accountState, now time.Time) (string, error) {
	policy, err := r.risks.Policy(ctx, user, accountKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("automation: read signal schedule policy: %w", err)
	}
	if !policy.Enabled {
		return "", nil
	}
	closeTime := now.Truncate(SignalInterval)
	if state.lastSignal.IsZero() || closeTime.After(state.lastSignal) {
		state.lastSignal = closeTime
		state.signalIndex = 0
	}
	if closeTime.Before(state.lastSignal) {
		return "", nil
	}
	for state.signalIndex < len(policy.Symbols) {
		symbol := policy.Symbols[state.signalIndex]
		checkpoint := signalScan{AccountKey: accountKey, Symbol: symbol, BarClose: closeTime}
		result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "account_key"}, {Name: "symbol"}},
			DoUpdates: clause.AssignmentColumns([]string{"bar_close"}),
			Where:     clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "managed_signal_scans.bar_close < ?", Vars: []any{closeTime}}}},
		}).Create(&checkpoint)
		if result.Error != nil {
			return "", fmt.Errorf("automation: persist signal scan: %w", result.Error)
		}
		state.signalIndex++
		if result.RowsAffected > 0 {
			return symbol, nil
		}
	}
	return "", nil
}
