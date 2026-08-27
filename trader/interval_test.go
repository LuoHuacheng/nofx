package trader

import (
	"testing"
	"time"
)

func TestResolveOrderSyncInterval_DefaultsToThirtySeconds(t *testing.T) {
	if got := resolveOrderSyncInterval(0); got != 30*time.Second {
		t.Fatalf("got %v, want 30s", got)
	}
}

func TestResolveOrderSyncInterval_ClampsUnsafeValues(t *testing.T) {
	cases := []struct {
		name  string
		input time.Duration
		want  time.Duration
	}{
		{"too short", time.Second, minOrderSyncInterval},
		{"too long", 2 * time.Hour, maxOrderSyncInterval},
		{"negative", -time.Second, defaultOrderSyncInterval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveOrderSyncInterval(tc.input); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdaptiveScanInterval_UsesBounds(t *testing.T) {
	base := 15 * time.Minute
	if got := adaptiveScanInterval(base, 0, 0.03); got != 30*time.Second {
		t.Fatalf("high volatility interval=%v, want 30s", got)
	}
	if got := adaptiveScanInterval(base, 1, 0); got != base/2 {
		t.Fatalf("open-position interval=%v, want %v", got, base/2)
	}
	if got := adaptiveScanInterval(base, 0, 0); got != base {
		t.Fatalf("quiet interval=%v, want %v", got, base)
	}
}
