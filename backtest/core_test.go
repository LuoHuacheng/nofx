package backtest

import (
	"testing"
	"time"
)

func TestRunLock(t *testing.T) {
	l := NewRunLock()
	if !l.TryAcquire("run1") {
		t.Fatal("first acquire should succeed")
	}
	if l.TryAcquire("run1") {
		t.Fatal("second acquire should fail (re-entrancy)")
	}
	if !l.Held("run1") {
		t.Fatal("run1 should be held")
	}
	if !l.TryAcquire("run2") {
		t.Fatal("different run should acquire")
	}
	l.Release("run1")
	if l.Held("run1") {
		t.Fatal("run1 should be released")
	}
	if !l.TryAcquire("run1") {
		t.Fatal("run1 should be re-acquirable after release")
	}
}

func TestRunRegistryLifecycle(t *testing.T) {
	r := NewRunRegistry()
	cfg := RunConfig{Name: "test"}
	r.Add(&RunRecord{ID: "r1", UserID: "u1", Status: StatusPending, Config: cfg})

	rec := r.Get("r1")
	if rec == nil || rec.Status != StatusPending {
		t.Fatalf("record = %+v", rec)
	}
	if !r.Update("r1", func(x *RunRecord) { x.Status = StatusRunning }) {
		t.Fatal("update should succeed")
	}
	if r.Get("r1").Status != StatusRunning {
		t.Fatal("status should be running")
	}
	if r.Update("nope", func(x *RunRecord) {}) {
		t.Fatal("update of missing run should fail")
	}
	r.Remove("r1")
	if r.Get("r1") != nil {
		t.Fatal("run should be removed")
	}
}

func TestCleanupExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	runs := []RetainedRun{
		{ID: "old", Status: StatusCompleted, EndedAt: now.Add(-31 * 24 * time.Hour)},
		{ID: "recent", Status: StatusCompleted, EndedAt: now.Add(-1 * 24 * time.Hour)},
		{ID: "failed-old", Status: StatusFailed, EndedAt: now.Add(-40 * 24 * time.Hour)},
		{ID: "active", Status: StatusRunning},   // never cleaned
		{ID: "paused", Status: StatusPaused},    // never cleaned
		{ID: "no-end", Status: StatusCompleted}, // EndedAt zero -> expired
	}
	expired := CleanupExpired(runs, RetentionDays*24*time.Hour, now)
	want := []string{"failed-old", "no-end", "old"}
	if len(expired) != len(want) {
		t.Fatalf("expired = %v, want %v", expired, want)
	}
	for i := range want {
		if expired[i] != want[i] {
			t.Fatalf("expired = %v, want %v", expired, want)
		}
	}
}

func TestEquityCurveSampling(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ec := NewEquityCurve(start, 1000)
	// bucket 1..4: no sample (interval 5)
	for i := 1; i <= 4; i++ {
		ec.Sample(start.Add(time.Duration(i)*time.Minute), 1000, 1000+float64(i))
	}
	if ec.Size() != 1 {
		t.Fatalf("size = %d, want 1 (initial only)", ec.Size())
	}
	// bucket 5: sample
	ec.Sample(start.Add(5*time.Minute), 1000, 1010)
	if ec.Size() != 2 {
		t.Fatalf("size = %d, want 2", ec.Size())
	}
	// forced sample in between
	ec.Force()
	ec.Sample(start.Add(6*time.Minute), 1000, 900)
	if ec.Size() != 3 {
		t.Fatalf("size = %d, want 3 (forced)", ec.Size())
	}
	// backdated sample dropped
	ec.Sample(start.Add(5*time.Minute), 1000, 999)
	if ec.Size() != 3 {
		t.Fatalf("backdated sample should be dropped, size = %d", ec.Size())
	}
}

func TestComputeMetrics(t *testing.T) {
	// Equity rises 0 -> 1100, one winning and one losing trade.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	equity := []EquityPoint{
		{Time: start, Equity: 1000},
		{Time: start.Add(1 * time.Hour), Equity: 1100},
		{Time: start.Add(2 * time.Hour), Equity: 1050},
	}
	trades := []BacktestTrade{
		{Time: start.Add(30 * time.Minute), Position: "close", PnL: 100},
		{Time: start.Add(90 * time.Minute), Position: "close", PnL: -50},
	}
	m := ComputeMetrics(equity, trades, 1000)
	if m.TradeCount != 2 {
		t.Fatalf("trade count = %d", m.TradeCount)
	}
	if diff := abs(m.TotalReturnPct - 5); diff > 1e-9 {
		t.Fatalf("return pct = %f, want 5", m.TotalReturnPct)
	}
	if diff := abs(m.WinRate - 50); diff > 1e-9 {
		t.Fatalf("win rate = %f, want 50", m.WinRate)
	}
	if diff := abs(m.ProfitFactor - 2); diff > 1e-9 {
		t.Fatalf("profit factor = %f, want 2", m.ProfitFactor)
	}
	// max drawdown: peak 1100, trough 1050 -> 50/1100 = 4.5454%
	if diff := abs(m.MaxDrawdownPct - 4.545454545454546); diff > 1e-9 {
		t.Fatalf("max drawdown = %f, want 4.545%%", m.MaxDrawdownPct)
	}
	if m.FinalBalance != 1050 || m.TotalPnL != 50 {
		t.Fatalf("final = %f pnl = %f", m.FinalBalance, m.TotalPnL)
	}
	// Sharpe for a monotonic up series must be > 0.
	if m.SharpeRatio <= 0 {
		t.Fatalf("sharpe = %f, want > 0", m.SharpeRatio)
	}
}

func TestComputeMetricsEmpty(t *testing.T) {
	m := ComputeMetrics(nil, nil, 1000)
	if m.TradeCount != 0 || m.WinRate != 0 || m.ProfitFactor != 0 {
		t.Fatalf("empty metrics = %+v", m)
	}
}
