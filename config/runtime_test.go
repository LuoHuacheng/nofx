package config

import (
	"testing"
	"time"
)

func TestParseOrderSyncInterval_DefaultAndBounds(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"empty", "", 30 * time.Second},
		{"invalid", "bad", 30 * time.Second},
		{"negative", "-1", 30 * time.Second},
		{"too short", "1", 5 * time.Second},
		{"valid", "45", 45 * time.Second},
		{"too long", "3600", 15 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseOrderSyncInterval(tc.input); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
