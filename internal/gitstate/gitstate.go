// Package gitstate reads the local git state of a checkout (branch, upstream,
// ahead/behind counts, dirty) and caches it per checkout path, so the TUI can
// show it without ever running git on the render path.
//
// One `git status --porcelain=v2 --branch` call per checkout, with a 5 s
// timeout, answers everything. Callers fetch asynchronously (a tea.Cmd) and
// read the Cache when rendering.
package gitstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/childenv"
)

const (
	// TTL is how long a fetched state is served before a new fetch starts.
	TTL = 10 * time.Second
	// CallTimeout bounds every git call.
	CallTimeout = 5 * time.Second
)

// State is the local git state of one checkout.
type State struct {
	// Repo is the checkout's top-level directory (where .git or .jj lives).
	Repo string
	// Branch is the checked-out branch, or "(detached)".
	Branch string
	// Upstream is the tracked branch (e.g. "origin/main"); empty when none.
	Upstream string
	// HasCounts is true when Ahead/Behind are known (the upstream exists).
	HasCounts bool
	Ahead     int
	Behind    int
	// Dirty is true for any tracked change or untracked file.
	Dirty bool
	// JJ marks a Jujutsu workspace; v1 shows no git state for it.
	JJ bool
	// NotRepo marks a path that is in no git repository.
	NotRepo bool
	// Err is set when the git call failed or timed out.
	Err error
}

// errMissingPath reports a checkout path that no longer exists.
var errMissingPath = errors.New("checkout path does not exist")

// Fetch reads the state of the checkout at path. It never panics and never
// returns a partial error: failures are reported in State.Err.
func Fetch(ctx context.Context, path string) State {
	abs, err := filepath.Abs(path)
	if err != nil {
		return State{Err: fmt.Errorf("resolving checkout path %q: %w", path, err)}
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return State{Err: fmt.Errorf("reading git state of %s: %w", abs, errMissingPath)}
		}
		return State{Err: fmt.Errorf("reading git state of %s: %w", abs, err)}
	}

	top, jj := findTopLevel(abs)
	switch {
	case top == "":
		return State{NotRepo: true}
	case jj:
		return State{Repo: top, JJ: true}
	}

	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	// #nosec G204 -- fixed git argv; abs is a directory passed via -C, not a shell string
	cmd := exec.CommandContext(ctx, "git", "-C", abs, "--no-optional-locks", "status", "--porcelain=v2", "--branch")
	cmd.Env = childenv.WithoutClaudeJobDir()
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return State{Repo: top, Err: fmt.Errorf("reading git status of %s: %w", abs, err)}
	}
	st := ParseStatusV2(string(out))
	st.Repo = top
	return st
}

// findTopLevel walks up from dir to the nearest directory holding .jj or
// .git. It returns "" when there is none. A directory holding .jj (alone or
// colocated with .git) is a Jujutsu workspace.
func findTopLevel(dir string) (top string, jj bool) {
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".jj")); err == nil && fi.IsDir() {
			return dir, true
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ParseStatusV2 parses `git status --porcelain=v2 --branch` output. Repo is
// left empty.
func ParseStatusV2(out string) State {
	var st State
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "# ") {
			// Any entry line (changed, renamed, unmerged, untracked) is dirt.
			// Ignored entries ("!") only appear with --ignored, never here.
			st.Dirty = true
			continue
		}
		key, val, _ := strings.Cut(strings.TrimPrefix(line, "# "), " ")
		switch key {
		case "branch.head":
			st.Branch = val
		case "branch.upstream":
			st.Upstream = val
		case "branch.ab":
			a, b, ok := strings.Cut(val, " ")
			if !ok {
				continue
			}
			ahead, errA := strconv.Atoi(strings.TrimPrefix(a, "+"))
			behind, errB := strconv.Atoi(strings.TrimPrefix(b, "-"))
			if errA == nil && errB == nil {
				st.Ahead, st.Behind, st.HasCounts = ahead, behind, true
			}
		}
	}
	return st
}

// Cache holds the latest State per checkout path. It is safe for concurrent
// use; Get never runs git.
type Cache struct {
	ttl      time.Duration
	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]bool
	started  map[string]time.Time
}

type cacheEntry struct {
	state State
	at    time.Time
}

// NewCache returns an empty cache whose entries go stale after ttl.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl:      ttl,
		entries:  make(map[string]cacheEntry),
		inflight: make(map[string]bool),
		started:  make(map[string]time.Time),
	}
}

// Get returns the cached state for path, stale or not.
func (c *Cache) Get(path string) (State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	return e.state, ok
}

// Begin reports whether the caller should start a fetch for path now: there
// is no fresh entry and no fetch in flight. A true result marks the fetch as
// in flight until Put.
func (c *Cache) Begin(path string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight[path] && now.Sub(c.started[path]) < CallTimeout+c.ttl {
		return false
	}
	if e, ok := c.entries[path]; ok && now.Sub(e.at) <= c.ttl {
		return false
	}
	c.inflight[path] = true
	c.started[path] = now
	return true
}

// BeginForce marks a fetch for path as in flight regardless of freshness,
// for callers that need a current answer (e.g. a dialog opening). It always
// returns true.
func (c *Cache) BeginForce(path string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inflight[path] = true
	c.started[path] = now
	return true
}

// Put stores a fetched state and ends the in-flight fetch for path.
func (c *Cache) Put(path string, st State, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[path] = cacheEntry{state: st, at: now}
	delete(c.inflight, path)
	delete(c.started, path)
}

// Invalidate drops everything known about path.
func (c *Cache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, path)
	delete(c.inflight, path)
	delete(c.started, path)
}
