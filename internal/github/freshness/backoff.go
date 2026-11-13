// Package freshness is the cache every forge view reads (spec #18 §5): one
// entry per Key, at most one fetch in flight per key, when the next
// automatic fetch is due, exponential backoff on transient failures and a
// stop while a host is rate-limited. It does no I/O and starts no
// goroutines: the caller fetches and reports the outcome, and passes the
// clock in.
package freshness

import "time"

// MaxBackoff is the longest wait between automatic retries after transient
// failures (unless the refresh interval itself is longer).
const MaxBackoff = 5 * time.Minute

// Backoff is how long to wait before the next automatic fetch after
// failures consecutive transient failures: the interval, doubled per
// failure after the first, capped at MaxBackoff (or at the interval when
// that is longer). An interval of 0 (manual refresh only) returns 0: no
// automatic retry.
func Backoff(interval time.Duration, failures int) time.Duration {
	if interval <= 0 {
		return 0
	}
	limit := max(MaxBackoff, interval)
	d := interval
	for i := 1; i < failures && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}
