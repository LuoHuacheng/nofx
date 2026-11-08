package automation

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestAccountStatusReturnsDatabaseErrors(t *testing.T) {
	for _, table := range []string{"managed_equity_states", "planning_live_runs", "managed_risk_reservations"} {
		t.Run(table, func(t *testing.T) {
			f := rig(t, nil)
			f.ctrl.Interval = -1
			f.source.signals = []Signal{{ID: "status-run", Symbol: "BTCUSDT", Idea: f.idea(), CreatedAt: f.now, ExpiresAt: f.now.Add(SignalExpiry)}}
			rt := f.runtime(t, f.source)
			if err := rt.Tick(context.Background(), "alice", "account"); err != nil {
				t.Fatal(err)
			}
			callback := "status_read_failure"
			if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(errors.New("status database unavailable"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.db.Callback().Query().Remove(callback) })
			if status, err := rt.AccountStatus(context.Background(), "alice", "account"); err == nil {
				t.Fatalf("reported successful status %q after %s query failure", status.State, table)
			}
		})
	}
}
