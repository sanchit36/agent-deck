// Package githubtest is the test double for internal/github: a fake Runner
// that serves recorded gh fixtures, and the fixtures themselves.
//
// A fixture is a directory under testdata/ holding what one gh call printed:
// `stdout`, `stderr` and `exit` (the exit code; missing means 0). A fixture
// for a gh that never ran instead holds an `error` file: `gh-missing` (gh
// not on PATH) or `timeout` (the call's timeout elapsed). Stdout and stderr
// are gh's own words (gh 2.103), recorded or taken from cli/cli's messages;
// the tests classify them exactly as they arrive from the real gh.
package githubtest

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

//go:embed testdata
var fixtures embed.FS

// Fixture is one recorded gh call. Err is set for a gh that did not run to
// completion and wraps the same sentinel github.ExecRunner returns.
type Fixture struct {
	Name   string
	Result github.Result
	Err    error
}

// Fixture names, one per error class plus the success cases the tests use.
const (
	OK                = "ok"
	GhMissing         = "gh-missing"
	Timeout           = "timeout"
	NotLoggedIn       = "not-logged-in"
	BadToken          = "bad-token"
	SAML              = "saml"
	UnknownHost       = "unknown-host"
	NotFound          = "not-found"
	Network           = "network"
	RateLimited       = "rate-limited"
	ServerError       = "server-error"
	Failed            = "failed"
	RateLimitStatus   = "rate-limit-status"
	AuthStatusOK      = "auth-status-ok"
	AuthStatusNoHost  = "auth-status-no-host"
	AuthStatusOffline = "auth-status-offline"
	// RepoFork is `gh api repos/me/widgets`: a fork whose parent is
	// acme/widgets (GitHub's REST repository shape, trimmed).
	RepoFork = "repo-fork"
	// RepoSource is `gh api repos/acme/widgets`: not a fork.
	RepoSource = "repo-source"
	// RepoNotFound is `gh api repos/...` for a repository that does not
	// exist or is not visible.
	RepoNotFound = "repo-not-found"
	// SignalsPoll is an Attention signal poll of acme/widgets (r0) with
	// Issue #12 (i0) open and PR #34 (i1) open, checks passing and a
	// review required, plus one review request (acme/widgets#77).
	SignalsPoll = "signals-poll"
	// SignalsPollChanged is the same poll later: #12 closed, #34 failing
	// with changes requested, and a second review request
	// (acme/gadgets#81).
	SignalsPollChanged = "signals-poll-changed"
	// SignalsPollRepoGone is the poll after acme/widgets went away: gh
	// exits 1 with the partial data (r0 null) on stdout.
	SignalsPollRepoGone = "signals-poll-repo-gone"
	// IssueList is the lean Issues list query (github.IssueListQuery) for
	// cli/cli, recorded with gh 2.103 and trimmed to six of its 1075 open
	// Issues; the last one's author is a deleted account (null).
	IssueList = "issue-list"
	// IssueListDisabled is the same query for torvalds/linux, which has
	// Issues turned off.
	IssueListDisabled = "issue-list-disabled"
	// PRList is the lean PRs list query (github.PRListQuery) for cli/cli,
	// recorded with gh 2.103 and trimmed to six of its 59 open PRs: checks
	// passing, failing and absent; review required, approved and changes
	// requested; drafts; and PRs from forks.
	PRList = "pr-list"
	// IssueSearch is the Issues search (github.IssueSearchQuery) for
	// `repo:cli/cli is:issue is:closed label:bug sort:updated-desc`,
	// recorded with gh 2.103 and trimmed to four of its 2002 matches.
	IssueSearch = "issue-search"
	// PRSearch is the PRs search (github.PRSearchQuery) for
	// `repo:cli/cli is:pr is:merged sort:updated-desc`, recorded with gh
	// 2.103 and trimmed to four of its 3359 matches; one is from a fork
	// and has no check rollup.
	PRSearch = "pr-search"
	// SearchRejected is a search GitHub rejects: gh exits 1 with the
	// GraphQL error on stdout and its message on stderr. Hand-written in
	// the shape gh 2.103 prints for a rejected search argument (recorded
	// from `search(first:101)`); GitHub search tolerates almost any user
	// query, so no user query was found that it rejects.
	SearchRejected = "search-rejected"
	// IssueDetail is the detail query (github.DetailQuery) for cli/cli
	// Issue #14822, recorded with gh 2.103: open, three labels, five
	// comments.
	IssueDetail = "issue-detail"
	// PRDetail is the detail query for cli/cli PR #14927, recorded with gh
	// 2.103: a draft with one failing check of 25, review required, four
	// commits and one review.
	PRDetail = "pr-detail"
)

// Load returns the fixture named name.
func Load(name string) (Fixture, error) {
	dir := path.Join("testdata", name)
	if _, err := fs.Stat(fixtures, dir); err != nil {
		return Fixture{}, fmt.Errorf("loading gh fixture %q: %w", name, err)
	}
	f := Fixture{Name: name}
	read := func(file string) ([]byte, error) {
		b, err := fs.ReadFile(fixtures, path.Join(dir, file))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	var err error
	if f.Result.Stdout, err = read("stdout"); err != nil {
		return Fixture{}, fmt.Errorf("reading stdout of gh fixture %q: %w", name, err)
	}
	if f.Result.Stderr, err = read("stderr"); err != nil {
		return Fixture{}, fmt.Errorf("reading stderr of gh fixture %q: %w", name, err)
	}
	exit, err := read("exit")
	if err != nil {
		return Fixture{}, fmt.Errorf("reading exit of gh fixture %q: %w", name, err)
	}
	if s := strings.TrimSpace(string(exit)); s != "" {
		if f.Result.ExitCode, err = strconv.Atoi(s); err != nil {
			return Fixture{}, fmt.Errorf("parsing exit of gh fixture %q: %w", name, err)
		}
	}
	runErr, err := read("error")
	if err != nil {
		return Fixture{}, fmt.Errorf("reading error of gh fixture %q: %w", name, err)
	}
	switch kind := strings.TrimSpace(string(runErr)); kind {
	case "":
	case "gh-missing":
		f.Err = fmt.Errorf("looking up gh: %w", exec.ErrNotFound)
	case "timeout":
		f.Err = fmt.Errorf("running gh: timed out: %w", context.DeadlineExceeded)
	default:
		return Fixture{}, fmt.Errorf("gh fixture %q: unknown error kind %q", name, kind)
	}
	return f, nil
}

// MustLoad is Load that fails the test on error.
func MustLoad(t testing.TB, name string) Fixture {
	t.Helper()
	f, err := Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Names lists every fixture.
func Names() []string {
	entries, err := fs.ReadDir(fixtures, "testdata")
	if err != nil {
		panic(fmt.Sprintf("reading embedded gh fixtures: %v", err))
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

type route struct {
	match   func(args []string) bool
	fixture Fixture
}

// Fake is a github.Runner that answers each call with the fixture of the
// first route whose args match, and records every call. A call no route
// matches gets exit 1 and a stderr naming the call, so a missing route
// fails the test that made it instead of passing silently.
type Fake struct {
	mu     sync.Mutex
	routes []route
	calls  []github.Call
}

// NewFake returns a Fake with no routes.
func NewFake() *Fake { return &Fake{} }

// Serve answers calls whose args equal args exactly with f.
func (k *Fake) Serve(f Fixture, args ...string) *Fake {
	want := slices.Clone(args)
	return k.ServeFunc(f, func(got []string) bool { return slices.Equal(got, want) })
}

// ServeFunc answers calls whose args satisfy match with f.
func (k *Fake) ServeFunc(f Fixture, match func(args []string) bool) *Fake {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.routes = append(k.routes, route{match: match, fixture: f})
	return k
}

// Calls returns the calls made so far, in order.
func (k *Fake) Calls() []github.Call {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.calls)
}

// Run implements github.Runner.
func (k *Fake) Run(ctx context.Context, call github.Call) (github.Result, error) {
	call.Args = slices.Clone(call.Args)
	k.mu.Lock()
	k.calls = append(k.calls, call)
	routes := slices.Clone(k.routes)
	k.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return github.Result{}, fmt.Errorf("running gh: %w", err)
	}
	for _, r := range routes {
		if r.match(call.Args) {
			res := r.fixture.Result
			res.Stdout = slices.Clone(res.Stdout)
			res.Stderr = slices.Clone(res.Stderr)
			return res, r.fixture.Err
		}
	}
	return github.Result{
		Stderr:   []byte(fmt.Sprintf("githubtest: no fixture for gh %s\n", strings.Join(call.Args, " "))),
		ExitCode: 1,
	}, nil
}
