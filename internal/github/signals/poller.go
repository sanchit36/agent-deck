package signals

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
)

// DefaultTick is how often the poller looks for a host that is due. Each
// host is still polled only once per refresh interval.
const DefaultTick = 5 * time.Second

// Config is what a Poller polls with.
type Config struct {
	// Client runs the gh calls.
	Client *github.Client
	// Cache decides when each host is due: the refresh interval, the
	// backoff after transient failures and the stop while a host is
	// rate-limited. Sharing the forge's cache shares its rate-limit stops.
	Cache *freshness.Cache
	// Targets returns the linked items to watch. It is called on the
	// poller goroutine and must not block.
	Targets func() []Target
	// Interval is [github] refresh_interval; 0 stops polling.
	Interval func() time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Ticks drives the loop; nil means a DefaultTick ticker.
	Ticks <-chan time.Time
	// OnChange is called on the poller goroutine after every pass that
	// polled a host, with the new snapshot and its transitions (none on
	// the first load). It must not block for long: Stop waits for it.
	OnChange func(snap *Snapshot, events []Event)
}

// Poller is the Attention signal poller (spec #18 §15): one batched
// GraphQL request per host (the linked items plus the review search) per
// refresh interval, on one goroutine of its own that runs while the TUI
// runs, attached or not. Readers take Snapshot, which never blocks.
type Poller struct {
	cfg  Config
	snap atomic.Pointer[Snapshot]
	// polled is, per host, the targets of its last successful poll, so a
	// new link is fetched at once.
	polled map[string]string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a stopped Poller.
func New(cfg Config) *Poller {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Cache == nil {
		cfg.Cache = freshness.New()
	}
	if cfg.Client == nil {
		cfg.Client = github.NewClient(nil)
	}
	return &Poller{cfg: cfg, polled: map[string]string{}}
}

// Snapshot is the latest published state; nil before the first poll.
func (p *Poller) Snapshot() *Snapshot { return p.snap.Load() }

// Start runs the poll loop on a goroutine until Stop. It polls at once,
// then on every tick. Starting a running poller does nothing.
func (p *Poller) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	go p.loop(ctx, p.done)
}

// Stop cancels the loop and a poll in flight, and waits for the goroutine
// to return. Safe to call more than once, and on a poller never started.
func (p *Poller) Stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Running reports whether the loop goroutine is running.
func (p *Poller) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancel != nil
}

func (p *Poller) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticks := p.cfg.Ticks
	if ticks == nil {
		t := time.NewTicker(DefaultTick)
		defer t.Stop()
		ticks = t.C
	}
	p.Poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			p.Poll(ctx)
		}
	}
}

// hostKey is a host's poll in the freshness cache.
func hostKey(host string) freshness.Key {
	return freshness.Key{Repo: github.Repo{Host: host}, Kind: freshness.Signals}
}

// Poll polls every host that is due, one after the other, and publishes
// the result. github.com is always polled for the review count; other
// hosts only while an item there is linked.
func (p *Poller) Poll(ctx context.Context) {
	interval := time.Duration(0)
	if p.cfg.Interval != nil {
		interval = p.cfg.Interval()
	}
	if interval <= 0 {
		return
	}
	byHost := map[string][]Target{github.DefaultHost: nil}
	if p.cfg.Targets != nil {
		for _, t := range p.cfg.Targets() {
			if t.Repo.Owner == "" || t.Repo.Name == "" || t.Number <= 0 {
				continue
			}
			t = t.normal()
			if !slices.Contains(byHost[t.Repo.Host], t) {
				byHost[t.Repo.Host] = append(byHost[t.Repo.Host], t)
			}
		}
	}

	prev := p.snap.Load()
	next := &Snapshot{Items: map[Target]Item{}, Reviews: map[string]HostReviews{}, Warn: map[string]bool{}}
	if prev != nil {
		maps.Copy(next.Reviews, prev.Reviews)
		maps.Copy(next.Warn, prev.Warn)
	}
	// Keep the last known state of every item still linked; drop the rest
	// so a link made again starts from a new baseline.
	for _, ts := range byHost {
		for _, t := range ts {
			if it, ok := prev.Item(t); ok {
				next.Items[t] = it
			}
		}
	}
	for host := range next.Reviews {
		if _, ok := byHost[host]; !ok {
			delete(next.Reviews, host)
			delete(next.Warn, host)
		}
	}

	polledAny := false
	for _, host := range sortedHosts(byHost) {
		if ctx.Err() != nil {
			return
		}
		ts := byHost[host]
		if !p.due(host, ts, interval) {
			continue
		}
		k := hostKey(host)
		if !p.cfg.Cache.Begin(k) {
			continue
		}
		items, reviews, err := p.pollHost(ctx, host, ts)
		p.cfg.Cache.Done(k, nil, err, p.cfg.Now())
		if errors.Is(err, context.Canceled) {
			return
		}
		polledAny = true
		if err != nil {
			if warns(err) {
				next.Warn[host] = true
			}
			continue
		}
		delete(next.Warn, host)
		maps.Copy(next.Items, items)
		next.Reviews[host] = reviews
		p.polled[host] = signature(ts)
	}
	if !polledAny && len(next.Items) == len(prev.items()) {
		return
	}
	p.snap.Store(next)
	if p.cfg.OnChange != nil {
		p.cfg.OnChange(next, Diff(prev, next))
	}
}

// due reports whether host should be polled now: the cache says so (first
// poll, interval elapsed, backoff over, rate limit lifted), or its linked
// items changed since its last good poll.
func (p *Poller) due(host string, ts []Target, interval time.Duration) bool {
	now := p.cfg.Now()
	k := hostKey(host)
	if p.cfg.Cache.Due(k, now, interval) {
		return true
	}
	sig, ok := p.polled[host]
	if !ok || sig == signature(ts) {
		return false
	}
	if _, limited := p.cfg.Cache.RateLimited(host, now); limited {
		return false
	}
	e, ok := p.cfg.Cache.Get(k)
	return ok && e.Err == nil && !e.Fetching
}

// pollHost runs host's one request. A reply that names a repository or
// item GitHub could not resolve still carries the rest: those items are
// just not found.
func (p *Poller) pollHost(ctx context.Context, host string, ts []Target) (map[Target]Item, HostReviews, error) {
	query, slots := buildQuery(ts)
	out, err := p.cfg.Client.API(ctx, host, github.Call{Args: []string{"graphql", "-f", "query=" + query}})
	if err != nil && github.ClassOf(err) != github.ClassNotFound {
		return nil, HostReviews{}, err
	}
	items, reviews, perr := parseReply(host, out, slots)
	if perr != nil {
		if err != nil {
			return nil, HostReviews{}, err
		}
		return nil, HostReviews{}, perr
	}
	return items, reviews, nil
}

// warns reports whether err is a login problem that puts ⚠ on the host's
// badges: one that will not clear by itself.
func warns(err error) bool {
	var ge *github.Error
	if !errors.As(err, &ge) {
		return false
	}
	switch ge.Class {
	case github.ClassGhMissing, github.ClassNotLoggedIn, github.ClassBadToken, github.ClassSAML, github.ClassUnknownHost:
		return true
	}
	return false
}

// signature names a host's set of targets.
func signature(ts []Target) string {
	labels := make([]string, 0, len(ts))
	for _, t := range ts {
		labels = append(labels, t.Repo.String()+t.Label())
	}
	slices.Sort(labels)
	return strings.Join(labels, " ")
}

func (s *Snapshot) items() map[Target]Item {
	if s == nil {
		return nil
	}
	return s.Items
}
