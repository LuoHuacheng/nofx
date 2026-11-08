package automation

import (
	"testing"
	"time"
)

func TestSignalTimestampBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		created, expires time.Time
		valid            bool
	}{
		{"fresh", now, now.Add(30 * time.Second), true},
		{"last instant", now.Add(-30*time.Second + time.Nanosecond), now.Add(time.Nanosecond), true},
		{"expired", now.Add(-30 * time.Second), now, false},
		{"future skew allowed", now.Add(time.Second), now.Add(30 * time.Second), true},
		{"future skew rejected", now.Add(time.Second + time.Nanosecond), now.Add(30 * time.Second), false},
		{"lifetime too long", now, now.Add(30*time.Second + time.Nanosecond), false},
		{"missing creation", time.Time{}, now.Add(time.Second), false},
		{"reversed timestamps", now, now.Add(-time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (Signal{CreatedAt: tc.created, ExpiresAt: tc.expires}).Validate(now)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
