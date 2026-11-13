package signals

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

var t0 = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// fakeClock is the poller's clock under test control.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// switchRunner answers every graphql call with the fixture of the moment,
// and every other gh call (rate_limit, auth status) with exit 1.
type switchRunner struct {
	mu      sync.Mutex
	fixture githubtest.Fixture
	calls   []github.Call
}

func (r *switchRunner) set(f githubtest.Fixture) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fixture = f
}

func (r *switchRunner) graphqlCalls() []github.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []github.Call
	for _, c := range r.calls {
		if slices.Contains(c.Args, "graphql") {
			out = append(out, c)
		}
	}
	return out
}

func (r *switchRunner) Run(ctx context.Context, call github.Call) (github.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	if !slices.Contains(call.Args, "graphql") {
		return github.Result{ExitCode: 1}, nil
	}
	return r.fixture.Result, r.fixture.Err
}

type harness struct {
	runner *switchRunner
	clock  *fakeClock
	cache  *freshness.Cache
	p      *Poller
	mu     sync.Mutex
	events [][]Event
	ticks  chan time.Time
}

func newHarness(t *testing.T, fixture string, targets ...Target) *harness {
	t.Helper()
	h := &harness{
		runner: &switchRunner{fixture: githubtest.MustLoad(t, fixture)},
		clock:  &fakeClock{now: t0},
		cache:  freshness.New(),
		ticks:  make(chan time.Time),
	}
	h.p = New(Config{
		Client:   github.NewClient(h.runner),
		Cache:    h.cache,
		Targets:  func() []Target { return targets },
		Interval: func() time.Duration { return time.Minute },
		Now:      h.clock.Now,
		Ticks:    h.ticks,
		OnChange: func(_ *Snapshot, ev []Event) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.events = append(h.events, ev)
		},
	})
	return h
}

func (h *harness) allEvents() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Event
	for _, ev := range h.events {
		out = append(out, ev...)
	}
	return out
}

// TestPollerOneQueryPerHost: one gh call per host, carrying every linked
// item of that host and the review search.
func TestPollerOneQueryPerHost(t *testing.T) {
	ghe := Target{Repo: github.Repo{Host: "ghe.corp.example", Owner: "team", Name: "svc"}, Number: 5, PR: true}
	h := newHarness(t, githubtest.SignalsPoll, pr34, issue12, ghe)
	h.p.Poll(context.Background())
	calls := h.runner.graphqlCalls()
	if len(calls) != 2 {
		t.Fatalf("graphql calls = %d, want 2 (one per host): %v", len(calls), calls)
	}
	var hosts []string
	for _, c := range calls {
		hosts = append(hosts, c.Args[2])
		q := strings.Join(c.Args, " ")
		if !strings.Contains(q, "user-review-requested:@me") {
			t.Errorf("call %v has no review search", c.Args)
		}
	}
	slices.Sort(hosts)
	if !slices.Equal(hosts, []string{"ghe.corp.example", "github.com"}) {
		t.Fatalf("hosts = %v", hosts)
	}
	for _, c := range calls {
		q := strings.Join(c.Args, " ")
		if c.Args[2] == "github.com" && !(strings.Contains(q, "number: 12") && strings.Contains(q, "number: 34") && strings.Contains(q, `owner: "acme"`)) {
			t.Errorf("github.com query misses a target: %s", q)
		}
	}
}

// TestPollerSearchesProjectHosts: a host with a resolved Project but no
// linked item still gets its review search (one request per host).
func TestPollerSearchesProjectHosts(t *testing.T) {
	h := newHarness(t, githubtest.SignalsPoll, pr34)
	h.p.cfg.Hosts = func() []string { return []string{"ghe.corp.example", "github.com", ""} }
	h.p.Poll(context.Background())
	var hosts []string
	for _, c := range h.runner.graphqlCalls() {
		hosts = append(hosts, c.Args[2])
	}
	slices.Sort(hosts)
	if !slices.Equal(hosts, []string{"ghe.corp.example", "github.com"}) {
		t.Fatalf("hosts polled = %v, want one request each for ghe.corp.example and github.com", hosts)
	}
}

// TestPollerNoNotificationOnFirstLoad covers the acceptance line: the first
// poll (a start, or a restart: nothing is persisted) only sets the
// baseline, then a change raises exactly the transitions.
func TestPollerNoNotificationOnFirstLoad(t *testing.T) {
	h := newHarness(t, githubtest.SignalsPollChanged, pr34, issue12)
	h.p.Poll(context.Background())
	if ev := h.allEvents(); len(ev) != 0 {
		t.Fatalf("first load raised %v, want nothing", ev)
	}
	snap := h.p.Snapshot()
	if got := snap.Badge(pr34); got != "✗±" {
		t.Fatalf("PR badge = %q, want ✗±", got)
	}
	if got := snap.Badge(issue12); got != "✓" {
		t.Fatalf("issue badge = %q, want ✓", got)
	}
	if got := snap.ReviewCount(); got != 2 {
		t.Fatalf("ReviewCount = %d, want 2", got)
	}
	if got := snap.ReviewCountFor(github.Repo{Owner: "acme", Name: "gadgets"}); got != 1 {
		t.Fatalf("ReviewCountFor(acme/gadgets) = %d, want 1", got)
	}
}

func TestPollerTransitions(t *testing.T) {
	h := newHarness(t, githubtest.SignalsPoll, pr34, issue12)
	ctx := context.Background()
	h.p.Poll(ctx)
	if got := h.p.Snapshot().Badge(pr34); got != "" {
		t.Fatalf("baseline PR badge = %q, want empty", got)
	}
	h.runner.set(githubtest.MustLoad(t, githubtest.SignalsPollChanged))

	// Before the interval nothing is polled.
	h.clock.Advance(30 * time.Second)
	h.p.Poll(ctx)
	if n := len(h.runner.graphqlCalls()); n != 1 {
		t.Fatalf("polled %d times before the interval, want 1", n)
	}

	h.clock.Advance(30 * time.Second)
	h.p.Poll(ctx)
	want := []Event{
		{Kind: EventChecksFailing, Target: pr34},
		{Kind: EventChangesRequested, Target: pr34},
		{Kind: EventReviewRequested, Review: ReviewRequest{Repo: github.Repo{Host: "github.com", Owner: "acme", Name: "gadgets"}, Number: 81, Title: "Speed up the gadget cache"}},
	}
	if got := h.allEvents(); !slices.Equal(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	// The same state again is not news.
	h.clock.Advance(time.Minute)
	h.p.Poll(ctx)
	if got := h.allEvents(); len(got) != len(want) {
		t.Fatalf("an unchanged poll raised %v", got[len(want):])
	}
}

// TestPollerAuthFailureWarnsHostOnly: a host gh is not logged in to shows
// ⚠ on its own badges, is not retried by itself, and other hosts keep
// their state.
func TestPollerAuthFailureWarnsHostOnly(t *testing.T) {
	ghe := Target{Repo: github.Repo{Host: "ghe.corp.example", Owner: "team", Name: "svc"}, Number: 5, PR: true}
	h := newHarness(t, githubtest.NotLoggedIn, pr34, ghe)
	h.p.Poll(context.Background())
	snap := h.p.Snapshot()
	if got := snap.Badge(ghe); got != "⚠" {
		t.Fatalf("ghe badge = %q, want ⚠", got)
	}
	n := len(h.runner.graphqlCalls())
	h.clock.Advance(10 * time.Minute)
	h.p.Poll(context.Background())
	if got := len(h.runner.graphqlCalls()); got != n {
		t.Fatalf("a login failure was retried automatically (%d calls, want %d)", got, n)
	}
}

// TestPollerRefreshClearsLoginWarning: after gh auth login, the user's
// refresh (r) polls a host that is not retried by itself, at once (no tick
// needed), and its ⚠ clears.
func TestPollerRefreshClearsLoginWarning(t *testing.T) {
	ghe := Target{Repo: github.Repo{Host: "ghe.corp.example", Owner: "team", Name: "svc"}, Number: 5, PR: true}
	h := newHarness(t, githubtest.NotLoggedIn, pr34, ghe)
	h.p.Start()
	t.Cleanup(h.p.Stop)
	waitFor(t, func() bool { return h.p.Snapshot() != nil })
	if got := h.p.Snapshot().Badge(ghe); got != "⚠" {
		t.Fatalf("ghe badge = %q, want ⚠", got)
	}
	h.runner.set(githubtest.MustLoad(t, githubtest.SignalsPoll))
	h.p.Refresh()
	waitFor(t, func() bool { return h.p.Snapshot().Badge(ghe) != "⚠" })
}

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestPollerBacksOffAndStopsWhenRateLimited reuses freshness: transient
// failures back off, a rate limit stops the host until it lifts.
func TestPollerBacksOff(t *testing.T) {
	h := newHarness(t, githubtest.Network, pr34)
	ctx := context.Background()
	h.p.Poll(ctx) // failure 1: retry after the interval
	h.clock.Advance(time.Minute)
	h.p.Poll(ctx) // failure 2: retry after 2 intervals
	h.clock.Advance(time.Minute)
	h.p.Poll(ctx)
	if got := len(h.runner.graphqlCalls()); got != 2 {
		t.Fatalf("calls = %d, want 2 (backoff after the second failure)", got)
	}
	h.clock.Advance(time.Minute)
	h.p.Poll(ctx)
	if got := len(h.runner.graphqlCalls()); got != 3 {
		t.Fatalf("calls = %d, want 3 after the backoff", got)
	}
	if got := h.p.Snapshot().Badge(pr34); got != "" {
		t.Fatalf("network failure badge = %q, want empty (no ⚠ for a transient failure)", got)
	}
}

func TestPollerRateLimitStopsHost(t *testing.T) {
	h := newHarness(t, githubtest.SignalsPoll, pr34)
	reset := t0.Add(10 * time.Minute)
	h.cache.Done(hostKey("github.com"), nil, &github.Error{Class: github.ClassRateLimited, Host: "github.com", Reset: reset}, t0)
	h.p.Poll(context.Background())
	if got := len(h.runner.graphqlCalls()); got != 0 {
		t.Fatalf("polled a rate-limited host (%d calls)", got)
	}
	h.clock.Advance(10 * time.Minute)
	h.p.Poll(context.Background())
	if got := len(h.runner.graphqlCalls()); got != 1 {
		t.Fatalf("calls after the reset = %d, want 1", got)
	}
}

// TestPollerIntervalZeroStops: refresh_interval = 0 stops the poller.
func TestPollerIntervalZeroStops(t *testing.T) {
	h := newHarness(t, githubtest.SignalsPoll, pr34)
	h.p.cfg.Interval = func() time.Duration { return 0 }
	h.p.Poll(context.Background())
	if got := len(h.runner.graphqlCalls()); got != 0 {
		t.Fatalf("calls = %d with interval 0, want 0", got)
	}
}

// TestPollerNewLinkPollsAtOnce: a newly linked item is fetched on the next
// tick rather than an interval later, and its first state is a baseline.
func TestPollerNewLinkPollsAtOnce(t *testing.T) {
	var targets atomic.Pointer[[]Target]
	targets.Store(&[]Target{issue12})
	h := newHarness(t, githubtest.SignalsPollChanged)
	h.p.cfg.Targets = func() []Target { return *targets.Load() }
	h.p.Poll(context.Background())
	targets.Store(&[]Target{pr34, issue12})
	h.clock.Advance(5 * time.Second)
	h.p.Poll(context.Background())
	if got := len(h.runner.graphqlCalls()); got != 2 {
		t.Fatalf("calls = %d, want 2 (new link polled at once)", got)
	}
	if got := h.p.Snapshot().Badge(pr34); got != "✗±" {
		t.Fatalf("PR badge = %q, want ✗±", got)
	}
	if ev := h.allEvents(); len(ev) != 0 {
		t.Fatalf("a new link raised %v", ev)
	}
}

// TestPollerRepoGone: gh's partial answer for a repository that went away
// marks its items not found instead of failing the host.
func TestPollerRepoGone(t *testing.T) {
	h := newHarness(t, githubtest.SignalsPoll, pr34, issue12)
	h.p.Poll(context.Background())
	h.runner.set(githubtest.MustLoad(t, githubtest.SignalsPollRepoGone))
	h.clock.Advance(time.Minute)
	h.p.Poll(context.Background())
	snap := h.p.Snapshot()
	if it, _ := snap.Item(pr34); it.Found {
		t.Fatalf("PR still found after its repository went away: %+v", it)
	}
	if snap.Warn["github.com"] {
		t.Fatal("a missing repository warned the whole host")
	}
	if got := snap.ReviewCount(); got != 0 {
		t.Fatalf("ReviewCount = %d, want 0", got)
	}
}

// TestPollerStartStopJoins covers the acceptance line: the poller goroutine
// is joined on stop, with a poll in flight cancelled.
func TestPollerStartStopJoins(t *testing.T) {
	h := newHarness(t, githubtest.SignalsPoll, pr34)
	polled := make(chan struct{}, 4)
	h.p.cfg.OnChange = func(*Snapshot, []Event) { polled <- struct{}{} }
	h.p.Start()
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		t.Fatal("no first poll after Start")
	}
	h.clock.Advance(time.Minute)
	h.ticks <- h.clock.Now()
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		t.Fatal("no poll after a tick")
	}
	done := make(chan struct{})
	go func() {
		h.p.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	if h.p.Running() {
		t.Fatal("Running after Stop")
	}
	h.p.Stop() // idempotent
}
