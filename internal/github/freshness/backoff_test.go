package freshness

import (
	"testing"
	"time"
)

// TestBackoff pins spec #18 §5: transient failures back off exponentially
// from the refresh interval up to 5 minutes; manual-only (interval 0) never
// retries on its own; an interval longer than 5 minutes is never shortened.
func TestBackoff(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		failures int
		want     time.Duration
	}{
		{"no failure", time.Minute, 0, time.Minute},
		{"first failure waits one interval", time.Minute, 1, time.Minute},
		{"second doubles", time.Minute, 2, 2 * time.Minute},
		{"third doubles again", time.Minute, 3, 4 * time.Minute},
		{"fourth caps at 5 min", time.Minute, 4, 5 * time.Minute},
		{"many stay at 5 min", time.Minute, 60, 5 * time.Minute},
		{"30 s interval", 30 * time.Second, 3, 2 * time.Minute},
		{"30 s interval caps", 30 * time.Second, 5, 5 * time.Minute},
		{"interval above cap stays", 10 * time.Minute, 3, 10 * time.Minute},
		{"manual only", 0, 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Backoff(tc.interval, tc.failures); got != tc.want {
				t.Fatalf("Backoff(%v, %d) = %v, want %v", tc.interval, tc.failures, got, tc.want)
			}
		})
	}
}
