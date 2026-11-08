package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"nofx/trader/execution"
	"sort"
)

// Defaults from the design table. Automation is disabled until an owner
// explicitly enables it; every limit can only be tightened from here.
const (
	DefaultPerTradeRiskBPS      = 50
	DefaultAccountOpenRiskBPS   = 100
	DefaultPortfolioOpenRiskBPS = 100
	DefaultDailyLossBPS         = 200
	DefaultDrawdownBPS          = 500
	DefaultMaxMarginBPS         = 3000
	DefaultMaxGrossBPS          = 15000
	DefaultMaxDirectionalBPS    = 10000
	DefaultMaxLeverage          = 3
	DefaultFundingReserveBPS    = 10
	DefaultMinRewardRisk        = "2"
	MaxFundingReserveBPS        = 1000
	MaxPolicyLeverage           = 3
)

// portfolioScope is internal: identical client labels never join different owners or environments.
func portfolioScope(p Policy) string {
	b, _ := json.Marshal([]string{p.UserID, string(p.Environment), p.PortfolioID})
	h := sha256.Sum256(b)
	return "portfolio:" + hex.EncodeToString(h[:])
}

var supportedVenues = map[string]bool{"binance": true, "okx": true, "bitget": true, "hyperliquid": true}

// DefaultPolicy builds the baseline for one managed account. Symbols are
// supplied by the caller because a whitelist is mandatory and never guessed.
func DefaultPolicy(user, accountKey, portfolioID, venue string, env execution.Environment) Policy {
	p := Policy{
		ID:                   policyID(user, accountKey),
		UserID:               user,
		AccountKey:           accountKey,
		PortfolioID:          portfolioID,
		Venue:                venue,
		Environment:          env,
		Symbols:              []string{},
		Enabled:              false,
		PerTradeRiskBPS:      DefaultPerTradeRiskBPS,
		AccountOpenRiskBPS:   DefaultAccountOpenRiskBPS,
		PortfolioOpenRiskBPS: DefaultPortfolioOpenRiskBPS,
		DailyLossBPS:         DefaultDailyLossBPS,
		DrawdownBPS:          DefaultDrawdownBPS,
		MaxMarginBPS:         DefaultMaxMarginBPS,
		MaxGrossBPS:          DefaultMaxGrossBPS,
		MaxDirectionalBPS:    DefaultMaxDirectionalBPS,
		MaxLeverage:          DefaultMaxLeverage,
		FundingReserveBPS:    DefaultFundingReserveBPS,
		MinRewardRisk:        DefaultMinRewardRisk,
	}
	p.Version, _ = PolicyVersion(p)
	return p
}

func policyID(user, accountKey string) string {
	h := sha256.Sum256([]byte("policy/" + user + "/" + accountKey))
	return hex.EncodeToString(h[:16])
}

// PolicyVersion hashes the policy content with Symbols canonically sorted, so
// a reordering is not a change but any limit change invalidates old approvals.
func PolicyVersion(p Policy) (string, error) {
	normalized := p
	normalized.Version = ""
	symbols := make([]string, len(p.Symbols))
	for n, s := range p.Symbols {
		symbols[n] = trimSymbol(s)
	}
	sort.Strings(symbols)
	normalized.Symbols = symbols
	b, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func trimSymbol(s string) string {
	out := ""
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '\n' {
			continue
		}
		out += string(c)
	}
	return out
}

// ValidatePolicy accepts only policies at least as tight as the design defaults.
func ValidatePolicy(p Policy) error {
	if p.ID == "" || p.UserID == "" || p.AccountKey == "" || p.PortfolioID == "" {
		return errors.New("policy requires id, user, account and portfolio identity")
	}
	if !supportedVenues[p.Venue] {
		return errors.New("unsupported policy venue")
	}
	if !p.Environment.Valid() {
		return errors.New("policy environment must be sandbox or live")
	}
	if len(p.Symbols) == 0 {
		return errors.New("policy requires a non-empty symbol whitelist")
	}
	seen := map[string]bool{}
	for _, s := range p.Symbols {
		t := trimSymbol(s)
		if t == "" {
			return errors.New("policy symbol whitelist contains a blank symbol")
		}
		if seen[t] {
			return fmt.Errorf("policy symbol whitelist repeats %s", t)
		}
		seen[t] = true
	}
	if p.MaxLeverage < 1 || p.MaxLeverage > MaxPolicyLeverage {
		return fmt.Errorf("policy leverage must be between 1x and %dx", MaxPolicyLeverage)
	}
	if minimum, err := execution.Number(string(p.MinRewardRisk)); err != nil || minimum.Cmp(big.NewRat(2, 1)) < 0 {
		return errors.New("policy minimum reward/risk must be at least 2")
	}
	for _, v := range []struct {
		name  string
		value int
	}{
		{"per-trade risk", p.PerTradeRiskBPS},
		{"account open risk", p.AccountOpenRiskBPS},
		{"portfolio open risk", p.PortfolioOpenRiskBPS},
		{"daily loss", p.DailyLossBPS},
		{"drawdown", p.DrawdownBPS},
		{"max margin", p.MaxMarginBPS},
		{"max gross", p.MaxGrossBPS},
		{"max directional", p.MaxDirectionalBPS},
	} {
		if v.value <= 0 {
			return fmt.Errorf("policy %s must be a positive basis-point limit", v.name)
		}
	}
	if p.FundingReserveBPS < 0 || p.FundingReserveBPS > MaxFundingReserveBPS {
		return fmt.Errorf("policy funding reserve must be between 0 and %d bps", MaxFundingReserveBPS)
	}
	version, err := PolicyVersion(p)
	if err != nil {
		return err
	}
	if p.Version != version {
		return errors.New("policy version does not match its content; re-derive it before use")
	}
	return nil
}
