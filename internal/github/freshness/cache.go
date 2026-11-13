package freshness

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// Kind is what a Key caches.
type Kind string

const (
	// IssueList is a Project's Issues list under one filter.
	IssueList Kind = "issues"
	// PRList is a Project's Pull Requests list under one filter.
	PRList Kind = "prs"
	// Detail is one Issue or Pull Request (they share GitHub's numbering).
	Detail Kind = "detail"
	// Diff is a Pull Request's diff at one head commit.
	Diff Kind = "diff"
	// Conversation is one Issue's or Pull Request's body and comments
	// (the overlay's Conversation tab).
	Conversation Kind = "conversation"
	// Signals is one host's Attention signal poll (Key.Repo names only
	// the host). It refetches on the refresh interval, like a list.
	Signals Kind = "signals"
)

// Key names one cached fetch (spec #18 §5): a list is Project + kind +
// filter, a detail or a Conversation Project + number, a diff Project +
// number + head SHA. Build keys with ListKey, DetailKey, ConversationKey and
// DiffKey.
type Key struct {
	Repo    github.Repo
	Kind    Kind
	Filter  string
	Number  int
	HeadSHA string
}

// ListKey is the key of repo's kind list (IssueList or PRList) under filter.
func ListKey(repo github.Repo, kind Kind, filter string) Key {
	return Key{Repo: normal(repo), Kind: kind, Filter: filter}
}

// DetailKey is the key of Issue or Pull Request number of repo.
func DetailKey(repo github.Repo, number int) Key {
	return Key{Repo: normal(repo), Kind: Detail, Number: number}
}

// ConversationKey is the key of the Conversation of Issue or Pull Request
// number of repo.
func ConversationKey(repo github.Repo, number int) Key {
	return Key{Repo: normal(repo), Kind: Conversation, Number: number}
}

// DiffKey is the key of Pull Request number's diff at headSHA.
func DiffKey(repo github.Repo, number int, headSHA string) Key {
	return Key{Repo: normal(repo), Kind: Diff, Number: number, HeadSHA: headSHA}
}

// normal spells the default host out, so github.com/o/r has one key.
func normal(r github.Repo) github.Repo {
	r.Host = r.HostOrDefault()
	return r
}

// polled reports whether the key refetches on the refresh interval: lists,
// the detail and the Attention signal poll do (the UI asks Due only about
// keys on screen, so only the open detail polls). A detail also refetches
// at once when a newer updatedAt is Seen for it; a PR's checks change
// without updatedAt, hence the poll. A Conversation follows updatedAt only,
// and a diff never changes.
func (k Key) polled() bool {
	return k.Kind == IssueList || k.Kind == PRList || k.Kind == Detail || k.Kind == Signals
}

// Versioned is a cached value that knows its GitHub updatedAt, so Seen can
// tell when it is out of date.
type Versioned interface{ Version() time.Time }

// Entry is a snapshot of one key's cache entry.
type Entry struct {
	// Value is the last successful result; it stays through failures.
	Value any
	// FetchedAt is when Value arrived; zero until the first success.
	FetchedAt time.Time
	// Err is the last fetch's failure, nil after a success.
	Err error
	// Failures counts consecutive failures since the last success.
	Failures int
	// Fetching is true while a fetch for the key is in flight.
	Fetching bool

	attemptAt time.Time
	invalid   bool
}

// HasData reports whether the entry holds a successful result.
func (e Entry) HasData() bool { return !e.FetchedAt.IsZero() }

// Stale reports whether the last fetch failed while an older result is
// still shown.
func (e Entry) Stale() bool { return e.HasData() && e.Err != nil }

// Cache holds one Entry per Key, in memory only. It is safe for concurrent
// use.
type Cache struct {
	mu      sync.Mutex
	entries map[Key]*Entry
	// limited is, per host, when its rate limit lifts.
	limited map[string]time.Time
}

// New returns an empty Cache.
func New() *Cache {
	return &Cache{entries: map[Key]*Entry{}, limited: map[string]time.Time{}}
}

// Get returns a snapshot of k's entry, false when k was never fetched.
func (c *Cache) Get(k Key) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Due reports whether an automatic fetch of k should start at now, given
// the refresh interval (0 = manual only): never while a fetch of k is in
// flight or k's host is rate-limited; at once for a key never fetched or
// invalidated; for a list or a detail, interval after its last success; after a
// transient failure, Backoff later; after any other failure, never (the
// user refreshes by hand).
func (c *Cache) Due(k Key, now time.Time, interval time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until, ok := c.limited[k.Repo.Host]; ok && now.Before(until) {
		return false
	}
	e, ok := c.entries[k]
	switch {
	case !ok:
		return true
	case e.Fetching:
		return false
	case e.Err != nil:
		if reset := resetAt(e.Err); !reset.IsZero() {
			return !now.Before(reset)
		}
		if !transient(e.Err) {
			return false
		}
		wait := Backoff(interval, e.Failures)
		return wait > 0 && !now.Before(e.attemptAt.Add(wait))
	case e.invalid:
		return true
	case k.polled() && interval > 0:
		return !now.Before(e.FetchedAt.Add(interval))
	}
	return false
}

// Begin marks a fetch of k in flight. It returns false when one already
// is: the caller joins that fetch instead of starting another.
func (c *Cache) Begin(k Key) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok {
		e = &Entry{}
		c.entries[k] = e
	}
	if e.Fetching {
		return false
	}
	e.Fetching = true
	return true
}

// Done records the outcome of k's fetch at now, under k whatever the
// caller shows by then. A success replaces the value; a failure keeps it
// and counts toward the backoff; a rate limit with a known reset stops
// k's host until then. A cancelled fetch (shutdown) records nothing.
func (c *Cache) Done(k Key, value any, err error, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok {
		e = &Entry{}
		c.entries[k] = e
	}
	e.Fetching = false
	if errors.Is(err, context.Canceled) {
		return
	}
	e.attemptAt = now
	if err != nil {
		e.Err = err
		e.Failures++
		if reset := resetAt(err); reset.After(now) {
			c.limited[k.Repo.Host] = reset
		}
		return
	}
	e.Value, e.FetchedAt, e.Err, e.Failures, e.invalid = value, now, nil, 0, false
	delete(c.limited, k.Repo.Host)
}

// Seen tells the cache GitHub reported updatedAt for k's item (from a list
// row). k is a DetailKey; the item's detail and Conversation cached at an
// older version become due.
func (c *Cache) Seen(k Key, updatedAt time.Time) {
	if k.Kind != Detail {
		return
	}
	conv := k
	conv.Kind = Conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range []Key{k, conv} {
		e, ok := c.entries[key]
		if !ok || !e.HasData() {
			continue
		}
		if v, ok := e.Value.(Versioned); !ok || updatedAt.After(v.Version()) {
			e.invalid = true
		}
	}
}

// RateLimited reports whether host is rate-limited at now, and until when.
func (c *Cache) RateLimited(host string, now time.Time) (time.Time, bool) {
	if host == "" {
		host = github.DefaultHost
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.limited[host]
	if !ok || !now.Before(until) {
		return time.Time{}, false
	}
	return until, true
}

// transient reports whether err may clear without the user doing anything.
// A failure that is not a classified gh error (a reply that did not parse,
// say) is retried with backoff too. Issues turned off for a repository is
// a settled state, not a failure: only the user's refresh asks again.
func transient(err error) bool {
	if errors.Is(err, github.ErrIssuesDisabled) {
		return false
	}
	var ge *github.Error
	if errors.As(err, &ge) {
		return ge.Transient()
	}
	return true
}

// resetAt is when a rate limit err reports lifts; zero when unknown or err
// is not a rate limit.
func resetAt(err error) time.Time {
	var ge *github.Error
	if errors.As(err, &ge) && ge.Class == github.ClassRateLimited {
		return ge.Reset
	}
	return time.Time{}
}
