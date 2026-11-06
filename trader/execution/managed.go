package execution

import (
	"context"
	"errors"
	"time"
)

// Environment separates rehearsal from real-money trading. An empty value is
// never a valid default: callers must choose the venue endpoint explicitly.
type Environment string

const (
	EnvironmentSandbox Environment = "sandbox"
	EnvironmentLive    Environment = "live"
)

func (e Environment) Valid() bool {
	return e == EnvironmentSandbox || e == EnvironmentLive
}

// ErrUnsupported marks managed capabilities an adapter does not implement yet.
var ErrUnsupported = errors.New("execution: capability unsupported")

// ManagedPosition reports base-asset quantities; venues with contract sizing
// convert inside the adapter and never leak contract counts.
type ManagedPosition struct {
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`
	Quantity    Decimal `json:"quantity"`
	Entry       Decimal `json:"entry"`
	Mark        Decimal `json:"mark"`
	Liquidation Decimal `json:"liquidation"`
	Margin      Decimal `json:"margin"`
	StopPrice   Decimal `json:"stop_price,omitempty"`
}

type ManagedAccount struct {
	AccountKey   string            `json:"account_key"`
	Venue        string            `json:"venue"`
	Environment  Environment       `json:"environment"`
	PositionMode string            `json:"position_mode"`
	MarginMode   string            `json:"margin_mode"`
	Equity       Decimal           `json:"equity"`
	Available    Decimal           `json:"available"`
	MarginUsed   Decimal           `json:"margin_used"`
	Positions    []ManagedPosition `json:"positions"`
	Orders       []Order           `json:"orders"`
	ObservedAt   time.Time         `json:"observed_at"`
}

type AccountEvent struct {
	ID         string    `json:"id"`
	AccountKey string    `json:"account_key"`
	Kind       string    `json:"kind"`
	Symbol     string    `json:"symbol"`
	Order      Order     `json:"order"`
	Amount     Decimal   `json:"amount"`
	Currency   string    `json:"currency"`
	OccurredAt time.Time `json:"occurred_at"`
}

type Capabilities struct {
	SafeStopAmend bool
	PrivateEvents bool
}

// ManagedTrader is the unattended-execution contract. Snapshots and settings
// verification must reflect the venue or fail; they never degrade to defaults.
type ManagedTrader interface {
	Trader
	RouteClientID(string) (string, error)
	PrepareExecutionLeverage(context.Context, string, int) error
	ManagedSnapshot(context.Context) (ManagedAccount, error)
	VerifyManagedSettings(context.Context, string, int) error
	ManagedCapabilities() Capabilities
	AmendStop(context.Context, Order, Decimal) (Order, error)
	Events(context.Context, func(AccountEvent) error) error
}
