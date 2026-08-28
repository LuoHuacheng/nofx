package backtest

import (
	"testing"
	"time"
)

func TestAccountOpenCloseLong(t *testing.T) {
	acct := NewAccount(1000, 0.0002, 0.001)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if err := acct.Open("BTCUSDT", "long", 500, 100, 2, now); err != nil {
		t.Fatalf("open long failed: %v", err)
	}
	pos := acct.PositionAt("BTCUSDT")
	if pos == nil {
		t.Fatal("position not opened")
	}
	if pos.Qty <= 0 {
		t.Fatalf("qty should be positive, got %f", pos.Qty)
	}
	// notional 500 - taker fee 0.5 = 499.5, qty = 4.995
	if diff := abs(pos.Qty - 4.995); diff > 1e-9 {
		t.Fatalf("qty = %f, want 4.995", pos.Qty)
	}
	// margin locked = 500/2 = 250, fee 0.5
	if diff := abs(acct.Balance() - (1000 - 250 - 0.5)); diff > 1e-9 {
		t.Fatalf("balance = %f, want %f", acct.Balance(), 1000-250-0.5)
	}

	// Mark up 10% -> unrealized +49.95
	acct.UpdateMark("BTCUSDT", 110)
	if diff := abs(acct.UnrealizedPnL() - 49.95); diff > 1e-9 {
		t.Fatalf("unrealized = %f, want 49.95", acct.UnrealizedPnL())
	}
	if diff := abs(acct.Equity() - 799.45); diff > 1e-9 {
		// equity = balance (749.5) + unrealized (49.95)
		t.Fatalf("equity = %f, want 799.45", acct.Equity())
	}

	// Close at 110: gross pnl = qty*10 = 49.95, fee = notional*0.001
	tr, err := acct.Close("BTCUSDT", 110, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if tr.PnL <= 0 {
		t.Fatalf("pnl = %f, want > 0", tr.PnL)
	}
	if acct.HasPosition("BTCUSDT") {
		t.Fatal("position should be closed")
	}
	// balance back to 1000 - open fee (0.5) + net pnl (close fee inside pnl)
	if diff := abs(acct.Balance() - (1000 - 0.5 + tr.PnL)); diff > 1e-9 {
		t.Fatalf("final balance = %f", acct.Balance())
	}
	if len(acct.Trades()) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(acct.Trades()))
	}
}

func TestAccountRejectsOversizeOpen(t *testing.T) {
	acct := NewAccount(100, 0.0002, 0.001)
	now := time.Now()
	// size 100k with lev 1 needs 100k margin -> rejected
	if err := acct.Open("BTCUSDT", "long", 100000, 100, 1, now); err == nil {
		t.Fatal("expected insufficient balance error")
	}
	if acct.HasPosition("BTCUSDT") {
		t.Fatal("no position should exist")
	}
}

func TestAccountRejectsDuplicatePosition(t *testing.T) {
	acct := NewAccount(1000, 0.0002, 0.001)
	now := time.Now()
	if err := acct.Open("BTCUSDT", "long", 100, 100, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := acct.Open("BTCUSDT", "short", 100, 100, 1, now); err == nil {
		t.Fatal("expected duplicate position error")
	}
}

func TestAccountFunding(t *testing.T) {
	acct := NewAccount(1000, 0.0002, 0.001)
	now := time.Now()
	if err := acct.Open("BTCUSDT", "long", 100, 100, 1, now); err != nil {
		t.Fatal(err)
	}
	acct.UpdateMark("BTCUSDT", 100)
	balBefore := acct.Balance()
	// qty = (100 - 0.1)/100 = 0.999; funding = 0.999*100*0.0001 = 0.00999
	acct.ApplyFunding("BTCUSDT", 0.0001, now)
	if diff := abs(acct.Balance() - (balBefore - 0.00999)); diff > 1e-9 {
		t.Fatalf("balance after funding = %f, want %f", acct.Balance(), balBefore-0.00999)
	}
	// Short receives funding
	acct2 := NewAccount(1000, 0.0002, 0.001)
	_ = acct2.Open("ETHUSDT", "short", 100, 100, 1, now)
	acct2.UpdateMark("ETHUSDT", 100)
	balBefore2 := acct2.Balance()
	acct2.ApplyFunding("ETHUSDT", 0.0001, now)
	if diff := abs(acct2.Balance() - (balBefore2 + 0.00999)); diff > 1e-9 {
		t.Fatalf("short balance after funding = %f, want %f", acct2.Balance(), balBefore2+0.00999)
	}
}

func TestAccountLiquidation(t *testing.T) {
	acct := NewAccount(1000, 0.0002, 0.001)
	now := time.Now()
	if err := acct.Open("BTCUSDT", "long", 500, 100, 2, now); err != nil {
		t.Fatal(err)
	}
	// liq price for long @100 lev 2 = 50
	pos := acct.PositionAt("BTCUSDT")
	if diff := abs(pos.LiqPrice - 50); diff > 1e-9 {
		t.Fatalf("liq price = %f, want 50", pos.LiqPrice)
	}
	acct.UpdateMark("BTCUSDT", 49)
	liq := acct.CheckLiquidation(now.Add(time.Hour))
	if len(liq) != 1 {
		t.Fatalf("expected 1 liquidation, got %d", len(liq))
	}
	if acct.HasPosition("BTCUSDT") {
		t.Fatal("position should be liquidated")
	}
	if !acct.Liquidated() {
		t.Fatal("liquidated flag should be set")
	}
	if liq[0].Position != "liquidation" {
		t.Fatalf("trade position tag = %q, want liquidation", liq[0].Position)
	}
	// loss on liquidation = qty*(50-100) = -249.75; margin returned; close fee charged
	wantBal := 1000 - 0.5 + (50-100)*4.995 - liq[0].Fee
	if diff := abs(acct.Balance() - wantBal); diff > 1e-9 {
		t.Fatalf("balance after liq = %f, want %f", acct.Balance(), wantBal)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestAccountClosePartial verifies the PnL/margin/funding split of a
// scale-out: half the position is realized, half the margin is released,
// funding attribution is split proportionally, and the remainder keeps
// its stop/protection and entry.
func TestAccountClosePartial(t *testing.T) {
	acct := NewAccount(1000, 0.0002, 0.001)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// open 500 USDT notional at 100, leverage 2 -> qty 4.995, margin 250, open fee 0.5
	if err := acct.Open("BTCUSDT", "long", 500, 100, 2, now); err != nil {
		t.Fatalf("open failed: %v", err)
	}
	acct.SetProtection("BTCUSDT", 95, 110)

	// funding paid once: rate 0.0001 at mark 100 -> 4.995*100*0.0001 = 0.04995
	acct.ApplyFunding("BTCUSDT", 0.0001, now.Add(time.Minute))

	// mark up to 110, then partial-close half
	acct.UpdateMark("BTCUSDT", 110)
	tr, err := acct.ClosePartial("BTCUSDT", 0.5, 110, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("partial close failed: %v", err)
	}

	pos := acct.PositionAt("BTCUSDT")
	if pos == nil {
		t.Fatal("remainder must stay open after partial close")
	}
	// half qty: 4.995 * 0.5 = 2.4975
	if diff := abs(pos.Qty - 2.4975); diff > 1e-9 {
		t.Fatalf("remaining qty = %f, want 2.4975", pos.Qty)
	}
	// half margin released: 250 * 0.5 = 125 remaining
	if diff := abs(pos.Margin - 125); diff > 1e-9 {
		t.Fatalf("remaining margin = %f, want 125", pos.Margin)
	}
	// half funding attribution released
	if diff := abs(pos.FundingPaid - 0.04995*0.5); diff > 1e-9 {
		t.Fatalf("remaining funding attribution = %f", pos.FundingPaid)
	}
	// protection survives the scale-out
	if pos.StopLoss != 95 || pos.TakeProfit != 110 {
		t.Fatalf("protection must survive partial close, got sl=%v tp=%v", pos.StopLoss, pos.TakeProfit)
	}
	if pos.EntryPrice != 100 {
		t.Fatalf("entry must be unchanged, got %f", pos.EntryPrice)
	}
	// close trade: pricePnl = (110-100)*4.995*0.5 = 24.975; fee = 2.4975*110*0.001
	expectedPnL := 24.975 - 2.4975*110*0.001 - 0.04995*0.5
	if diff := abs(tr.PnL - expectedPnL); diff > 1e-9 {
		t.Fatalf("partial trade pnl = %f, want %f", tr.PnL, expectedPnL)
	}
	if tr.Position != "close" {
		t.Fatalf("partial close trade must be Position=close for metrics, got %q", tr.Position)
	}

	// pct >= 1 delegates to Close and empties the position
	if _, err := acct.ClosePartial("BTCUSDT", 1.0, 110, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("full-fraction partial close failed: %v", err)
	}
	if acct.HasPosition("BTCUSDT") {
		t.Fatal("pct=1 must fully close the position")
	}

	// invalid pct rejected
	if err := acct.Open("BTCUSDT", "long", 100, 100, 1, now); err != nil {
		t.Fatalf("re-open failed: %v", err)
	}
	if _, err := acct.ClosePartial("BTCUSDT", 0, 100, now); err == nil {
		t.Fatal("pct=0 must be rejected")
	}
}
