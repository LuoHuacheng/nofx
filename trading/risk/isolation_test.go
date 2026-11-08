package risk

import (
	"context"
	"testing"
	"time"

	"nofx/trader/execution"
)

func TestReviewPolicyIDCannotOverwriteAnotherOwner(t *testing.T) {
	store, _, _ := ledgerStore(t)
	alice := DefaultPolicy("alice", "alice-account", "alice-portfolio", "binance", execution.EnvironmentSandbox)
	alice.Symbols = []string{"BTCUSDT"}
	alice.Version = ""
	saved, err := store.PutPolicy(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	bob := DefaultPolicy("bob", "bob-account", "bob-portfolio", "binance", execution.EnvironmentSandbox)
	bob.ID = saved.ID // API overrides user/account but preserves client-supplied ID.
	bob.Symbols = []string{"BTCUSDT"}
	bob.Version = ""
	if _, err := store.PutPolicy(context.Background(), bob); err == nil {
		t.Fatal("forged policy ID was accepted")
	}
	got, err := store.Policy(context.Background(), "alice", "alice-account")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "alice" || got.AccountKey != "alice-account" {
		t.Fatalf("bob overwrote alice policy body: user=%s account=%s portfolio=%s", got.UserID, got.AccountKey, got.PortfolioID)
	}
}

func TestReviewPortfolioBreakerCannotCrossOwners(t *testing.T) {
	store, _, _ := ledgerStore(t)
	day := time.Date(2026, 11, 5, 0, 0, 5, 0, time.UTC)
	for _, user := range []string{"alice", "bob"} {
		account := user + "-account"
		p := DefaultPolicy(user, account, "shared-client-portfolio", "binance", execution.EnvironmentSandbox)
		p.Symbols = []string{"BTCUSDT"}
		p.Enabled = true
		p.Version = ""
		if _, err := store.PutPolicy(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		observe(t, store, ledgerAccount(account, execution.EnvironmentSandbox, "100"), nil, day)
	}
	now := day.Add(time.Minute)
	observe(t, store, ledgerAccount("alice-account", execution.EnvironmentSandbox, "90"), nil, now)
	observe(t, store, ledgerAccount("bob-account", execution.EnvironmentSandbox, "100"), nil, now)
	if _, err := store.Evaluate(context.Background(), "alice", "alice-account", now); err != nil {
		t.Fatal(err)
	}
	got, err := store.Evaluate(context.Background(), "bob", "bob-account", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != ActionAllow {
		t.Fatalf("unrelated bob account forced to %s: %s", got.Action, got.Reason)
	}
}
