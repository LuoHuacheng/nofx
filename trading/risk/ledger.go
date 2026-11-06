package risk

import (
	"time"

	"nofx/trader/execution"
)

// Ledger event kinds accepted from execution.AccountEvent.Kind. Equity is
// reported after fees and funding, so only cashflow moves the equity baseline:
// fee and funding events are kept for attribution, audit and dedup only.
const (
	KindCashflow    = "cashflow"    // signed deposit (+) or withdrawal (-)
	KindFunding     = "funding"     // venue funding payment, already in equity
	KindFee         = "fee"         // trading fee, already in equity
	KindFundingGap  = "funding_gap" // funding history could not be paged
	ledgerCurrency  = "USDT"        // the only currency this version can verify
	ledgerDayLayout = "2006-01-02"  // UTC day key
)

// DayStartGrace is how close to 00:00:00 UTC an observation must be to prove
// the day's opening equity. An account polled less often cannot prove its UTC
// day start, so live entries stay blocked until the evidence exists.
const DayStartGrace = 5 * time.Minute

// EquityState is the persisted daily baseline of one managed account.
// AdjustedEquity is exchange equity minus cumulative net cashflow, DayStart is
// the adjusted equity at the UTC day boundary, and Peak is the running maximum
// of adjusted equity across all days: a late ledger correction may raise it but
// never lowers it, so an already-triggered drawdown latch is not cleared.
type EquityState struct {
	AccountKey     string            `gorm:"column:account_key;primaryKey" json:"account_key"`
	Day            string            `gorm:"column:day;primaryKey" json:"day"`
	AdjustedEquity execution.Decimal `gorm:"column:adjusted_equity;not null" json:"adjusted_equity"`
	DayStart       execution.Decimal `gorm:"column:day_start" json:"day_start"`
	Peak           execution.Decimal `gorm:"column:peak;not null" json:"peak"`
	NetCashflow    execution.Decimal `gorm:"column:net_cashflow;not null" json:"net_cashflow"`
	Complete       bool              `gorm:"column:complete;not null" json:"complete"`
	ObservedAt     time.Time         `gorm:"column:observed_at;not null" json:"observed_at"`
}

func (EquityState) TableName() string { return "managed_equity_states" }

// EquityObservation keeps the raw exchange equity of every snapshot so a late
// cashflow event can be re-attributed without rewriting the ledger itself.
type EquityObservation struct {
	ID         uint              `gorm:"primaryKey" json:"id"`
	AccountKey string            `gorm:"column:account_key;not null;index" json:"account_key"`
	Day        string            `gorm:"column:day;not null;index" json:"day"`
	ObservedAt time.Time         `gorm:"column:observed_at;not null" json:"observed_at"`
	Equity     execution.Decimal `gorm:"column:equity;not null" json:"equity"`
}

func (EquityObservation) TableName() string { return "managed_equity_observations" }

// LedgerEvent is one venue event. (AccountKey, EventID) is unique, so re-observing
// the same funding page never counts twice. Currency is retained even when it
// cannot be verified: such an event marks the ledger incomplete.
type LedgerEvent struct {
	AccountKey string            `gorm:"column:account_key;primaryKey" json:"account_key"`
	EventID    string            `gorm:"column:event_id;primaryKey" json:"event_id"`
	Kind       string            `gorm:"column:kind;not null" json:"kind"`
	Amount     execution.Decimal `gorm:"column:amount" json:"amount"`
	Currency   string            `gorm:"column:currency" json:"currency"`
	OccurredAt time.Time         `gorm:"column:occurred_at;not null;index" json:"occurred_at"`
}

func (LedgerEvent) TableName() string { return "managed_ledger_events" }

// policyRecord stores the server-derived policy version next to its body.
type policyRecord struct {
	ID         string `gorm:"primaryKey" json:"id"`
	UserID     string `gorm:"column:user_id;not null;uniqueIndex:managed_policy_owner,priority:1" json:"user_id"`
	AccountKey string `gorm:"column:account_key;not null;uniqueIndex:managed_policy_owner,priority:2" json:"account_key"`
	Version    string `gorm:"column:version;not null" json:"version"`
	Body       string `gorm:"column:body;type:text;not null" json:"-"`
}

func (policyRecord) TableName() string { return "managed_risk_policies" }
