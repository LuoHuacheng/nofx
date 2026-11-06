package live

import (
	"context"
	"nofx/planning"
	"nofx/planning/core"
	"testing"
	"time"
)

func TestCheckStatusOnlyReleasesVerifiedClosedTrade(t *testing.T) {
	for _, scenario := range []string{"closed", "position", "open_order", "unknown", "triggered", "bad_position", "other_user"} {
		t.Run(scenario, func(t *testing.T) {
			c, b, _, p := setup(t, "limit")
			v := started(t, c, p)
			c.step(context.Background(), "alice", v.ID)
			v, _ = c.Get("alice", v.ID)
			entry := b.orders[v.Orders[0].ClientID]
			entry.Status, entry.Filled = "FILLED", entry.Quantity
			b.orders[entry.ClientID] = entry
			b.account.Position = entry.Quantity
			c.step(context.Background(), "alice", v.ID)
			v, _ = c.Get("alice", v.ID)
			v.State = "needs_attention"
			if err := c.save(&v); err != nil {
				t.Fatal(err)
			}
			for id, order := range b.orders {
				if order.Role != "entry" {
					order.Status = "CANCELED"
					b.orders[id] = order
				}
			}
			b.account.Position = "0"
			user := "alice"
			switch scenario {
			case "position":
				b.account.Position = entry.Quantity
			case "open_order":
				b.account.OpenOrders = 1
			case "unknown":
				b.lookupErr = true
			case "triggered":
				o := b.orders[v.Orders[1].ClientID]
				o.Status = "TRIGGERED"
				b.orders[o.ClientID] = o
			case "bad_position":
				b.account.Position = ""
			case "other_user":
				user = "bob"
			}
			placed, cancelled := len(b.placed), len(b.cancelled)
			out, err := c.CheckStatus(context.Background(), user, v.ID)
			var leases int64
			c.DB.Model(&Lease{}).Count(&leases)
			if scenario == "closed" {
				if err != nil || out.State != "completed" || leases != 0 {
					t.Fatalf("not released: %s %v leases=%d", out.State, err, leases)
				}
			} else if err == nil || leases != 1 {
				t.Fatalf("unsafe release: %v leases=%d", err, leases)
			}
			if len(b.placed) != placed || len(b.cancelled) != cancelled || len(c.workers) != 0 {
				t.Fatal("status check changed exchange orders or started execution")
			}
		})
	}
}

func TestCheckStatusKeepsUnusedApproval(t *testing.T) {
	c, b, _, p := setup(t, "limit")
	v := approved(t, c, p)
	out, err := c.CheckStatus(context.Background(), "alice", v.ID)
	var leases int64
	c.DB.Model(&Lease{}).Count(&leases)
	if err != nil || out.State != "approved" || leases != 1 || len(b.placed) != 0 {
		t.Fatal("unused approval was altered")
	}
}

func TestApproveChecksPreviousTradeBeforeReservingAccount(t *testing.T) {
	for _, scenario := range []string{"closed", "position", "open_order", "unknown", "fresh_fill", "unused_approval"} {
		t.Run(scenario, func(t *testing.T) {
			c, b, m, p := setup(t, "limit")
			var old View
			if scenario == "unused_approval" {
				old = approved(t, c, p)
			} else {
				old = started(t, c, p)
				c.step(context.Background(), "alice", old.ID)
				old, _ = c.Get("alice", old.ID)
				entry := b.orders[old.Orders[0].ClientID]
				entry.Status, entry.Filled = "FILLED", entry.Quantity
				b.orders[entry.ClientID] = entry
				if scenario != "fresh_fill" {
					b.account.Position = entry.Quantity
				}
				c.step(context.Background(), "alice", old.ID)
				for id, order := range b.orders {
					if order.Role != "entry" {
						order.Status = "CANCELED"
						b.orders[id] = order
					}
				}
				b.account.Position = "0"
				old, _ = c.Get("alice", old.ID)
				old.State = "needs_attention"
				if err := c.save(&old); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "position":
					b.account.Position = entry.Quantity
				case "open_order":
					b.account.OpenOrders = 1
				case "unknown":
					b.lookupErr = true
				}
			}
			idea := p.Idea
			idea.Name = "Next independent trade"
			next, err := core.Compile(m.book, p.Constraints, idea, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			store := planning.DraftStore{DB: c.DB, UserID: "alice", ExchangeID: "exchange", Venue: "binance"}
			if err = store.Add([]core.Plan{next}); err != nil {
				t.Fatal(err)
			}
			placed, cancelled := len(b.placed), len(b.cancelled)
			approved, err := c.Approve(context.Background(), "alice", next.ID, next.Hash, "APPROVE LIVE")
			var lease Lease
			if e := c.DB.First(&lease).Error; e != nil {
				t.Fatal(e)
			}
			previous, _ := c.Get("alice", old.ID)
			if scenario == "closed" {
				if err != nil || previous.State != "completed" || approved.State != "approved" || lease.RunID != approved.ID {
					t.Fatalf("closed trade not released: %v %s %s", err, previous.State, approved.State)
				}
			} else if err == nil || lease.RunID != old.ID || previous.State == "completed" {
				t.Fatalf("unresolved trade released: %v %+v", err, lease)
			}
			if len(b.placed) != placed || len(b.cancelled) != cancelled || len(c.workers) != 0 {
				t.Fatal("approval changed exchange orders")
			}
		})
	}
}

func TestManualCloseCleansOnlyConfirmedPlanExits(t *testing.T) {
	for _, scenario := range []string{"closed", "position", "unknown", "other_user", "running"} {
		t.Run(scenario, func(t *testing.T) {
			c, b, _, p := setup(t, "limit")
			v := started(t, c, p)
			c.step(context.Background(), "alice", v.ID)
			v, _ = c.Get("alice", v.ID)
			entry := b.orders[v.Orders[0].ClientID]
			entry.Status, entry.Filled = "FILLED", entry.Quantity
			b.orders[entry.ClientID] = entry
			b.account.Position = entry.Quantity
			c.step(context.Background(), "alice", v.ID)
			v, _ = c.Get("alice", v.ID)
			v.State = "stopping"
			c.save(&v)
			b.account.Position = "0"
			user := "alice"
			switch scenario {
			case "position":
				b.account.Position = entry.Quantity
			case "unknown":
				b.lookupErr = true
			case "other_user":
				user = "bob"
			case "running":
				v.State = "running"
				c.save(&v)
			}
			placed := len(b.placed)
			out, err := c.FinishManualClose(context.Background(), user, v.ID)
			if scenario == "closed" {
				if err != nil || out.State != "completed" || len(b.cancelled) != 2 {
					t.Fatal(out.State, err, b.cancelled)
				}
			} else if err == nil || len(b.cancelled) != 0 {
				t.Fatal("unsafe cleanup", err, b.cancelled)
			}
			if len(b.placed) != placed || len(c.workers) != 0 {
				t.Fatal("cleanup submitted or restarted execution")
			}
		})
	}
}
