package freshness

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

var (
	widgets = github.Repo{Owner: "acme", Name: "widgets"}
	gadgets = github.Repo{Owner: "acme", Name: "gadgets"}
	t0      = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
)

// fixtureErr is the error a gh call answered with fixture name gives.
func fixtureErr(t *testing.T, name string) error {
	t.Helper()
	f := githubtest.MustLoad(t, name)
	err := github.Classify(github.DefaultHost, f.Result, f.Err)
	if err == nil {
		t.Fatalf("fixture %s classifies as success", name)
	}
	return err
}

func TestCacheFirstFetchAndInFlight(t *testing.T) {
	c := New()
	k := ListKey(widgets, IssueList, "is:open")
	if !c.Due(k, t0, time.Minute) {
		t.Fatal("a key never fetched is not due")
	}
	if !c.Begin(k) {
		t.Fatal("Begin on an idle key refused")
	}
	if c.Begin(k) {
		t.Fatal("a second Begin while the first is in flight started another fetch")
	}
	if c.Due(k, t0.Add(time.Hour), time.Minute) {
		t.Fatal("a key in flight is due")
	}
	e, ok := c.Get(k)
	if !ok || !e.Fetching || e.HasData() {
		t.Fatalf("in-flight entry = %+v, %v", e, ok)
	}
}

func TestCacheListPollsOnInterval(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		after    time.Duration
		due      bool
	}{
		{"fresh", time.Minute, 59 * time.Second, false},
		{"interval elapsed", time.Minute, time.Minute, true},
		{"longer interval", 2 * time.Minute, 90 * time.Second, false},
		{"manual only never polls", 0, 24 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			k := ListKey(widgets, PRList, "is:open")
			c.Begin(k)
			c.Done(k, "rows", nil, t0)
			e, _ := c.Get(k)
			if e.Value != "rows" || !e.FetchedAt.Equal(t0) || e.Err != nil || e.Fetching || e.Stale() {
				t.Fatalf("entry after success = %+v", e)
			}
			if got := c.Due(k, t0.Add(tc.after), tc.interval); got != tc.due {
				t.Fatalf("Due after %v at interval %v = %v, want %v", tc.after, tc.interval, got, tc.due)
			}
		})
	}
}

func TestCacheKeysAreSeparate(t *testing.T) {
	c := New()
	open := ListKey(widgets, IssueList, "is:open")
	closed := ListKey(widgets, IssueList, "is:closed")
	other := ListKey(gadgets, IssueList, "is:open")
	prs := ListKey(widgets, PRList, "is:open")
	c.Begin(open)
	c.Done(open, "open rows", nil, t0)
	for _, k := range []Key{closed, other, prs, DetailKey(widgets, 7), DiffKey(widgets, 7, "abc")} {
		if _, ok := c.Get(k); ok {
			t.Fatalf("result for %v landed under %v", open, k)
		}
	}
}

// TestCacheDetailAndDiff: a detail refetches only when a newer updatedAt is
// seen for it; a diff is keyed by head SHA and never refetched.
func TestCacheDetailAndDiff(t *testing.T) {
	c := New()
	d := DetailKey(widgets, 7)
	c.Begin(d)
	c.Done(d, versioned{"pr 7", t0}, nil, t0)
	if c.Due(d, t0.Add(24*time.Hour), time.Minute) {
		t.Fatal("a detail is polled on the interval")
	}
	c.Seen(d, t0)
	if c.Due(d, t0.Add(time.Second), time.Minute) {
		t.Fatal("the same updatedAt invalidated the detail")
	}
	c.Seen(d, t0.Add(time.Minute))
	if !c.Due(d, t0.Add(time.Second), time.Minute) {
		t.Fatal("a newer updatedAt did not invalidate the detail")
	}
	c.Begin(d)
	c.Done(d, versioned{"pr 7 v2", t0.Add(time.Minute)}, nil, t0.Add(2*time.Second))
	if c.Due(d, t0.Add(time.Hour), time.Minute) {
		t.Fatal("the refetched detail is still due")
	}

	// The Conversation of #7 follows the same row updatedAt as its detail.
	conv := ConversationKey(widgets, 7)
	c.Begin(conv)
	c.Done(conv, versioned{"conversation 7", t0.Add(time.Minute)}, nil, t0.Add(2*time.Second))
	if c.Due(conv, t0.Add(24*time.Hour), time.Minute) {
		t.Fatal("a Conversation is polled on the interval")
	}
	c.Seen(d, t0.Add(time.Minute))
	if c.Due(conv, t0.Add(time.Hour), time.Minute) {
		t.Fatal("the same updatedAt invalidated the Conversation")
	}
	c.Seen(d, t0.Add(2*time.Minute))
	if !c.Due(conv, t0.Add(time.Hour), time.Minute) || !c.Due(d, t0.Add(time.Hour), time.Minute) {
		t.Fatal("a newer row updatedAt did not invalidate both the detail and the Conversation")
	}
	if ConversationKey(github.Repo{Owner: "acme", Name: "widgets"}, 7) != conv {
		t.Fatal("the default host is not spelled out in a Conversation key")
	}

	diff := DiffKey(widgets, 7, "abc123")
	c.Begin(diff)
	c.Done(diff, "diff text", nil, t0)
	c.Seen(diff, t0.Add(time.Hour))
	if c.Due(diff, t0.Add(48*time.Hour), time.Minute) {
		t.Fatal("a diff (immutable per head SHA) became due")
	}
}

type versioned struct {
	v  string
	at time.Time
}

func (v versioned) Version() time.Time { return v.at }

// TestCacheTransientFailureBacksOff: the last good value stays, marked
// stale; retries back off from the interval and a success resets them.
func TestCacheTransientFailureBacksOff(t *testing.T) {
	for _, fixture := range []string{githubtest.Network, githubtest.Timeout, githubtest.ServerError} {
		t.Run(fixture, func(t *testing.T) {
			c := New()
			k := ListKey(widgets, IssueList, "is:open")
			c.Begin(k)
			c.Done(k, "rows", nil, t0)
			at := t0.Add(time.Minute)
			for i, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
				c.Begin(k)
				c.Done(k, nil, fixtureErr(t, fixture), at)
				e, _ := c.Get(k)
				if e.Value != "rows" || !e.Stale() || e.Failures != i+1 {
					t.Fatalf("failure %d: entry = %+v", i+1, e)
				}
				if c.Due(k, at.Add(wait-time.Second), time.Minute) {
					t.Fatalf("failure %d: due before %v", i+1, wait)
				}
				if !c.Due(k, at.Add(wait), time.Minute) {
					t.Fatalf("failure %d: not due after %v", i+1, wait)
				}
				at = at.Add(wait)
			}
			c.Begin(k)
			c.Done(k, "new rows", nil, at)
			e, _ := c.Get(k)
			if e.Value != "new rows" || e.Stale() || e.Failures != 0 || e.Err != nil {
				t.Fatalf("after success: %+v", e)
			}
		})
	}
}

// TestCacheNoAutomaticRetry: auth, SAML, gh missing and unknown-host
// failures wait for the user (r), with or without a cached value.
func TestCacheNoAutomaticRetry(t *testing.T) {
	for _, fixture := range []string{githubtest.GhMissing, githubtest.NotLoggedIn, githubtest.BadToken, githubtest.SAML, githubtest.NotFound} {
		t.Run(fixture, func(t *testing.T) {
			c := New()
			k := ListKey(widgets, IssueList, "is:open")
			c.Begin(k)
			c.Done(k, nil, fixtureErr(t, fixture), t0)
			e, _ := c.Get(k)
			if e.HasData() || e.Err == nil || e.Stale() {
				t.Fatalf("entry = %+v", e)
			}
			if c.Due(k, t0.Add(24*time.Hour), time.Minute) {
				t.Fatal("retried automatically")
			}
			if !c.Begin(k) {
				t.Fatal("a manual refresh could not start")
			}
		})
	}
}

// TestCacheIssuesDisabledSettles: a repository with Issues turned off is a
// settled empty state, not a failure to retry: no backoff refetches, only
// the user's refresh (r).
func TestCacheIssuesDisabledSettles(t *testing.T) {
	c := New()
	k := ListKey(widgets, IssueList, "is:open")
	c.Begin(k)
	c.Done(k, nil, fmt.Errorf("listing issues: %w", github.ErrIssuesDisabled), t0)
	for _, after := range []time.Duration{time.Minute, 10 * time.Minute, 24 * time.Hour} {
		if c.Due(k, t0.Add(after), time.Minute) {
			t.Fatalf("Issues-disabled list refetched automatically after %v", after)
		}
	}
	if !c.Begin(k) {
		t.Fatal("a manual refresh could not start")
	}
}

// TestCacheRateLimitStopsHost: a rate limit with a known reset stops every
// key on that host until the reset; other hosts keep polling.
func TestCacheRateLimitStopsHost(t *testing.T) {
	c := New()
	issues := ListKey(widgets, IssueList, "is:open")
	prs := ListKey(widgets, PRList, "is:open")
	ghe := ListKey(github.Repo{Host: "ghe.example.com", Owner: "acme", Name: "widgets"}, IssueList, "is:open")
	reset := t0.Add(20 * time.Minute)
	err := fixtureErr(t, githubtest.RateLimited)
	var ge *github.Error
	if !errors.As(err, &ge) {
		t.Fatal("rate-limited fixture is not a *github.Error")
	}
	ge.Reset = reset
	c.Begin(issues)
	c.Done(issues, nil, ge, t0)

	if until, ok := c.RateLimited(github.DefaultHost, t0.Add(time.Minute)); !ok || !until.Equal(reset) {
		t.Fatalf("RateLimited = %v, %v; want %v", until, ok, reset)
	}
	for _, at := range []time.Duration{time.Minute, 5 * time.Minute, 19 * time.Minute} {
		if c.Due(issues, t0.Add(at), time.Minute) || c.Due(prs, t0.Add(at), time.Minute) {
			t.Fatalf("polled %v into the rate limit", at)
		}
	}
	if !c.Due(ghe, t0.Add(time.Minute), time.Minute) {
		t.Fatal("another host stopped polling")
	}
	if !c.Due(issues, reset, time.Minute) || !c.Due(prs, reset, time.Minute) {
		t.Fatal("polling did not resume at the reset")
	}
	if _, ok := c.RateLimited(github.DefaultHost, reset); ok {
		t.Fatal("still rate-limited at the reset")
	}
}

// TestCacheRateLimitUnknownReset backs off like any transient failure.
func TestCacheRateLimitUnknownReset(t *testing.T) {
	c := New()
	k := ListKey(widgets, IssueList, "is:open")
	c.Begin(k)
	c.Done(k, nil, fixtureErr(t, githubtest.RateLimited), t0)
	if _, ok := c.RateLimited(github.DefaultHost, t0); ok {
		t.Fatal("rate-limited with no known reset")
	}
	if c.Due(k, t0.Add(59*time.Second), time.Minute) || !c.Due(k, t0.Add(time.Minute), time.Minute) {
		t.Fatal("unknown reset does not back off from the interval")
	}
}

// TestCacheCancelledFetch: a fetch cancelled on shutdown records nothing.
func TestCacheCancelledFetch(t *testing.T) {
	c := New()
	k := ListKey(widgets, IssueList, "is:open")
	c.Begin(k)
	c.Done(k, "rows", nil, t0)
	c.Begin(k)
	c.Done(k, nil, fmt.Errorf("running gh: %w", context.Canceled), t0.Add(time.Minute))
	e, _ := c.Get(k)
	if e.Fetching || e.Err != nil || e.Value != "rows" {
		t.Fatalf("entry after a cancelled fetch = %+v", e)
	}
}
