package live

import (
	"context"
	"testing"
	"time"
)

func TestPolicyRunHoldDeadlineSurvivesRecovery(t *testing.T) {
	c, broker, _, v := managedRestingEntry(t)
	v.AuthorizationMode = "policy"
	started := time.Now().UTC().Add(-24*time.Hour - time.Second)
	v.StartedAt = &started
	v.State = "needs_attention" // same persisted state produced by a process restart
	if err := c.DB.Model(&Run{}).Where("id = ?", v.ID).Updates(map[string]any{"authorization_mode": "policy", "started_at": started, "state": v.State, "approval_hash": v.approvalDigest(v.Plan)}).Error; err != nil {
		t.Fatal(err)
	}
	c.step(context.Background(), "alice", v.ID)
	got, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	exits := 0
	for _, order := range got.Orders {
		if order.Role == "exit" {
			exits++
		}
	}
	if exits != 1 {
		t.Fatalf("24h position was not reduced after recovery: exits=%d state=%s error=%s orders=%+v", exits, got.State, got.Error, got.Orders)
	}
	if len(broker.cancelled) == 0 {
		t.Fatal("timed exit left the unfilled entry open")
	}
}
