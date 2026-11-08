package live

import (
	"context"
	"errors"
	"testing"
	"time"

	"nofx/trader/execution"
)

func TestReviewBreakerPositionReadFailureRetainsSupervision(t *testing.T) {
	c, broker, venue, v := managedRestingEntry(t)
	acct := execution.ManagedAccount{AccountKey: "account", Venue: "binance", Environment: execution.EnvironmentLive, Equity: "9700", Available: "9700"}
	if err := c.Risk.Observe(context.Background(), acct, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	venue.accountErr = errors.New("temporary position read failure")
	stopped := c.step(context.Background(), "alice", v.ID)
	got, err := c.Get("alice", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped || got.State != "needs_attention" || got.Orders[0].Status != "CANCELED" {
		t.Fatalf("transient failure lost supervision or left entry live: stopped=%v state=%s entry=%s", stopped, got.State, got.Orders[0].Status)
	}
	venue.accountErr = nil
	c.step(context.Background(), "alice", v.ID)
	if exits := exitOrders(broker); len(exits) != 1 {
		t.Fatalf("recovered supervisor did not reduce exactly once: %+v", exits)
	}
}
