// Package live executes only finite, immutable, human-approved order baskets.
// Its broker interface has no autonomous strategy or AI capability.
package live

import (
	"context"
	"nofx/planning/core"
	"nofx/trader/execution"
	"time"
)

// Aliases preserve persisted run JSON while execution belongs to NOFX.
type Order = execution.Order
type Account = execution.Account

// ClientIDRouter supplies an exchange-native route before intent is persisted.
// Empty routes on historical records retain their original raw ID for lookup.
type ClientIDRouter interface{ RouteClientID(string) (string, error) }

func routedOrder(b Broker, o Order) (Order, error) {
	if router, ok := b.(ClientIDRouter); ok {
		id, e := router.RouteClientID(o.ClientID)
		if e != nil {
			return o, e
		}
		o.VenueClientID = id
	}
	return o, nil
}

// Broker preserves exact approved decimals and stable client IDs. Missing orders
// are never assumed filled and never authorize blind resubmission.
type Broker interface {
	Venue() string
	Account(context.Context, string) (Account, error)
	Prepare(context.Context, string) error
	Place(context.Context, Order) (Order, error)
	Lookup(context.Context, Order) (Order, error)
	Cancel(context.Context, Order) error
}
type Run struct {
	ID           string     `gorm:"primaryKey" json:"id"`
	UserID       string     `gorm:"index;not null" json:"-"`
	ExchangeID   string     `gorm:"index;not null" json:"exchange_id"`
	AccountKey   string     `gorm:"uniqueIndex:managed_policy_signal,where:authorization_mode = 'policy' AND signal_id <> ''" json:"-"`
	PlanID       string     `gorm:"index;not null" json:"plan_id"`
	PlanHash     string     `json:"plan_hash"`
	ApprovalHash string     `json:"approval_hash"`
	State        string     `gorm:"index" json:"state"`
	PlanJSON     string     `gorm:"type:text" json:"-"`
	OrdersJSON   string     `gorm:"type:text" json:"-"`
	ApprovedAt   time.Time  `json:"approved_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	Deadline     *time.Time `json:"deadline,omitempty"`
	// ProtectionState and ProtectionObservedAt are supervised-account protection
	// supervision; ProtectionObservedAt is when the position became unprotected.
	ProtectionState      string     `json:"protection_state,omitempty"`
	ProtectionObservedAt *time.Time `json:"protection_observed_at,omitempty"`
	UpdatedAt            time.Time  `json:"updated_at"`
	Error                string     `json:"error,omitempty"`
	// Managed marks a run whose account is an enabled execution.Binding; its
	// reservation identity is fixed at approval and never re-read at execution.
	Managed       bool   `gorm:"default:false" json:"-"`
	PolicyID      string `json:"-"`
	PolicyVersion string `json:"-"`
	PortfolioID   string `json:"-"`
	// AuthorizationMode "policy" marks an automatic authorization bound to a
	// policy version and a signal; empty is a historical human approval.
	AuthorizationMode string `json:"-"`
	SignalID          string `gorm:"uniqueIndex:managed_policy_signal,where:authorization_mode = 'policy' AND signal_id <> ''" json:"-"`
}

func (Run) TableName() string { return "planning_live_runs" }

type Lease struct {
	AccountKey string `gorm:"primaryKey"`
	RunID      string `gorm:"uniqueIndex"`
	UserID     string
	ExchangeID string `gorm:"index"`
}

func (Lease) TableName() string { return "planning_live_leases" }

type View struct {
	Run
	Plan   core.Plan `json:"plan"`
	Orders []Order   `json:"orders"`
}
type Factory func(context.Context, string, string) (Broker, string, error)
