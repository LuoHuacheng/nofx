package risk

import (
	"nofx/trader/execution"
	"testing"
)

func defaultTestPolicy() Policy {
	return DefaultPolicy("alice", "binance:main", "portfolio-main", "binance", execution.EnvironmentSandbox)
}

func versioned(t *testing.T, p Policy) Policy {
	t.Helper()
	v, err := PolicyVersion(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Version = v
	return p
}

func TestDefaultPolicyMatchesDesignLimits(t *testing.T) {
	p := defaultTestPolicy()
	if p.Enabled {
		t.Fatal("default policy must keep automation disabled")
	}
	if p.MaxLeverage != 3 || p.MinRewardRisk != "2" || p.FundingReserveBPS != 10 {
		t.Fatalf("leverage/rr/funding defaults = %d/%s/%d", p.MaxLeverage, p.MinRewardRisk, p.FundingReserveBPS)
	}
	if p.PerTradeRiskBPS != 50 || p.AccountOpenRiskBPS != 100 || p.PortfolioOpenRiskBPS != 100 {
		t.Fatalf("risk defaults = %d/%d/%d", p.PerTradeRiskBPS, p.AccountOpenRiskBPS, p.PortfolioOpenRiskBPS)
	}
	if p.DailyLossBPS != 200 || p.DrawdownBPS != 500 || p.MaxMarginBPS != 3000 || p.MaxGrossBPS != 15000 || p.MaxDirectionalBPS != 10000 {
		t.Fatalf("loss/margin defaults = %d/%d/%d/%d/%d", p.DailyLossBPS, p.DrawdownBPS, p.MaxMarginBPS, p.MaxGrossBPS, p.MaxDirectionalBPS)
	}
	// A whitelist is mandatory, so the bare default only validates once set.
	if err := ValidatePolicy(p); err == nil {
		t.Fatal("default policy without a symbol whitelist was accepted")
	}
	p.Symbols = []string{"BTCUSDT"}
	p = versioned(t, p)
	if err := ValidatePolicy(p); err != nil {
		t.Fatal(err)
	}
	if p.Version == "" || p.ID == "" || p.UserID != "alice" || p.AccountKey != "binance:main" || p.PortfolioID != "portfolio-main" || p.Venue != "binance" || p.Environment != execution.EnvironmentSandbox {
		t.Fatalf("policy identity not populated: %+v", p)
	}
}

func TestValidatePolicyRejectsLooserLimits(t *testing.T) {
	cases := map[string]func(*Policy){
		"empty id":           func(p *Policy) { p.ID = "" },
		"empty user":         func(p *Policy) { p.UserID = "" },
		"empty account key":  func(p *Policy) { p.AccountKey = "" },
		"empty portfolio":    func(p *Policy) { p.PortfolioID = "" },
		"unknown venue":      func(p *Policy) { p.Venue = "kraken" },
		"invalid env":        func(p *Policy) { p.Environment = "paper" },
		"no symbols":         func(p *Policy) { p.Symbols = nil },
		"blank symbol":       func(p *Policy) { p.Symbols = []string{"BTCUSDT", " "} },
		"leverage zero":      func(p *Policy) { p.MaxLeverage = 0 },
		"leverage too loose": func(p *Policy) { p.MaxLeverage = 4 },
		"rr zero":            func(p *Policy) { p.MinRewardRisk = "0" },
		"rr too loose":       func(p *Policy) { p.MinRewardRisk = "1.5" },
		"rr invalid":         func(p *Policy) { p.MinRewardRisk = "abc" },
		"per trade zero":     func(p *Policy) { p.PerTradeRiskBPS = 0 },
		"per trade negative": func(p *Policy) { p.PerTradeRiskBPS = -50 },
		"account zero":       func(p *Policy) { p.AccountOpenRiskBPS = 0 },
		"portfolio negative": func(p *Policy) { p.PortfolioOpenRiskBPS = -1 },
		"daily zero":         func(p *Policy) { p.DailyLossBPS = 0 },
		"drawdown zero":      func(p *Policy) { p.DrawdownBPS = 0 },
		"margin zero":        func(p *Policy) { p.MaxMarginBPS = 0 },
		"gross zero":         func(p *Policy) { p.MaxGrossBPS = 0 },
		"directional zero":   func(p *Policy) { p.MaxDirectionalBPS = 0 },
		"funding negative":   func(p *Policy) { p.FundingReserveBPS = -1 },
		"funding too loose":  func(p *Policy) { p.FundingReserveBPS = 1001 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := versioned(t, defaultTestPolicy())
			mutate(&p)
			p = versioned(t, p)
			if err := ValidatePolicy(p); err == nil {
				t.Fatalf("%s accepted: %+v", name, p)
			}
		})
	}
}

func TestPolicyVersionCoversContent(t *testing.T) {
	p := versioned(t, defaultTestPolicy())
	if p.Version == "" || p.Version == p.ID {
		t.Fatalf("version not derived from content: %+v", p)
	}
	sorted := p
	sorted.Symbols = []string{"ETHUSDT", "BTCUSDT"}
	reordered := p
	reordered.Symbols = []string{"BTCUSDT", "ETHUSDT"}
	a, err := PolicyVersion(sorted)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PolicyVersion(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("symbol order changed the policy version: %s != %s", a, b)
	}
	changed := p
	changed.PerTradeRiskBPS = 60
	c, err := PolicyVersion(changed)
	if err != nil {
		t.Fatal(err)
	}
	if c == p.Version {
		t.Fatal("changing a limit did not change the policy version")
	}
	stale := p
	stale.PerTradeRiskBPS = 60
	if err := ValidatePolicy(stale); err == nil {
		t.Fatal("policy with a stale version was accepted")
	}
}

func TestReservationRequiresIdentity(t *testing.T) {
	r := Reservation{UserID: "alice", PolicyID: "policy-1", RunID: "run-1", AccountKey: "binance:main", PortfolioID: "portfolio-main", Symbol: "BTCUSDT", Side: "long", PolicyVersion: "v1", Risk: "0.5", Notional: "100", Margin: "33.33", State: StateReserved}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	anonymous := r
	anonymous.UserID = ""
	if err := anonymous.Validate(); err == nil {
		t.Fatal("anonymous reservation accepted")
	}
	unpolicied := r
	unpolicied.PolicyID = ""
	if err := unpolicied.Validate(); err == nil {
		t.Fatal("reservation without a policy accepted")
	}
	unrouted := r
	unrouted.RunID = ""
	if err := unrouted.Validate(); err == nil {
		t.Fatal("reservation without a run accepted")
	}
	badState := r
	badState.State = "unknown"
	if err := badState.Validate(); err == nil {
		t.Fatal("unknown reservation state accepted")
	}
	noRisk := r
	noRisk.Risk = "0"
	if err := noRisk.Validate(); err == nil {
		t.Fatal("reservation without risk accepted")
	}
}
