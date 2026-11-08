package live

import (
	"context"
	"testing"
	"time"
)

func TestPolicySignalCannotExpireIntoFreshExecution(t *testing.T) {
	c, b, _, plan, store := policySetup(t)
	c.Interval = -1
	now := time.Now().UTC()
	c.Now = func() time.Time { return now }
	expiry := now.Add(30 * time.Second)
	v, err := c.AuthorizePolicy(context.Background(), "alice", plan.ID, plan.Hash, policyOf(t, store).Version, "signal-before-delay", expiry)
	if err != nil {
		t.Fatal(err)
	}
	now = expiry
	if _, err := c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); err == nil {
		t.Fatal("expired signal started an execution")
	}
	if entryPOSTs(b) != 0 {
		t.Fatal("expired signal submitted an entry")
	}
}

func TestPolicySignalExpiryIsBoundToApproval(t *testing.T) {
	c, _, _, plan, store := policySetup(t)
	v := authorize(t, c, plan, policyOf(t, store).Version, "signal-to-tamper")
	if err := c.DB.Model(&Run{}).Where("id = ?", v.ID).Update("signal_expires_at", time.Now().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("alice", v.ID); err == nil {
		t.Fatal("tampered signal expiry retained a valid approval digest")
	}
}
