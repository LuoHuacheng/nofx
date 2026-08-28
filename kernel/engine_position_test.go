package kernel

import (
	"testing"
)

func TestValidatePartialCloseAction(t *testing.T) {
	valid := Decision{Symbol: "BTCUSDT", Action: "partial_close", ClosePct: 0.5}
	if err := validateDecisionForMode(&valid, 10000, 10, 10, 5, 5, false); err != nil {
		t.Fatalf("valid partial_close must pass: %v", err)
	}
}

func TestValidatePartialCloseRejectsBadPct(t *testing.T) {
	cases := []struct {
		name   string
		action string
		pct    float64
	}{
		{"zero", "partial_close", 0},
		{"negative", "partial_close", -0.1},
		{"over-one", "partial_close", 1.1},
	}
	for _, tc := range cases {
		d := Decision{Symbol: "BTCUSDT", Action: tc.action, ClosePct: tc.pct}
		if err := validateDecisionForMode(&d, 10000, 10, 10, 5, 5, false); err == nil {
			t.Fatalf("%s: expected validation error, got nil", tc.name)
		}
	}
}

func TestValidateAdjustStopLossAction(t *testing.T) {
	valid := Decision{Symbol: "BTCUSDT", Action: "adjust_stop_loss", StopLoss: 90000}
	if err := validateDecisionForMode(&valid, 10000, 10, 10, 5, 5, false); err != nil {
		t.Fatalf("valid adjust_stop_loss must pass: %v", err)
	}

	missing := Decision{Symbol: "BTCUSDT", Action: "adjust_stop_loss", StopLoss: 0}
	if err := validateDecisionForMode(&missing, 10000, 10, 10, 5, 5, false); err == nil {
		t.Fatal("adjust_stop_loss without stop_loss must be rejected")
	}
}

func TestValidateActionNormalization(t *testing.T) {
	// Uppercase / padded actions must normalize to lowercase before the whitelist check.
	d := Decision{Symbol: "BTCUSDT", Action: "  CLOSE_LONG "}
	if err := validateDecisionForMode(&d, 10000, 10, 10, 5, 5, false); err != nil {
		t.Fatalf("uppercase action must normalize: %v", err)
	}
	if d.Action != "close_long" {
		t.Fatalf("action must be normalized to lowercase, got %q", d.Action)
	}
}
