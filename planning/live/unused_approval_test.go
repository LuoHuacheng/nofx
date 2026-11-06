package live

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"nofx/planning"
	"nofx/planning/core"
)

func nextApprovalPlan(t *testing.T, c *Controller, m *testMarket, original core.Plan, name string) core.Plan {
	t.Helper()
	idea := original.Idea
	idea.Name = name
	next, err := core.Compile(m.book, original.Constraints, idea, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	store := planning.DraftStore{DB: c.DB, UserID: "alice", ExchangeID: "exchange", Venue: "binance"}
	if err = store.Add([]core.Plan{next}); err != nil {
		t.Fatal(err)
	}
	return next
}

func TestReplaceOnlyExactUnusedApproval(t *testing.T) {
	for _, scenario := range []string{"replace", "wrong_hash", "wrong_id", "started", "draft_changed", "other_user"} {
		t.Run(scenario, func(t *testing.T) {
			c, b, m, p := setup(t, "limit")
			var old View
			if scenario == "started" {
				old = started(t, c, p)
			} else {
				old = approved(t, c, p)
			}
			next := nextApprovalPlan(t, c, m, p, "new selection")
			id, hash, user := old.ID, old.ApprovalHash, "alice"
			switch scenario {
			case "wrong_hash":
				hash = "wrong"
			case "wrong_id":
				id = "other"
			case "other_user":
				user = "bob"
			case "draft_changed":
				c.DB.Model(&planning.Draft{}).Where("id = ?", next.ID).Update("status", "cancelled")
			}
			got, err := c.ApproveReplacing(context.Background(), user, next.ID, next.Hash, "APPROVE LIVE", id, hash)
			previous, _ := c.Get("alice", old.ID)
			var lease Lease
			if e := c.DB.First(&lease).Error; e != nil {
				t.Fatal(e)
			}
			if scenario == "replace" {
				if err != nil || got.State != "approved" || previous.State != "cancelled" || lease.RunID != got.ID {
					t.Fatalf("replacement failed: %v %s %s", err, got.State, previous.State)
				}
				if _, e := c.Execute(context.Background(), "alice", old.ID, old.ApprovalHash); e == nil {
					t.Fatal("superseded approval executed")
				}
			} else if err == nil || previous.State != old.State || lease.RunID != old.ID {
				t.Fatalf("changed an unrelated or active approval: %v %s", err, previous.State)
			}
			if len(b.placed) != 0 || len(b.cancelled) != 0 {
				t.Fatal("replacement touched exchange orders")
			}
		})
	}
}

func TestConcurrentReplacementsCannotReplaceAnUnreviewedApproval(t *testing.T) {
	c, _, m, p := setup(t, "limit")
	old := approved(t, c, p)
	plans := []core.Plan{nextApprovalPlan(t, c, m, p, "first"), nextApprovalPlan(t, c, m, p, "second")}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, next := range plans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.ApproveReplacing(context.Background(), "alice", next.ID, next.Hash, "APPROVE LIVE", old.ID, old.ApprovalHash)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected one confirmed replacement, got %d", succeeded)
	}
}

func TestExpiredUnusedApprovalReleasesDuringStateRead(t *testing.T) {
	c, b, _, p := setup(t, "limit")
	v := approved(t, c, p)
	v.Plan.Expires = time.Now().Add(-time.Minute)
	v.Plan.Hash = v.Plan.Digest()
	raw, _ := json.Marshal(v.Plan)
	if err := c.DB.Model(&Run{}).Where("id = ?", v.ID).Updates(map[string]any{"plan_json": string(raw), "plan_hash": v.Plan.Hash, "approval_hash": digest(v.Plan, v.UserID, v.ExchangeID, v.AccountKey)}).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := c.ListContext(context.Background(), "alice")
	var leases int64
	c.DB.Model(&Lease{}).Count(&leases)
	if err != nil || len(rows) != 1 || rows[0].State != "expired" || leases != 0 {
		t.Fatalf("expired approval still reserved: %v %+v", err, rows)
	}
	if len(b.placed) != 0 || len(b.cancelled) != 0 {
		t.Fatal("expiry changed exchange orders")
	}
}
