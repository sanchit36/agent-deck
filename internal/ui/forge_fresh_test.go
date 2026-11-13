package ui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	tea "github.com/charmbracelet/bubbletea"
)

// forgeClock is the fake clock the freshness tests drive.
type forgeClock struct{ now time.Time }

func (c *forgeClock) Now() time.Time          { return c.now }
func (c *forgeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// freshT0 is 20 minutes before the graphql reset in the rate-limit-status
// fixture.
var freshT0 = time.Unix(1794569400, 0).Add(-20 * time.Minute)

var (
	widgetsRepo   = github.Repo{Host: github.DefaultHost, Owner: "me", Name: "widgets"}
	widgetsIssues = freshness.ListKey(widgetsRepo, freshness.IssueList, "is:open")
)

// listArgs is the gh call the test list fetchers make for kind on host.
func listArgs(host string, kind freshness.Kind) []string {
	return []string{"api", "--hostname", host, "graphql", "-f", "query=" + string(kind)}
}

// testListFetcher stands in for the list slices' fetchers: one gh call
// through the client, whose stdout is the cached value.
func testListFetcher(ctx context.Context, c *github.Client, k freshness.Key) (any, error) {
	out, err := c.API(ctx, k.Repo.HostOrDefault(), github.Call{Args: listArgs(k.Repo.HostOrDefault(), k.Kind)[3:]})
	return strings.TrimSpace(string(out)), err
}

// freshHome is a gate-on Home on the widgets group whose list fetches go
// through fake, on a fake clock at freshT0, with a 60 s interval.
func freshHome(t *testing.T, fake *githubtest.Fake) (*Home, *forgeClock) {
	t.Helper()
	if fake == nil {
		fake = githubtest.NewFake()
	}
	h := forgeHome(t, 120, 30, false, fake, widgetsCheckout)
	clk := &forgeClock{now: freshT0}
	h.forge.now = clk.Now
	h.forge.client = github.NewClient(fake)
	h.forge.interval = time.Minute
	h.forge.fetchers = map[freshness.Kind]forgeFetchFunc{
		freshness.IssueList: testListFetcher,
		freshness.PRList:    testListFetcher,
	}
	return h, clk
}

func serveLists(fake *githubtest.Fake, f githubtest.Fixture) {
	fake.Serve(f, listArgs(github.DefaultHost, freshness.IssueList)...)
	fake.Serve(f, listArgs(github.DefaultHost, freshness.PRList)...)
}

// listCalls counts the list fetches fake has answered (GraphQL calls other
// than detail fetches).
func listCalls(fake *githubtest.Fake) int {
	n := 0
	for _, c := range fake.Calls() {
		if len(c.Args) > 3 && c.Args[3] == "graphql" && !slices.Contains(c.Args, "query="+github.DetailQuery) {
			n++
		}
	}
	return n
}

// collect runs cmd and returns the messages it produces, without feeding
// them to Home (batches flattened).
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch m := cmd().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range m {
			out = append(out, collect(c)...)
		}
		return out
	default:
		return []tea.Msg{m}
	}
}

func projectLine(h *Home) string {
	lines := frameLines(h.View())
	return lines[h.getListContentStartY()]
}

// listPane is the left pane's body as drawn: the Project line and what
// follows it.
func listPane(h *Home) string {
	return stripAnsi(h.renderLeftPane(h.forgeLeftPaneWidth(), 10))
}

// squash drops all whitespace, so text wrapped across rows still matches.
func squash(s string) string { return strings.Join(strings.Fields(s), "") }

// TestForgeFreshnessFirstOpen: opening a tab fetches its list in a tea.Cmd;
// until it lands the Project line spins and the pane says loading; then the
// line shows how old the data is.
func TestForgeFreshnessFirstOpen(t *testing.T) {
	fake := githubtest.NewFake()
	serveLists(fake, githubtest.MustLoad(t, githubtest.OK))
	h, clk := freshHome(t, fake)

	model, cmd := h.Update(altKeyMsg('i'))
	h = model.(*Home)
	if listCalls(fake) != 0 {
		t.Fatal("the key handler called gh")
	}
	var fetch tea.Cmd
	for _, m := range collect(cmd) {
		model, fetch = h.Update(m)
		h = model.(*Home)
	}
	if listCalls(fake) != 0 {
		t.Fatal("gh ran before the fetch command did")
	}
	if e, ok := h.forge.fresh.Get(widgetsIssues); !ok || !e.Fetching {
		t.Fatalf("no fetch in flight after the Project resolved: %+v", e)
	}
	if !strings.Contains(listPane(h), "loading") {
		t.Fatalf("no loading state:\n%s", listPane(h))
	}
	if line := projectLine(h); !strings.ContainsAny(line, "⣾⣽⣻⢿⡿⣟⣯⣷") {
		t.Fatalf("no spinner on the Project line: %q", line)
	}
	h = runCmds(t, h, fetch)
	if listCalls(fake) != 1 {
		t.Fatalf("%d list calls, want 1", listCalls(fake))
	}
	if line := projectLine(h); !strings.Contains(line, "updated just now") {
		t.Fatalf("Project line after the fetch: %q", line)
	}
	clk.advance(2*time.Minute + 10*time.Second)
	if line := projectLine(h); !strings.Contains(line, "updated 2m ago") {
		t.Fatalf("Project line two minutes later: %q", line)
	}
}

// TestForgeFreshnessPolls: the visible list refetches once its interval
// has passed, on the main tick, and not before.
func TestForgeFreshnessPolls(t *testing.T) {
	fake := githubtest.NewFake()
	serveLists(fake, githubtest.MustLoad(t, githubtest.OK))
	h, clk := freshHome(t, fake)
	h = drive(t, h, altKeyMsg('i'))
	if listCalls(fake) != 1 {
		t.Fatalf("%d list calls after opening the tab, want 1", listCalls(fake))
	}
	clk.advance(59 * time.Second)
	h = runCmds(t, h, h.forgeOnTick())
	if listCalls(fake) != 1 {
		t.Fatal("polled before the interval")
	}
	clk.advance(time.Second)
	h = runCmds(t, h, h.forgeOnTick())
	if listCalls(fake) != 2 {
		t.Fatalf("%d list calls at the interval, want 2", listCalls(fake))
	}

	// The main tick runs the same check.
	clk.advance(time.Minute)
	model, _ := h.Update(tickMsg(clk.now))
	h = model.(*Home)
	if e, _ := h.forge.fresh.Get(widgetsIssues); !e.Fetching {
		t.Fatal("the main tick did not start the due fetch")
	}
}

// TestForgeFreshnessManualOnlyConfig: refresh_interval = 0 never polls.
func TestForgeFreshnessManualOnlyConfig(t *testing.T) {
	fake := githubtest.NewFake()
	serveLists(fake, githubtest.MustLoad(t, githubtest.OK))
	h := forgeFrameHome(t, 120, 30, false, gateOn+"refresh_interval = 0\n")
	h.forge.resolver = github.NewResolver(github.NewClient(fake), forgeLocal(widgetsCheckout))
	if h.forge.interval != 0 {
		t.Fatalf("interval from config = %v, want 0", h.forge.interval)
	}
	clk := &forgeClock{now: freshT0}
	h.forge.now = clk.Now
	h.forge.client = github.NewClient(fake)
	h.forge.fetchers = map[freshness.Kind]forgeFetchFunc{freshness.IssueList: testListFetcher}
	h = drive(t, h, altKeyMsg('i'))
	clk.advance(time.Hour)
	h = runCmds(t, h, h.forgeOnTick())
	if listCalls(fake) != 1 {
		t.Fatalf("%d list calls with refresh_interval = 0, want 1 (the first open)", listCalls(fake))
	}
	_ = drive(t, h, runeKey('r'))
	if listCalls(fake) != 2 {
		t.Fatal("r did not refresh with refresh_interval = 0")
	}
}

// TestForgeFreshnessPauses: no polling while a session is attached, the
// embedded session has the keyboard, the terminal is in the background, or
// the Sessions tab shows.
func TestForgeFreshnessPauses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		pause, resume func(*Home) tea.Cmd
	}{
		{"attached",
			func(h *Home) tea.Cmd { h.isAttaching.Store(true); return nil },
			func(h *Home) tea.Cmd { h.isAttaching.Store(false); return h.forgeOnTick() }},
		{"embedded session focused",
			func(h *Home) tea.Cmd { h.embeddedMode = true; return nil },
			func(h *Home) tea.Cmd { h.embeddedMode = false; return h.forgeOnTick() }},
		{"terminal in the background",
			func(h *Home) tea.Cmd { _, cmd := h.Update(tea.BlurMsg{}); return cmd },
			func(h *Home) tea.Cmd { _, cmd := h.Update(tea.FocusMsg{}); return cmd }},
		{"sessions tab",
			func(h *Home) tea.Cmd { _, cmd := h.Update(altKeyMsg('s')); return cmd },
			func(h *Home) tea.Cmd { _, cmd := h.Update(altKeyMsg('i')); return cmd }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := githubtest.NewFake()
			serveLists(fake, githubtest.MustLoad(t, githubtest.OK))
			h, clk := freshHome(t, fake)
			h = drive(t, h, altKeyMsg('i'))
			h = runCmds(t, h, tc.pause(h))
			clk.advance(10 * time.Minute)
			h = runCmds(t, h, h.forgeOnTick())
			if listCalls(fake) != 1 {
				t.Fatalf("polled while paused (%d calls)", listCalls(fake))
			}
			_ = runCmds(t, h, tc.resume(h))
			if listCalls(fake) != 2 {
				t.Fatalf("did not refetch the overdue list on resuming (%d calls)", listCalls(fake))
			}
		})
	}
}

// TestForgeFreshnessManualRefresh: r fetches at once, joins a fetch in
// flight instead of starting another, and resets the poll timer.
func TestForgeFreshnessManualRefresh(t *testing.T) {
	fake := githubtest.NewFake()
	serveLists(fake, githubtest.MustLoad(t, githubtest.OK))
	h, clk := freshHome(t, fake)
	h = drive(t, h, altKeyMsg('i'))

	clk.advance(30 * time.Second)
	model, first := h.Update(runeKey('r'))
	h = model.(*Home)
	model, second := h.Update(runeKey('r'))
	h = model.(*Home)
	if second != nil {
		if msgs := collect(second); len(msgs) != 0 {
			t.Fatalf("a second r while the first is in flight produced %v", msgs)
		}
	}
	h = runCmds(t, h, first)
	if listCalls(fake) != 2 {
		t.Fatalf("%d list calls, want 2 (open + one r)", listCalls(fake))
	}
	clk.advance(59 * time.Second)
	h = runCmds(t, h, h.forgeOnTick())
	if listCalls(fake) != 2 {
		t.Fatal("the poll timer was not reset by r")
	}
	clk.advance(time.Second)
	_ = runCmds(t, h, h.forgeOnTick())
	if listCalls(fake) != 3 {
		t.Fatal("did not poll an interval after r")
	}
}

// TestForgeFreshnessStale: a failed refetch keeps the last good list on
// screen, marked ⚠ stale, with no error takeover; the next success clears it.
func TestForgeFreshnessStale(t *testing.T) {
	fake := githubtest.NewFake()
	ok := githubtest.MustLoad(t, githubtest.OK)
	failing := false
	fake.ServeFunc(githubtest.MustLoad(t, githubtest.Network), func([]string) bool { return failing })
	serveLists(fake, ok)
	h, clk := freshHome(t, fake)
	h = drive(t, h, altKeyMsg('i'))

	failing = true
	clk.advance(time.Minute)
	h = runCmds(t, h, h.forgeOnTick())
	e, _ := h.forge.fresh.Get(widgetsIssues)
	if !e.Stale() || e.Value == nil {
		t.Fatalf("entry after a failed refetch = %+v", e)
	}
	if line := projectLine(h); !strings.Contains(line, "⚠ stale") {
		t.Fatalf("Project line: %q", line)
	}
	// No takeover: the list stays, under one line saying why it is stale.
	if body := h.renderForgeListBody(60); body != nil {
		t.Fatalf("the error took over a pane that has data:\n%s", listPane(h))
	}
	if pane := listPane(h); !strings.Contains(pane, "⚠ offline · retrying") {
		t.Fatalf("no stale note:\n%s", pane)
	}
	if !h.forgeListStale() {
		t.Fatal("forgeListStale is false: the list slices would not dim the rows")
	}

	failing = false
	clk.advance(time.Minute)
	h = runCmds(t, h, h.forgeOnTick())
	if line := projectLine(h); strings.Contains(line, "stale") || !strings.Contains(line, "updated just now") {
		t.Fatalf("Project line after recovering: %q", line)
	}
}

// TestForgeFreshnessStaleSaysWhy: over the stale rows, one line says what
// failed by class (spec #18 §5), such as when a rate limit lifts; it goes
// with the next success.
func TestForgeFreshnessStaleSaysWhy(t *testing.T) {
	fake := githubtest.NewFake()
	limited := false
	fake.ServeFunc(githubtest.MustLoad(t, githubtest.RateLimited), func(args []string) bool { return limited && len(args) > 3 && args[3] == "graphql" })
	fake.Serve(githubtest.MustLoad(t, githubtest.RateLimitStatus), "api", "--hostname", "github.com", "rate_limit")
	serveIssueList(fake, githubtest.MustLoad(t, githubtest.IssueList))
	h := forgeHome(t, 120, 30, false, fake, widgetsCheckout)
	clk := &forgeClock{now: freshT0}
	h.forge.now = clk.Now
	h.forge.client = github.NewClient(fake)
	h.forge.fetchers = defaultForgeFetchers()
	h.forge.interval = time.Minute
	h = drive(t, h, altKeyMsg('i'))

	limited = true
	clk.advance(time.Minute)
	h = runCmds(t, h, h.forgeOnTick())
	want := "⚠ rate-limited until " + time.Unix(1794569400, 0).Local().Format("15:04")
	if pane := listPane(h); !strings.Contains(pane, want) || !strings.Contains(projectLine(h), "⚠ stale") {
		t.Fatalf("stale pane lacks %q:\n%s", want, pane)
	}
	if i, _ := h.forgeListItemAt(h.getListContentStartY() + 1); i != -1 {
		t.Fatalf("a click on the stale note selects item %d", i)
	}
	if i, _ := h.forgeListItemAt(h.getListContentStartY() + 2); i != 0 {
		t.Fatalf("a click on the first row selects item %d, want 0", i)
	}

	limited = false
	clk.advance(20 * time.Minute)
	h = runCmds(t, h, h.forgeOnTick())
	if pane := listPane(h); strings.Contains(pane, "rate-limited") {
		t.Fatalf("the note stayed after a success:\n%s", pane)
	}
}

// TestForgeFreshnessFailureMessages: with no cached list, each failure
// class takes over the pane with its own message (spec #18 §5).
func TestForgeFreshnessFailureMessages(t *testing.T) {
	reset := time.Unix(1794569400, 0)
	ghe := github.Repo{Host: "ghe.example.com", Owner: "acme", Name: "tools"}
	for _, tc := range []struct {
		name    string
		repo    github.Repo
		fixture string
		err     error // instead of a fixture
		want    []string
		absent  []string
	}{
		{name: "gh missing", fixture: githubtest.GhMissing, want: []string{"gh is not installed", "https://cli.github.com"}},
		{name: "not logged in", fixture: githubtest.NotLoggedIn, want: []string{"gh auth login -h github.com"}},
		{name: "bad token", fixture: githubtest.BadToken, want: []string{"gh auth login -h github.com"}},
		{name: "saml", fixture: githubtest.SAML, want: []string{"https://github.com/orgs/acme/sso?authorization_request=A1B2C3", "o open", "y copy"}},
		{name: "unknown ghe host", repo: ghe, fixture: githubtest.UnknownHost, want: []string{"gh has no login for ghe.example.com"}},
		{name: "network", fixture: githubtest.Network, want: []string{"offline · retrying"}},
		{name: "timeout", fixture: githubtest.Timeout, want: []string{"offline · retrying"}},
		{name: "rate-limited", fixture: githubtest.RateLimited, want: []string{"rate-limited until " + reset.Local().Format("15:04")}},
		{name: "issues disabled", err: github.ErrIssuesDisabled, want: []string{"Issues are disabled for me/widgets"}, absent: []string{"⚠"}},
		{name: "repo not found", fixture: githubtest.NotFound, want: []string{"No GitHub project", "me/widgets not found"}},
		{name: "server error", fixture: githubtest.ServerError, want: []string{"GitHub is not answering", "retrying"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := githubtest.NewFake()
			repo := tc.repo
			if repo.Owner == "" {
				repo = widgetsRepo
			}
			if tc.fixture != "" {
				fake.Serve(githubtest.MustLoad(t, tc.fixture), listArgs(repo.HostOrDefault(), freshness.IssueList)...)
			}
			fake.Serve(githubtest.MustLoad(t, githubtest.RateLimitStatus), "api", "--hostname", "github.com", "rate_limit")
			fake.Serve(githubtest.MustLoad(t, githubtest.AuthStatusNoHost), "auth", "status", "--hostname", "ghe.example.com")
			h, _ := freshHome(t, fake)
			if tc.err != nil {
				h.forge.fetchers[freshness.IssueList] = func(context.Context, *github.Client, freshness.Key) (any, error) { return nil, tc.err }
			}
			if tc.repo.Owner != "" {
				forgeShowRepo(h, tc.repo)
			}
			h = drive(t, h, altKeyMsg('i'))
			pane := listPane(h)
			for _, w := range tc.want {
				if !strings.Contains(squash(pane), squash(w)) {
					t.Errorf("pane lacks %q:\n%s", w, pane)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(pane, a) {
					t.Errorf("pane has %q:\n%s", a, pane)
				}
			}
		})
	}
}

// forgeShowRepo makes the widgets group's only Project repo, skipping
// resolution.
func forgeShowRepo(h *Home, repo github.Repo) {
	if h.forge.views == nil {
		h.forge.views = map[string]*forgeGroupView{}
	}
	h.forge.views["widgets"] = &forgeGroupView{group: github.Group{Projects: []github.Project{{Repo: repo, Root: "/src/widgets"}}}}
}

// TestForgeFreshnessRateLimitStopsPolling: after a rate limit polling stops
// until the reset, r included, then resumes.
func TestForgeFreshnessRateLimitStopsPolling(t *testing.T) {
	fake := githubtest.NewFake()
	limited := true
	fake.ServeFunc(githubtest.MustLoad(t, githubtest.RateLimited), func(args []string) bool { return limited && len(args) > 3 && args[3] == "graphql" })
	fake.Serve(githubtest.MustLoad(t, githubtest.RateLimitStatus), "api", "--hostname", "github.com", "rate_limit")
	serveLists(fake, githubtest.MustLoad(t, githubtest.OK))
	h, clk := freshHome(t, fake)
	h = drive(t, h, altKeyMsg('i'))
	for range 19 {
		clk.advance(time.Minute)
		h = runCmds(t, h, h.forgeOnTick())
		h = drive(t, h, runeKey('r'))
		h = drive(t, h, altKeyMsg('p'))
		h = drive(t, h, altKeyMsg('i'))
	}
	if listCalls(fake) != 1 {
		t.Fatalf("%d list calls during the rate limit, want 1", listCalls(fake))
	}
	limited = false
	clk.advance(time.Minute)
	h = runCmds(t, h, h.forgeOnTick())
	if listCalls(fake) != 2 {
		t.Fatal("polling did not resume at the reset")
	}
	if line := projectLine(h); !strings.Contains(line, "updated just now") {
		t.Fatalf("Project line after the reset: %q", line)
	}
}

// TestForgeFreshnessNoAutoRetryForAuth: auth failures wait for r.
func TestForgeFreshnessNoAutoRetryForAuth(t *testing.T) {
	fake := githubtest.NewFake()
	serveLists(fake, githubtest.MustLoad(t, githubtest.BadToken))
	h, clk := freshHome(t, fake)
	h = drive(t, h, altKeyMsg('i'))
	clk.advance(time.Hour)
	h = runCmds(t, h, h.forgeOnTick())
	if listCalls(fake) != 1 {
		t.Fatalf("retried a bad token automatically (%d calls)", listCalls(fake))
	}
	_ = drive(t, h, runeKey('r'))
	if listCalls(fake) != 2 {
		t.Fatal("r did not retry")
	}
}

// TestForgeFreshnessLateResult: a result that arrives after the user moved
// to another tab or Project is cached under its own key and never shown in
// the view on screen; going back shows it at once.
func TestForgeFreshnessLateResult(t *testing.T) {
	fake := githubtest.NewFake()
	serveLists(fake, githubtest.MustLoad(t, githubtest.OK))
	h, clk := freshHome(t, fake)

	model, cmd := h.Update(altKeyMsg('i'))
	h = model.(*Home)
	var issuesFetch tea.Cmd
	for _, m := range collect(cmd) {
		model, issuesFetch = h.Update(m)
		h = model.(*Home)
	}
	// Move to PRs before the Issues list lands; leave the PRs fetch pending.
	model, _ = h.Update(altKeyMsg('p'))
	h = model.(*Home)
	h = runCmds(t, h, issuesFetch)

	if e, ok := h.forge.fresh.Get(widgetsIssues); !ok || !e.HasData() {
		t.Fatalf("the late Issues result was not cached under its key: %+v", e)
	}
	if line := projectLine(h); strings.Contains(line, "updated") {
		t.Fatalf("the PRs tab shows the Issues result's age: %q", line)
	}
	if !strings.Contains(listPane(h), "loading") {
		t.Fatalf("the PRs tab is not loading:\n%s", listPane(h))
	}

	calls := listCalls(fake)
	clk.advance(10 * time.Second)
	h = drive(t, h, altKeyMsg('i'))
	if line := projectLine(h); !strings.Contains(line, "updated just now") {
		t.Fatalf("back on Issues the cache is not shown at once: %q", line)
	}
	if listCalls(fake) != calls {
		t.Fatal("refetched a list younger than the interval")
	}
	clk.advance(time.Minute)
	h = drive(t, h, altKeyMsg('p'))
	h = drive(t, h, altKeyMsg('i'))
	if line := projectLine(h); !strings.Contains(line, "1m ago") && !strings.Contains(line, "updated just now") {
		t.Fatalf("Project line: %q", line)
	}
	if listCalls(fake) <= calls {
		t.Fatal("going back to a list older than the interval did not refetch")
	}
}

// TestForgeFreshnessSAMLOpenCopy: on the SAML message, o opens and y
// copies the authorize URL, each in a tea.Cmd.
func TestForgeFreshnessSAMLOpenCopy(t *testing.T) {
	const url = "https://github.com/orgs/acme/sso?authorization_request=A1B2C3"
	fake := githubtest.NewFake()
	serveLists(fake, githubtest.MustLoad(t, githubtest.SAML))
	h, _ := freshHome(t, fake)
	var opened, copied []string
	h.forge.openURL = func(u string) error { opened = append(opened, u); return nil }
	h.forge.copyURL = func(u string) error { copied = append(copied, u); return nil }
	h = drive(t, h, altKeyMsg('i'))

	model, cmd := h.Update(runeKey('o'))
	h = model.(*Home)
	if len(opened) != 0 {
		t.Fatal("o opened the browser in the key handler")
	}
	h = runCmds(t, h, cmd)
	model, cmd = h.Update(runeKey('y'))
	h = model.(*Home)
	if len(copied) != 0 {
		t.Fatal("y copied in the key handler")
	}
	_ = runCmds(t, h, cmd)
	if len(opened) != 1 || opened[0] != url || len(copied) != 1 || copied[0] != url {
		t.Fatalf("opened %v, copied %v", opened, copied)
	}
}

// TestForgeStopJoinsFetches: stopping the forge cancels a fetch in flight
// and waits for it; a fetch command run after the stop never calls gh.
func TestForgeStopJoinsFetches(t *testing.T) {
	h, _ := freshHome(t, nil)
	started := make(chan struct{})
	finished := make(chan struct{})
	calls := 0
	h.forge.fetchers[freshness.IssueList] = func(ctx context.Context, _ *github.Client, _ freshness.Key) (any, error) {
		calls++
		close(started)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		close(finished)
		return nil, ctx.Err()
	}
	forgeShowRepo(h, widgetsRepo)
	model, cmd := h.Update(altKeyMsg('i'))
	h = model.(*Home)
	done := make(chan []tea.Msg)
	go func() { done <- collect(cmd) }()
	<-started

	stopped := make(chan struct{})
	go func() { h.stopForge(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stopForge did not return")
	}
	select {
	case <-finished:
	default:
		t.Fatal("stopForge returned before the fetch did")
	}
	<-done

	h.forge.fetchers[freshness.PRList] = func(context.Context, *github.Client, freshness.Key) (any, error) {
		t.Error("a fetch ran after stopForge")
		return nil, nil
	}
	_, cmd = h.Update(altKeyMsg('p'))
	_ = collect(cmd)
	if calls != 1 {
		t.Fatalf("%d fetches, want 1", calls)
	}
}
