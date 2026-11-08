package risk

import (
	"context"
	"testing"
	"time"

	"nofx/trader/execution"
)

// The same label must only combine accounts belonging to one owner/environment.
func TestPortfolioIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, user string
		env        execution.Environment
	}{
		{"same owner and environment", "alice", execution.EnvironmentSandbox},
		{"different owner", "bob", execution.EnvironmentSandbox},
		{"different environment", "alice", execution.EnvironmentLive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := ledgerStore(t)
			at := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
			var policies []Policy
			for i, user := range []string{"alice", tc.user} {
				env := execution.EnvironmentSandbox
				account := "first"
				if i == 1 {
					env, account = tc.env, "second"
				}
				p := DefaultPolicy(user, account, "shared", "binance", env)
				p.Symbols, p.Enabled, p.Version = []string{"BTCUSDT"}, true, ""
				p.PortfolioOpenRiskBPS = 25
				stored, err := s.PutPolicy(context.Background(), p)
				if err != nil {
					t.Fatal(err)
				}
				policies = append(policies, stored)
				observe(t, s, ledgerAccount(account, env, "100"), nil, at)
			}
			p := policies[0]
			hold := Reservation{UserID: p.UserID, PolicyID: p.ID, RunID: "isolation-hold", AccountKey: p.AccountKey, PortfolioID: p.PortfolioID, Symbol: "BTCUSDT", Side: "long", PolicyVersion: p.Version, Risk: "0.3", Notional: "30", Margin: "10", State: StateReserved}
			err := reserveInTx(t, s, hold, at)
			shared := tc.user == "alice" && tc.env == execution.EnvironmentSandbox
			if shared && err != nil {
				t.Fatalf("same-domain equity must combine: %v", err)
			}
			if !shared && err == nil {
				t.Fatal("borrowed another owner's/environment's equity for portfolio risk")
			}
			observe(t, s, ledgerAccount("first", execution.EnvironmentSandbox, "90"), nil, at.Add(time.Minute))
			observe(t, s, ledgerAccount("second", tc.env, "100"), nil, at.Add(time.Minute))
			if _, err := s.Evaluate(context.Background(), "alice", "first", at.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			verdict, err := s.Evaluate(context.Background(), tc.user, "second", at.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			want := ActionAllow
			if shared {
				want = ActionFlatten
			}
			if verdict.Action != want {
				t.Fatalf("second account: got %s, want %s: %s", verdict.Action, want, verdict.Reason)
			}
		})
	}
}
