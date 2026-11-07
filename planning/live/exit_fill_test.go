package live

import (
	"context"
	"testing"
	"time"
)

// ackOnlyExitBroker acknowledges a market exit before it fills, the way OKX
// does, and reports the fill only on the next lookup.
type ackOnlyExitBroker struct{ *testBroker }

func (b *ackOnlyExitBroker) Place(ctx context.Context, o Order) (Order, error) {
	ack, err := b.testBroker.Place(ctx, o)
	if err != nil {
		return ack, err
	}
	if o.Role != "exit" {
		return ack, nil
	}
	b.testBroker.mu.Lock()
	filled := b.testBroker.orders[o.ClientID]
	filled.Status, filled.Filled = "FILLED", o.Quantity
	b.testBroker.orders[o.ClientID] = filled
	b.testBroker.account.Position = "0"
	b.testBroker.mu.Unlock()
	ack.Status, ack.Filled = "NEW", "0"
	return ack, nil
}

// A market exit is acknowledged before it fills, so the run has to read the
// venue fill back or every closed trade halts on an unfinished order.
func TestFlattenRecordsTheExitFill(t *testing.T) {
	c, b, _, p := setup(t, "market")
	broker := &ackOnlyExitBroker{testBroker: b}
	c.Factory = func(context.Context, string, string) (Broker, string, error) {
		return broker, "account", nil
	}
	b.fillEntryImmediately = true
	v := started(t, c, p)
	c.step(context.Background(), "alice", v.ID)
	b.mu.Lock()
	b.account.Position = b.orders[v.Orders[0].ClientID].Filled
	b.mu.Unlock()

	if _, err := c.Flatten(context.Background(), "alice", v.ID, "operator exit"); err != nil {
		t.Fatalf("flatten: %v", err)
	}
	got, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("run state is %q, want completed: %s", got.State, got.Error)
	}
	var exits int
	for _, o := range got.Orders {
		if o.Role != "exit" {
			continue
		}
		exits++
		if o.Status != "FILLED" || o.Filled != o.Quantity {
			t.Fatalf("exit order kept %q/%s; the venue fill was not recorded", o.Status, o.Filled)
		}
	}
	if exits != 1 {
		t.Fatalf("run holds %d exit orders, want 1", exits)
	}
	var leases int64
	c.DB.Model(&Lease{}).Count(&leases)
	if leases != 0 {
		t.Fatalf("%d leases survived a completed trade", leases)
	}
}

// A negative Interval leaves supervision to the caller, so a fixture can pace
// the run instead of the two-second worker.
func TestNegativeIntervalDefersToStepContext(t *testing.T) {
	c, b, _, p := setup(t, "limit")
	c.Interval = -1
	v := approved(t, c, p)

	if _, err := c.Execute(context.Background(), "alice", v.ID, v.ApprovalHash); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(b.placed) != 0 {
		t.Fatalf("a negative interval started a worker that placed %d orders", len(b.placed))
	}
	got, _ := c.Get("alice", v.ID)
	if got.State != "running" {
		t.Fatalf("run state is %q, want running", got.State)
	}
	c.Now = func() time.Time { return time.Now().UTC() }
	if c.StepContext(context.Background(), "alice", v.ID) {
		t.Fatal("the run finished on its first tick")
	}
	if len(b.placed) != 1 {
		t.Fatalf("first tick placed %d orders, want 1", len(b.placed))
	}
}
