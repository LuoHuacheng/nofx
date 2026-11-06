package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"nofx/trader/execution"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store persists risk policies and the adjusted-equity baseline of managed
// accounts. Every observation is one transaction: a caller that gets an error
// must assume no new risk was recorded.
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("risk: store requires a database")
	}
	if err := db.AutoMigrate(&policyRecord{}, &EquityState{}, &EquityObservation{}, &LedgerEvent{}); err != nil {
		return nil, fmt.Errorf("risk: migrate risk tables: %w", err)
	}
	return &Store{db: db}, nil
}

// Observe records one account snapshot plus any venue events seen since the
// previous snapshot, then recomputes the adjusted-equity baseline of every day
// that has observations. Recomputation is what makes a late cashflow correction
// land on the right day; peaks only ever rise, so a triggered drawdown latch is
// never cleared by a correction.
func (s *Store) Observe(ctx context.Context, a execution.ManagedAccount, events []execution.AccountEvent, now time.Time) error {
	if a.AccountKey == "" {
		return errors.New("risk: observation requires an account key")
	}
	if !a.Environment.Valid() {
		return errors.New("risk: observation requires a sandbox or live environment")
	}
	if _, err := execution.Number(string(a.Equity)); err != nil {
		return fmt.Errorf("risk: invalid observed equity: %w", err)
	}
	normalized, err := normalizeEvents(a.AccountKey, events)
	if err != nil {
		return err
	}
	observedAt := now.UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		obs := EquityObservation{
			AccountKey: a.AccountKey,
			Day:        observedAt.Format(ledgerDayLayout),
			ObservedAt: observedAt,
			Equity:     a.Equity,
		}
		if err := tx.Create(&obs).Error; err != nil {
			return fmt.Errorf("risk: record equity observation: %w", err)
		}
		if len(normalized) > 0 {
			// A re-observed event id keeps the first amount, so a venue correction
			// to an already-recorded event is not applied; record a correction as
			// a new event with a distinct id.
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&normalized).Error; err != nil {
				return fmt.Errorf("risk: record ledger events: %w", err)
			}
		}
		return s.recompute(tx, a.AccountKey, a.Environment)
	})
}

// normalizeEvents validates venue events before anything is written. Currency
// is deliberately not validated: an unverifiable currency is recorded and only
// makes the ledger incomplete, so the evidence is not lost.
func normalizeEvents(accountKey string, events []execution.AccountEvent) ([]LedgerEvent, error) {
	out := make([]LedgerEvent, 0, len(events))
	for _, e := range events {
		if e.ID == "" {
			return nil, errors.New("risk: ledger event requires an id")
		}
		if e.AccountKey != "" && e.AccountKey != accountKey {
			return nil, fmt.Errorf("risk: ledger event %s belongs to another account", e.ID)
		}
		if e.OccurredAt.IsZero() {
			return nil, fmt.Errorf("risk: ledger event %s requires an occurrence time", e.ID)
		}
		switch e.Kind {
		case KindCashflow, KindFunding, KindFee:
			if _, err := execution.Number(string(e.Amount)); err != nil {
				return nil, fmt.Errorf("risk: ledger event %s has an invalid amount", e.ID)
			}
		case KindFundingGap:
		default:
			return nil, fmt.Errorf("risk: ledger event %s has unknown kind %q", e.ID, e.Kind)
		}
		out = append(out, LedgerEvent{
			AccountKey: accountKey,
			EventID:    e.ID,
			Kind:       e.Kind,
			Amount:     e.Amount,
			Currency:   e.Currency,
			OccurredAt: e.OccurredAt.UTC(),
		})
	}
	return out, nil
}

func (s *Store) recompute(tx *gorm.DB, accountKey string, env execution.Environment) error {
	var events []LedgerEvent
	if err := tx.Where("account_key = ?", accountKey).Order("occurred_at").Find(&events).Error; err != nil {
		return fmt.Errorf("risk: load ledger events: %w", err)
	}
	var observations []EquityObservation
	if err := tx.Where("account_key = ?", accountKey).Order("observed_at").Order("id").Find(&observations).Error; err != nil {
		return fmt.Errorf("risk: load equity observations: %w", err)
	}
	var existing []EquityState
	if err := tx.Where("account_key = ?", accountKey).Find(&existing).Error; err != nil {
		return fmt.Errorf("risk: load equity states: %w", err)
	}
	stored := map[string]EquityState{}
	for _, st := range existing {
		stored[st.Day] = st
	}

	// Verified cashflow moves the baseline; fees and funding are already inside
	// the venue equity, so subtracting them again would invent losses.
	type cashflow struct {
		at     time.Time
		amount *big.Rat
	}
	var cashflows []cashflow
	var marks []string
	for _, e := range events {
		if e.Currency != ledgerCurrency {
			// Unverifiable currency: retained for audit, never converted, and it
			// marks exactly its own UTC day incomplete.
			marks = append(marks, e.OccurredAt.Format(ledgerDayLayout))
			continue
		}
		switch e.Kind {
		case KindCashflow:
			n, err := execution.Number(string(e.Amount))
			if err != nil {
				return fmt.Errorf("risk: ledger event %s has an invalid amount", e.EventID)
			}
			cashflows = append(cashflows, cashflow{at: e.OccurredAt, amount: n})
		case KindFundingGap:
			marks = append(marks, e.OccurredAt.Format(ledgerDayLayout))
		}
	}
	sort.Strings(marks)
	// A gap marks exactly its own UTC day, never a later one. A verified page for
	// the same day must re-observe that day to clear the mark.
	incomplete := func(day string) bool {
		for _, m := range marks {
			if m == day {
				return true
			}
		}
		return false
	}

	days := make([]string, 0, len(observations))
	grouped := map[string][]EquityObservation{}
	for _, o := range observations {
		if _, ok := grouped[o.Day]; !ok {
			days = append(days, o.Day)
		}
		grouped[o.Day] = append(grouped[o.Day], o)
	}
	sort.Strings(days)

	cum := new(big.Rat)
	next := 0
	cumulative := func(t time.Time) *big.Rat {
		for next < len(cashflows) && !cashflows[next].at.After(t) {
			cum.Add(cum, cashflows[next].amount)
			next++
		}
		return new(big.Rat).Set(cum)
	}

	var peak *big.Rat
	for _, day := range days {
		dayObs := grouped[day]
		var first, last, net *big.Rat
		for _, o := range dayObs {
			equity, err := execution.Number(string(o.Equity))
			if err != nil {
				return fmt.Errorf("risk: stored observation has invalid equity: %w", err)
			}
			net = cumulative(o.ObservedAt)
			adjusted := new(big.Rat).Sub(equity, net)
			if first == nil {
				first = adjusted
			}
			last = adjusted
			if peak == nil || adjusted.Cmp(peak) > 0 {
				peak = new(big.Rat).Set(adjusted)
			}
		}
		if prev, ok := stored[day]; ok && prev.Peak != "" {
			p, err := execution.Number(string(prev.Peak))
			if err != nil {
				return fmt.Errorf("risk: stored peak is invalid: %w", err)
			}
			if peak == nil || p.Cmp(peak) > 0 {
				peak = p
			}
		}

		// A day start is proven by the first observation of the day: sandbox may
		// create the baseline, live needs it within the boundary grace window.
		start := ""
		if env == execution.EnvironmentSandbox || withinDayStartGrace(day, dayObs[0].ObservedAt) {
			d, err := execution.ExactDecimal(first)
			if err != nil {
				return fmt.Errorf("risk: day start is not an exact decimal: %w", err)
			}
			start = string(d)
		}
		adjusted, err := execution.ExactDecimal(last)
		if err != nil {
			return fmt.Errorf("risk: adjusted equity is not an exact decimal: %w", err)
		}
		netCashflow, err := execution.ExactDecimal(net)
		if err != nil {
			return fmt.Errorf("risk: net cashflow is not an exact decimal: %w", err)
		}
		peakValue, err := execution.ExactDecimal(peak)
		if err != nil {
			return fmt.Errorf("risk: peak equity is not an exact decimal: %w", err)
		}
		state := EquityState{
			AccountKey:     accountKey,
			Day:            day,
			AdjustedEquity: adjusted,
			DayStart:       execution.Decimal(start),
			Peak:           peakValue,
			NetCashflow:    netCashflow,
			Complete:       start != "" && !incomplete(day),
			ObservedAt:     dayObs[len(dayObs)-1].ObservedAt,
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&state).Error; err != nil {
			return fmt.Errorf("risk: record equity state: %w", err)
		}
	}
	return nil
}

func withinDayStartGrace(day string, at time.Time) bool {
	midnight, err := time.ParseInLocation(ledgerDayLayout, day, time.UTC)
	if err != nil {
		return false
	}
	return !at.Before(midnight) && at.Sub(midnight) <= DayStartGrace
}

// EquityState returns the persisted baseline of one account and UTC day.
func (s *Store) EquityState(ctx context.Context, accountKey, day string) (EquityState, error) {
	var state EquityState
	if err := s.db.WithContext(ctx).Where("account_key = ? AND day = ?", accountKey, day).First(&state).Error; err != nil {
		return EquityState{}, err
	}
	return state, nil
}

// PutPolicy stores the policy under its server-derived version. Callers cannot
// choose the version, and a stored policy can only be tightened: loosening a
// live limit would silently grant risk the owner never approved.
func (s *Store) PutPolicy(ctx context.Context, p Policy) (Policy, error) {
	if p.ID == "" {
		p.ID = policyID(p.UserID, p.AccountKey)
	}
	base, err := s.policyBaseline(ctx, p)
	if err != nil {
		return Policy{}, err
	}
	if err := tightenOnly(p, base); err != nil {
		return Policy{}, err
	}
	version, err := PolicyVersion(p)
	if err != nil {
		return Policy{}, err
	}
	if p.Version != "" && p.Version != version {
		return Policy{}, errors.New("risk: policy version does not match its content; re-derive it before use")
	}
	p.Version = version
	if err := ValidatePolicy(p); err != nil {
		return Policy{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return Policy{}, fmt.Errorf("risk: encode policy: %w", err)
	}
	record := policyRecord{ID: p.ID, UserID: p.UserID, AccountKey: p.AccountKey, Version: p.Version, Body: string(body)}
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"version", "body"}),
		}).
		Create(&record).Error; err != nil {
		return Policy{}, fmt.Errorf("risk: store policy: %w", err)
	}
	return p, nil
}

func (s *Store) policyBaseline(ctx context.Context, p Policy) (Policy, error) {
	stored, err := s.Policy(ctx, p.UserID, p.AccountKey)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Policy{}, err
	}
	return DefaultPolicy(p.UserID, p.AccountKey, p.PortfolioID, p.Venue, p.Environment), nil
}

func tightenOnly(p, base Policy) error {
	if p.Venue != base.Venue || p.Environment != base.Environment || p.PortfolioID != base.PortfolioID {
		return errors.New("risk: a policy cannot change venue, environment or portfolio")
	}
	for _, limit := range []struct {
		name        string
		value, base int
	}{
		{"per-trade risk", p.PerTradeRiskBPS, base.PerTradeRiskBPS},
		{"account open risk", p.AccountOpenRiskBPS, base.AccountOpenRiskBPS},
		{"portfolio open risk", p.PortfolioOpenRiskBPS, base.PortfolioOpenRiskBPS},
		{"daily loss", p.DailyLossBPS, base.DailyLossBPS},
		{"drawdown", p.DrawdownBPS, base.DrawdownBPS},
		{"max margin", p.MaxMarginBPS, base.MaxMarginBPS},
		{"max gross", p.MaxGrossBPS, base.MaxGrossBPS},
		{"max directional", p.MaxDirectionalBPS, base.MaxDirectionalBPS},
		{"max leverage", p.MaxLeverage, base.MaxLeverage},
	} {
		if limit.value > limit.base {
			return fmt.Errorf("risk: policy %s can only be tightened, not raised from %d", limit.name, limit.base)
		}
	}
	// These two tighten upward: a larger funding reserve and a larger minimum
	// reward/risk are both more conservative.
	if p.FundingReserveBPS < base.FundingReserveBPS {
		return fmt.Errorf("risk: policy funding reserve can only be raised from %d bps", base.FundingReserveBPS)
	}
	minimum, err := execution.Number(string(p.MinRewardRisk))
	if err != nil {
		return fmt.Errorf("risk: policy minimum reward/risk is invalid: %w", err)
	}
	baseMinimum, err := execution.Number(string(base.MinRewardRisk))
	if err != nil {
		return fmt.Errorf("risk: stored policy minimum reward/risk is invalid: %w", err)
	}
	if minimum.Cmp(baseMinimum) < 0 {
		return errors.New("risk: policy minimum reward/risk can only be raised")
	}
	return nil
}

// Policy returns the stored policy of one account.
func (s *Store) Policy(ctx context.Context, user, accountKey string) (Policy, error) {
	var record policyRecord
	if err := s.db.WithContext(ctx).
		Where("user_id = ? AND account_key = ?", user, accountKey).
		First(&record).Error; err != nil {
		return Policy{}, err
	}
	var p Policy
	if err := json.Unmarshal([]byte(record.Body), &p); err != nil {
		return Policy{}, fmt.Errorf("risk: decode stored policy: %w", err)
	}
	return p, nil
}
