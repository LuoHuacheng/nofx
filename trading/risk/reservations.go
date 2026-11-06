package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"nofx/trader/execution"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PortfolioSnapshotMaxAge is how old an equity baseline may be and still
// justify new risk. Limits computed from a stale snapshot would let an account
// that already lost equity keep its old budget.
const PortfolioSnapshotMaxAge = 5 * time.Second

// ReservationRecord is the durable form of one Run's budget hold. A row exists
// from before the first order until the run is provably terminal, so a crash or
// an UNKNOWN receipt never silently frees risk.
type ReservationRecord struct {
	RunID         string            `gorm:"column:run_id;primaryKey" json:"run_id"`
	UserID        string            `gorm:"column:user_id;not null;index" json:"user_id"`
	PolicyID      string            `gorm:"column:policy_id;not null" json:"policy_id"`
	AccountKey    string            `gorm:"column:account_key;not null;index" json:"account_key"`
	PortfolioID   string            `gorm:"column:portfolio_id;not null;index" json:"portfolio_id"`
	Symbol        string            `gorm:"column:symbol;not null" json:"symbol"`
	Side          string            `gorm:"column:side;not null" json:"side"`
	PolicyVersion string            `gorm:"column:policy_version;not null" json:"policy_version"`
	Risk          execution.Decimal `gorm:"column:risk;not null" json:"risk"`
	Notional      execution.Decimal `gorm:"column:notional;not null" json:"notional"`
	Margin        execution.Decimal `gorm:"column:margin;not null" json:"margin"`
	State         string            `gorm:"column:state;not null;index" json:"state"`
	CreatedAt     time.Time         `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt     time.Time         `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ReservationRecord) TableName() string { return "managed_risk_reservations" }

// portfolioLock serializes limit computation per portfolio: every reserving
// transaction takes the row lock before it reads the portfolio's other holds.
type portfolioLock struct {
	PortfolioID string `gorm:"column:portfolio_id;primaryKey"`
}

func (portfolioLock) TableName() string { return "managed_portfolio_locks" }

// ReserveTx records a Run's budget hold inside the caller's transaction. It
// never opens its own transaction: the hold and whatever the caller writes
// before the first order commit together or not at all.
func (s *Store) ReserveTx(ctx context.Context, tx *gorm.DB, r Reservation, now time.Time) error {
	if tx == nil {
		return errors.New("risk: reserve requires a transaction")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	now = now.UTC()
	day := now.Format(ledgerDayLayout)

	policy, err := policyTx(tx, r.UserID, r.AccountKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("risk: account %s has no risk policy: %w", r.AccountKey, err)
		}
		return err
	}
	if policy.Version != r.PolicyVersion {
		return errors.New("risk: policy version changed; re-authorize")
	}

	state, err := equityStateTx(tx, r.AccountKey, day)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("risk: account %s has no equity baseline for %s: %w", r.AccountKey, day, err)
		}
		return err
	}
	if !state.Complete {
		return fmt.Errorf("risk: equity baseline of %s is incomplete", r.AccountKey)
	}
	if age := now.Sub(state.ObservedAt.UTC()); age > PortfolioSnapshotMaxAge {
		return fmt.Errorf("risk: equity baseline of %s is stale: observed %s ago", r.AccountKey, age)
	}

	if err := lockPortfolio(tx, r.PortfolioID); err != nil {
		return err
	}

	equity, err := execution.Number(string(state.AdjustedEquity))
	if err != nil {
		return fmt.Errorf("risk: stored adjusted equity of %s is invalid: %w", r.AccountKey, err)
	}
	portfolioEquity, err := portfolioEquityTx(tx, r.PortfolioID, day, now)
	if err != nil {
		return err
	}

	risk, err := execution.Number(string(r.Risk))
	if err != nil {
		return err
	}
	notional, err := execution.Number(string(r.Notional))
	if err != nil {
		return err
	}
	margin, err := execution.Number(string(r.Margin))
	if err != nil {
		return err
	}

	// A retried run must not count its own row as additional risk.
	var held []ReservationRecord
	if err := tx.Where("account_key = ? AND state IN ? AND run_id <> ?", r.AccountKey,
		[]string{StateReserved, StateExposed}, r.RunID).Find(&held).Error; err != nil {
		return fmt.Errorf("risk: load account reservations: %w", err)
	}
	accountRisk, accountMargin, accountGross, accountDirectional, err := sums(held, r.Side)
	if err != nil {
		return err
	}

	if err := checkLimit("per-trade risk", risk, equity, policy.PerTradeRiskBPS); err != nil {
		return err
	}
	if err := checkLimit("account open risk", new(big.Rat).Add(accountRisk, risk), equity, policy.AccountOpenRiskBPS); err != nil {
		return err
	}
	if err := checkLimit("account margin", new(big.Rat).Add(accountMargin, margin), equity, policy.MaxMarginBPS); err != nil {
		return err
	}
	if err := checkLimit("account gross notional", new(big.Rat).Add(accountGross, notional), equity, policy.MaxGrossBPS); err != nil {
		return err
	}
	if err := checkLimit("account directional notional", new(big.Rat).Add(accountDirectional, notional), equity, policy.MaxDirectionalBPS); err != nil {
		return err
	}

	var portfolioHeld []ReservationRecord
	if err := tx.Where("portfolio_id = ? AND state IN ? AND run_id <> ?", r.PortfolioID,
		[]string{StateReserved, StateExposed}, r.RunID).Find(&portfolioHeld).Error; err != nil {
		return fmt.Errorf("risk: load portfolio reservations: %w", err)
	}
	portfolioRisk, _, portfolioGross, portfolioDirectional, err := sums(portfolioHeld, r.Side)
	if err != nil {
		return err
	}
	if err := checkLimit("portfolio open risk", new(big.Rat).Add(portfolioRisk, risk), portfolioEquity, policy.PortfolioOpenRiskBPS); err != nil {
		return err
	}
	if err := checkLimit("portfolio gross notional", new(big.Rat).Add(portfolioGross, notional), portfolioEquity, policy.MaxGrossBPS); err != nil {
		return err
	}
	if err := checkLimit("portfolio directional notional", new(big.Rat).Add(portfolioDirectional, notional), portfolioEquity, policy.MaxDirectionalBPS); err != nil {
		return err
	}

	existing, err := reservationTx(tx, r.RunID)
	switch {
	case err == nil:
		if !sameHold(existing, r) {
			return fmt.Errorf("risk: run %s already holds a different reservation", r.RunID)
		}
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
	default:
		return err
	}

	record := ReservationRecord{
		RunID:         r.RunID,
		UserID:        r.UserID,
		PolicyID:      r.PolicyID,
		AccountKey:    r.AccountKey,
		PortfolioID:   r.PortfolioID,
		Symbol:        r.Symbol,
		Side:          r.Side,
		PolicyVersion: r.PolicyVersion,
		Risk:          r.Risk,
		Notional:      r.Notional,
		Margin:        r.Margin,
		State:         r.State,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := tx.Create(&record).Error; err != nil {
		return fmt.Errorf("risk: record reservation %s: %w", r.RunID, err)
	}
	return nil
}

// ExposeTx marks a hold as backed by a live position. Only a reserved row
// moves: an already exposed or released run keeps its state.
func (s *Store) ExposeTx(ctx context.Context, tx *gorm.DB, runID string) error {
	if tx == nil {
		return errors.New("risk: expose requires a transaction")
	}
	return tx.Model(&ReservationRecord{}).
		Where("run_id = ? AND state = ?", runID, StateReserved).
		Updates(map[string]any{"state": StateExposed, "updated_at": time.Now().UTC()}).Error
}

// ReleaseTx frees a hold. It is idempotent: the caller owns the terminal
// proof, and releasing an already released run is not an error.
func (s *Store) ReleaseTx(ctx context.Context, tx *gorm.DB, runID string) error {
	if tx == nil {
		return errors.New("risk: release requires a transaction")
	}
	return tx.Model(&ReservationRecord{}).
		Where("run_id = ?", runID).
		Updates(map[string]any{"state": StateReleased, "updated_at": time.Now().UTC()}).Error
}

// Reservation returns the stored hold of one run.
func (s *Store) Reservation(ctx context.Context, runID string) (ReservationRecord, error) {
	return reservationTx(s.db.WithContext(ctx), runID)
}

func reservationTx(tx *gorm.DB, runID string) (ReservationRecord, error) {
	var record ReservationRecord
	if err := tx.Where("run_id = ?", runID).First(&record).Error; err != nil {
		return ReservationRecord{}, err
	}
	return record, nil
}

func equityStateTx(tx *gorm.DB, accountKey, day string) (EquityState, error) {
	var state EquityState
	if err := tx.Where("account_key = ? AND day = ?", accountKey, day).First(&state).Error; err != nil {
		return EquityState{}, err
	}
	return state, nil
}

func policyTx(tx *gorm.DB, user, accountKey string) (Policy, error) {
	var record policyRecord
	if err := tx.Where("user_id = ? AND account_key = ?", user, accountKey).First(&record).Error; err != nil {
		return Policy{}, err
	}
	var p Policy
	if err := json.Unmarshal([]byte(record.Body), &p); err != nil {
		return Policy{}, fmt.Errorf("risk: decode stored policy: %w", err)
	}
	return p, nil
}

// lockPortfolio takes the portfolio row lock inside the transaction. SQLite
// ignores FOR UPDATE and serializes through its single writer instead.
func lockPortfolio(tx *gorm.DB, portfolioID string) error {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&portfolioLock{PortfolioID: portfolioID}).Error; err != nil {
		return fmt.Errorf("risk: create portfolio lock row: %w", err)
	}
	if tx.Dialector.Name() == "sqlite" {
		return nil
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("portfolio_id = ?", portfolioID).First(&portfolioLock{}).Error; err != nil {
		return fmt.Errorf("risk: lock portfolio %s: %w", portfolioID, err)
	}
	return nil
}

// portfolioEquityTx sums the adjusted equity of every fresh, complete account
// whose stored policy belongs to the same portfolio.
func portfolioEquityTx(tx *gorm.DB, portfolioID, day string, now time.Time) (*big.Rat, error) {
	var policies []policyRecord
	if err := tx.Find(&policies).Error; err != nil {
		return nil, fmt.Errorf("risk: load portfolio policies: %w", err)
	}
	var keys []string
	for _, record := range policies {
		var p Policy
		if err := json.Unmarshal([]byte(record.Body), &p); err != nil {
			return nil, fmt.Errorf("risk: decode stored policy: %w", err)
		}
		if p.PortfolioID == portfolioID {
			keys = append(keys, record.AccountKey)
		}
	}
	total := new(big.Rat)
	if len(keys) == 0 {
		return total, nil
	}
	var states []EquityState
	if err := tx.Where("account_key IN ? AND day = ? AND complete = ?", keys, day, true).
		Find(&states).Error; err != nil {
		return nil, fmt.Errorf("risk: load portfolio equity: %w", err)
	}
	for _, state := range states {
		if now.Sub(state.ObservedAt.UTC()) > PortfolioSnapshotMaxAge {
			continue
		}
		n, err := execution.Number(string(state.AdjustedEquity))
		if err != nil {
			return nil, fmt.Errorf("risk: stored adjusted equity of %s is invalid: %w", state.AccountKey, err)
		}
		total.Add(total, n)
	}
	return total, nil
}

// sums totals the holds of one set of rows. Only the requested side counts
// towards the directional limit.
func sums(rows []ReservationRecord, side string) (risk, margin, gross, directional *big.Rat, err error) {
	risk, margin, gross, directional = new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
	for _, row := range rows {
		r, err := execution.Number(string(row.Risk))
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("risk: stored reservation %s has invalid risk", row.RunID)
		}
		n, err := execution.Number(string(row.Notional))
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("risk: stored reservation %s has invalid notional", row.RunID)
		}
		m, err := execution.Number(string(row.Margin))
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("risk: stored reservation %s has invalid margin", row.RunID)
		}
		risk.Add(risk, r)
		gross.Add(gross, n)
		margin.Add(margin, m)
		if row.Side == side {
			directional.Add(directional, n)
		}
	}
	return risk, margin, gross, directional, nil
}

// checkLimit compares an already-summed value against a BPS limit of equity.
func checkLimit(name string, value, equity *big.Rat, bps int) error {
	limit := new(big.Rat).Mul(equity, big.NewRat(int64(bps), 10000))
	if value.Cmp(limit) <= 0 {
		return nil
	}
	return fmt.Errorf("risk: %s %s exceeds the policy limit of %s", name, formatRat(value), formatRat(limit))
}

func formatRat(r *big.Rat) string {
	s := strings.TrimRight(strings.TrimRight(r.FloatString(12), "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func sameHold(record ReservationRecord, r Reservation) bool {
	return record.RunID == r.RunID &&
		record.UserID == r.UserID &&
		record.PolicyID == r.PolicyID &&
		record.AccountKey == r.AccountKey &&
		record.PortfolioID == r.PortfolioID &&
		record.Symbol == r.Symbol &&
		record.Side == r.Side &&
		record.PolicyVersion == r.PolicyVersion &&
		record.Risk == r.Risk &&
		record.Notional == r.Notional &&
		record.Margin == r.Margin
}
