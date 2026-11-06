// Package risk owns unattended trading policies, risk reservations and risk
// decisions. It may depend on trader/execution only: planning and controller
// concerns stay outside this boundary.
package risk

import (
	"errors"
	"fmt"
	"nofx/trader/execution"
)

// Policy is the server-side limit set for one managed account. All BPS values
// are integers; money is an exact decimal string.
type Policy struct {
	ID          string                `json:"id"`
	UserID      string                `json:"user_id"`
	AccountKey  string                `json:"account_key"`
	PortfolioID string                `json:"portfolio_id"`
	Version     string                `json:"version"`
	Venue       string                `json:"venue"`
	Environment execution.Environment `json:"environment"`
	Symbols     []string              `json:"symbols"`
	Enabled     bool                  `json:"enabled"`

	PerTradeRiskBPS      int `json:"per_trade_risk_bps"`
	AccountOpenRiskBPS   int `json:"account_open_risk_bps"`
	PortfolioOpenRiskBPS int `json:"portfolio_open_risk_bps"`
	DailyLossBPS         int `json:"daily_loss_bps"`
	DrawdownBPS          int `json:"drawdown_bps"`
	MaxMarginBPS         int `json:"max_margin_bps"`
	MaxGrossBPS          int `json:"max_gross_bps"`
	MaxDirectionalBPS    int `json:"max_directional_bps"`
	MaxLeverage          int `json:"max_leverage"`
	FundingReserveBPS    int `json:"funding_reserve_bps"`

	MinRewardRisk execution.Decimal `json:"min_reward_risk"`
}

// Reservation states. An UNKNOWN state never releases budget.
const (
	StateReserved = "reserved"
	StateExposed  = "exposed"
	StateReleased = "released"
)

// Reservation holds budget for one entry. Identity is mandatory: an anonymous
// reservation cannot be attributed, audited or released.
type Reservation struct {
	UserID        string            `json:"user_id"`
	PolicyID      string            `json:"policy_id"`
	RunID         string            `json:"run_id"`
	AccountKey    string            `json:"account_key"`
	PortfolioID   string            `json:"portfolio_id"`
	Symbol        string            `json:"symbol"`
	Side          string            `json:"side"`
	PolicyVersion string            `json:"policy_version"`
	Risk          execution.Decimal `json:"risk"`
	Notional      execution.Decimal `json:"notional"`
	Margin        execution.Decimal `json:"margin"`
	State         string            `json:"state"`
}

func (r Reservation) Validate() error {
	for _, v := range []struct{ name, value string }{
		{"user", r.UserID},
		{"policy", r.PolicyID},
		{"run", r.RunID},
		{"account", r.AccountKey},
		{"portfolio", r.PortfolioID},
		{"symbol", r.Symbol},
		{"side", r.Side},
		{"policy version", r.PolicyVersion},
	} {
		if v.value == "" {
			return fmt.Errorf("reservation requires %s identity", v.name)
		}
	}
	if r.State != StateReserved && r.State != StateExposed && r.State != StateReleased {
		return fmt.Errorf("unknown reservation state %q", r.State)
	}
	for _, v := range []struct {
		name  string
		value execution.Decimal
	}{{"risk", r.Risk}, {"notional", r.Notional}, {"margin", r.Margin}} {
		if n, err := execution.Number(string(v.value)); err != nil || n.Sign() <= 0 {
			return fmt.Errorf("reservation %s must be a positive decimal", v.name)
		}
	}
	return nil
}

// Decision actions. flatten is a terminal risk action; it never resumes by itself.
const (
	ActionAllow        = "allow"
	ActionPauseEntries = "pause_entries"
	ActionFlatten      = "flatten"
)

type Decision struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func (d Decision) Validate() error {
	switch d.Action {
	case ActionAllow, ActionPauseEntries, ActionFlatten:
		return nil
	}
	return errors.New("unknown risk decision action")
}
